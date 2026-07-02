package capture

import (
	"database/sql"
	"regexp"
	"strings"
)

// (hashRe defined at the bottom of this file guards git argv inputs.)

var revertsCommitRe = regexp.MustCompile(`This reverts commit ([0-9a-f]{7,40})`)

// ScanReverts maps revert commits in a repo back to the runs that produced
// the reverted work. A run owns the commits in its head_before..head_after
// range (recorded by hooks/wrap). Each reverted commit yields one
// revert_detected event on its run (deduped by hash) — a real acceptance
// signal, not a proxy. Returns the number of new detections.
func (g *Ingestor) ScanReverts(repoDir string) (int, error) {
	// 1. Which commit hashes were reverted, per `git revert` message trailers.
	log, err := gitOut(repoDir, "log", "--grep=This reverts commit", "--format=%B%x00")
	if err != nil {
		return 0, nil // not a repo / no git — silent skip (capture is best-effort)
	}
	reverted := map[string]bool{}
	for _, m := range revertsCommitRe.FindAllStringSubmatch(log, -1) {
		reverted[m[1]] = true
	}
	if len(reverted) == 0 {
		return 0, nil
	}

	// 2. Map run → its commit range, check membership. Only runs rooted in
	// this repo (cwd prefix) — scanning every run ever recorded would spawn
	// one git subprocess per row forever.
	rows, err := g.St.DB.Query(`SELECT id, head_before, head_after FROM runs
		WHERE head_before IS NOT NULL AND head_after IS NOT NULL AND head_before != head_after
		AND cwd LIKE ? || '%'`, repoDir)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type runRange struct{ id, before, after string }
	var ranges []runRange
	for rows.Next() {
		var r runRange
		if err := rows.Scan(&r.id, &r.before, &r.after); err != nil {
			return 0, err
		}
		ranges = append(ranges, r)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	// Prefetch existing detections once (avoids a SELECT per hash).
	seen := map[string]bool{}
	seenRows, err := g.St.DB.Query(`SELECT run_id, payload FROM events WHERE kind = 'revert_detected'`)
	if err != nil {
		return 0, err
	}
	for seenRows.Next() {
		var runID, hash string
		if err := seenRows.Scan(&runID, &hash); err != nil {
			seenRows.Close()
			return 0, err
		}
		seen[runID+"|"+hash] = true
	}
	seenRows.Close()

	detected := 0
	for _, r := range ranges {
		if !hashRe.MatchString(r.before) || !hashRe.MatchString(r.after) {
			continue // never pass non-hash DB strings to git argv
		}
		out, err := gitOut(repoDir, "rev-list", r.before+".."+r.after)
		if err != nil {
			continue // range not in this repo
		}
		for _, hash := range strings.Fields(out) {
			if !revertedMatch(reverted, hash) || seen[r.id+"|"+hash] {
				continue
			}
			if _, err := g.AddEvent(r.id, "revert_detected", "git",
				sql.NullInt64{Int64: 0, Valid: true}, hash); err != nil {
				return detected, err
			}
			seen[r.id+"|"+hash] = true
			detected++
		}
	}
	return detected, nil
}

var hashRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// revertedMatch handles short-hash trailers against full rev-list hashes.
func revertedMatch(reverted map[string]bool, fullHash string) bool {
	if reverted[fullHash] {
		return true
	}
	for h := range reverted {
		if len(h) < len(fullHash) && strings.HasPrefix(fullHash, h) {
			return true
		}
	}
	return false
}
