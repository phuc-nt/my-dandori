package contexthub

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Merge = concatenation in precedence order (company → team → agent) with
// fixed layer headers, boundary markers, and per-layer RUNE budgets, capped
// ≤ totalCap. Rationale (rejected whole-doc override): org policy semantics
// require company rules to ALWAYS apply — a team doc refines, never erases,
// company policy. Lower layers come last so they refine the higher ones.
const (
	hdrCompany = "## Chính sách công ty"
	hdrTeam    = "## Quy ước đội"
	hdrAgent   = "## Riêng agent"

	// Budgets in RUNES (Vietnamese is multi-byte — count runes, not bytes).
	// company 3800 + team 3800 + agent 2000 = 9600, plus headers/markers/joins
	// (~150) < the 10 000-rune SessionStart cap. The total-cap step below is a
	// belt-and-suspenders guarantee the closing marker always survives.
	budCompany = 3800
	budTeam    = 3800
	budAgent   = 2000
	totalCap   = 10000

	truncMark   = "\n…[cắt bớt]"
	markerBegin = "<!-- dandori-context: begin"
	markerEnd   = "<!-- dandori-context: end -->"
	markerToken = "dandori-context:" // literal that must not be forgeable in content
)

// Provenance records which version of each included layer was merged — drives
// the context_injected event and the preview labels. nil = layer absent.
type Provenance struct {
	Company *int `json:"company,omitempty"`
	Team    *int `json:"team,omitempty"`
	Agent   *int `json:"agent,omitempty"`
}

// EffectiveContext resolves the merged context an agent should receive.
// Deterministic (same DB → identical bytes, required for the E2E byte-match),
// pure read, output always ≤ totalCap runes and always ends with the closing
// marker. Empty (no docs) → "" and a zero Provenance.
func (h *Hub) EffectiveContext(agentID string) (string, Provenance, error) {
	var prov Provenance

	type layer struct{ hdr, content string }
	var layers []layer

	if d, err := h.Head(LayerCompany, CompanyTarget); err != nil {
		return "", prov, err
	} else if d != nil && !blank(d.Content) {
		v := d.VersionN
		prov.Company = &v
		layers = append(layers, layer{hdrCompany, d.Content})
	}

	if teamID, ok := h.resolveTeam(agentID); ok {
		if d, err := h.Head(LayerTeam, teamID); err != nil {
			return "", prov, err
		} else if d != nil && !blank(d.Content) {
			v := d.VersionN
			prov.Team = &v
			layers = append(layers, layer{hdrTeam, d.Content})
		}
	}

	if d, err := h.Head(LayerAgent, agentID); err != nil {
		return "", prov, err
	} else if d != nil && !blank(d.Content) {
		v := d.VersionN
		prov.Agent = &v
		layers = append(layers, layer{hdrAgent, d.Content})
	}

	if len(layers) == 0 {
		return "", Provenance{}, nil
	}

	budgets := map[string]int{hdrCompany: budCompany, hdrTeam: budTeam, hdrAgent: budAgent}
	var blocks []string
	for _, l := range layers {
		body := budget(neutralize(l.content), budgets[l.hdr])
		blocks = append(blocks, l.hdr+"\n"+body)
	}

	out := beginMarker(prov) + "\n" + strings.Join(blocks, "\n\n") + "\n" + markerEnd

	// Total-cap enforcement (H2): trim the agent block's tail (lowest
	// precedence) until the whole string fits — the closing marker must
	// always survive. Per-layer budgets make this a rare no-op.
	out = enforceTotalCap(out)
	return out, prov, nil
}

// beginMarker lists only the present layers, e.g.
// "<!-- dandori-context: begin (company v3, agent v2) -->".
func beginMarker(p Provenance) string {
	var parts []string
	if p.Company != nil {
		parts = append(parts, fmt.Sprintf("company v%d", *p.Company))
	}
	if p.Team != nil {
		parts = append(parts, fmt.Sprintf("team v%d", *p.Team))
	}
	if p.Agent != nil {
		parts = append(parts, fmt.Sprintf("agent v%d", *p.Agent))
	}
	return fmt.Sprintf("%s (%s) -->", markerBegin, strings.Join(parts, ", "))
}

// resolveTeam picks the agent's team. Multiple teams → lowest team_id
// (deterministic; single-team is the MVP norm). No team → ok=false.
func (h *Hub) resolveTeam(agentID string) (string, bool) {
	var teamID string
	// team_id is stored TEXT but is an integer id — order numerically so
	// "lowest team_id" means team 2 before team 10 (not lexical "10" < "2").
	err := h.St.Read().QueryRow(`SELECT team_id FROM team_members
		WHERE member_type = 'agent' AND member_id = ?
		ORDER BY CAST(team_id AS INTEGER) ASC LIMIT 1`, agentID).Scan(&teamID)
	if err != nil {
		return "", false
	}
	return teamID, true
}

// blank treats a whitespace-only head as absent: emptying a doc is the only
// way to retire it, so a blank head must not emit a titled empty block that
// wastes budget and lists a phantom version.
func blank(s string) bool { return strings.TrimSpace(s) == "" }

// neutralize defuses any literal marker token inside content so a sloppy
// paste or malicious edit cannot forge a begin/end marker and escape the
// boundary. Replaces the ASCII ':' with U+2236 (ratio colon).
func neutralize(s string) string {
	return strings.ReplaceAll(s, markerToken, "dandori-context∶")
}

// budget truncates content to maxRunes on a rune boundary (never splits a
// UTF-8 sequence), appending a marker when it cuts.
func budget(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	keep := maxRunes - utf8.RuneCountInString(truncMark)
	if keep < 0 {
		keep = 0
	}
	return string(r[:keep]) + truncMark
}

// enforceTotalCap trims the agent block's tail (kept just before the closing
// marker) until the string fits totalCap runes, preserving the end marker.
func enforceTotalCap(out string) string {
	if utf8.RuneCountInString(out) <= totalCap {
		return out
	}
	suffix := "\n" + markerEnd
	body := strings.TrimSuffix(out, suffix)
	over := utf8.RuneCountInString(out) - totalCap
	r := []rune(body)
	cut := len(r) - over - utf8.RuneCountInString(truncMark)
	if cut < 0 {
		cut = 0
	}
	return string(r[:cut]) + truncMark + suffix
}
