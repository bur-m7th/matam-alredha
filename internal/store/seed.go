package store

import (
	"encoding/json"
	"fmt"
	"log"
)

// SeedMember mirrors one row of the spreadsheet supplied by the institution.
type SeedMember struct {
	Name  string `json:"name"`
	CPR   string `json:"cpr"`
	DOB   string `json:"dob"`
	Phone string `json:"phone"`
	House string `json:"house"`
	Road  string `json:"road"`
	Block string `json:"block"`
}

// SeedMembers imports the historical membership list on first boot only. Rows
// are inserted directly as approved members: they already exist, so they never
// pass through the request queue.
//
// Note: a handful of legacy rows share a contact number between family members.
// The uniqueness rule is enforced on new submissions, and the import keeps the
// existing rows as they are rather than dropping real members.
func (s *Store) SeedMembers(raw []byte) (int, error) {
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, nil // already imported
	}

	var members []SeedMember
	if err := json.Unmarshal(raw, &members); err != nil {
		return 0, fmt.Errorf("parse seed data: %w", err)
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO users
		 (application_id, name, cpr, dob, phone, email, house, road, block,
		  volunteer, volunteer_field, affiliated, affiliation, extra, source, created_at)
		VALUES (NULL,?,?,?,?,'',?,?,?,0,'',0,'','{}','import',?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	now := Now()
	inserted := 0
	for _, m := range members {
		cpr := NormalizeCPR(m.CPR)
		if len(cpr) != 9 {
			log.Printf("seed: skipping %q, personal number %q is not nine digits", m.Name, m.CPR)
			continue
		}
		phone := DigitsOnly(m.Phone)
		res, err := stmt.Exec(m.Name, cpr, m.DOB, phone, m.House, m.Road, m.Block, now)
		if err != nil {
			return 0, fmt.Errorf("insert %q: %w", m.Name, err)
		}
		if aff, _ := res.RowsAffected(); aff > 0 {
			inserted++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.Audit("system", "seed_members", fmt.Sprintf("استيراد %d عضو من الملف الأصلي", inserted))
	return inserted, nil
}
