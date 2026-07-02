package store

import "database/sql"

// Operators are the humans driving agents. In central mode the id is the
// auth principal (username@hostname) resolved server-side from the ingest
// token + hint header — never trusted from an event body.

// ResolveOperator returns the operator id for a principal, creating the row
// on first sight. The id is immutable once recorded.
func (s *Store) ResolveOperator(principal string) (string, error) {
	if principal == "" {
		return "", nil
	}
	var id string
	err := s.DB.QueryRow(`SELECT id FROM operators WHERE id = ?`, principal).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	_, err = s.DB.Exec(`INSERT INTO operators(id, display, created_at) VALUES(?, ?, ?)
		ON CONFLICT(id) DO NOTHING`, principal, principal, Now())
	return principal, err
}

type Team struct {
	ID   int64
	Name string
}

type TeamMember struct {
	Type string // operator | agent
	ID   string
}

// CreateTeam inserts a team (idempotent by name) and returns its id.
func (s *Store) CreateTeam(name string) (int64, error) {
	if _, err := s.DB.Exec(`INSERT INTO teams(name, created_at) VALUES(?, ?)
		ON CONFLICT(name) DO NOTHING`, name, Now()); err != nil {
		return 0, err
	}
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM teams WHERE name = ?`, name).Scan(&id)
	return id, err
}

// AssignMember puts an operator or agent into a team. The polymorphic
// member_id relation is enforced here, not by an FK (SQLite limitation).
func (s *Store) AssignMember(teamID int64, memberType, memberID string) error {
	if memberType != "operator" && memberType != "agent" {
		return sql.ErrNoRows
	}
	table := "operators"
	if memberType == "agent" {
		table = "agents"
	}
	var exists int
	if err := s.DB.QueryRow(`SELECT count(*) FROM `+table+` WHERE id = ?`, memberID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return sql.ErrNoRows
	}
	_, err := s.DB.Exec(`INSERT INTO team_members(team_id, member_type, member_id, added_at)
		VALUES(?, ?, ?, ?) ON CONFLICT DO NOTHING`, teamID, memberType, memberID, Now())
	return err
}

// UnassignMember removes a member from a team.
func (s *Store) UnassignMember(teamID int64, memberType, memberID string) error {
	_, err := s.DB.Exec(`DELETE FROM team_members WHERE team_id = ? AND member_type = ? AND member_id = ?`,
		teamID, memberType, memberID)
	return err
}

// ListTeams returns all teams ordered by name.
func (s *Store) ListTeams() ([]Team, error) {
	rows, err := s.Read().Query(`SELECT id, name FROM teams ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ts []Team
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		ts = append(ts, t)
	}
	return ts, rows.Err()
}

// TeamMembers returns a team's members (operators + agents).
func (s *Store) TeamMembers(teamID int64) ([]TeamMember, error) {
	rows, err := s.Read().Query(`SELECT member_type, member_id FROM team_members
		WHERE team_id = ? ORDER BY member_type, member_id`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ms []TeamMember
	for rows.Next() {
		var m TeamMember
		if err := rows.Scan(&m.Type, &m.ID); err != nil {
			return nil, err
		}
		ms = append(ms, m)
	}
	return ms, rows.Err()
}
