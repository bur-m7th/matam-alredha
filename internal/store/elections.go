package store

import (
	"errors"
	"fmt"
	"strconv"
)

// Voting statuses.
const (
	VotingDraft  = "draft"  // being prepared, invisible to members
	VotingOpen   = "open"   // members may cast a ballot
	VotingClosed = "closed" // finished, results locked
)

func (s *Store) VotingStatus() string { return s.Setting("voting_status", VotingDraft) }
func (s *Store) VotingMode() string {
	m := s.Setting("voting_mode", "roles")
	if m != "single" {
		return "roles"
	}
	return m
}

func (s *Store) SetVotingStatus(status, actor string) error {
	switch status {
	case VotingDraft, VotingOpen, VotingClosed:
	default:
		return errors.New("حالة تصويت غير معروفة")
	}
	if status == VotingOpen {
		if err := s.validateVotingSetup(); err != nil {
			return err
		}
	}
	if err := s.SetSetting("voting_status", status); err != nil {
		return err
	}
	s.Audit(actor, "voting_status", status)
	return nil
}

// validateVotingSetup blocks opening a vote that members could not complete.
func (s *Store) validateVotingSetup() error {
	if s.VotingMode() == "single" {
		ps, err := s.Participants(0, false)
		if err != nil {
			return err
		}
		if len(ps) < 2 {
			return errors.New("يجب إضافة مرشحين اثنين على الأقل قبل فتح التصويت")
		}
		sel := s.SingleSelections()
		if sel > len(ps) {
			return errors.New("عدد الاختيارات المسموح بها أكبر من عدد المرشحين")
		}
		return nil
	}
	roles, err := s.Roles()
	if err != nil {
		return err
	}
	if len(roles) == 0 {
		return errors.New("يجب إضافة منصب واحد على الأقل قبل فتح التصويت")
	}
	for _, r := range roles {
		ps, err := s.Participants(r.ID, false)
		if err != nil {
			return err
		}
		if len(ps) == 0 {
			return fmt.Errorf("لا يوجد مرشحون في المنصب: %s", r.Name)
		}
		if r.SelectionsAllowed > len(ps) {
			return fmt.Errorf("عدد الاختيارات في المنصب %s أكبر من عدد المرشحين", r.Name)
		}
	}
	return nil
}

func (s *Store) SingleWinners() int {
	n, _ := strconv.Atoi(s.Setting("single_winners_count", "1"))
	if n < 1 {
		return 1
	}
	return n
}

func (s *Store) SingleSelections() int {
	n, _ := strconv.Atoi(s.Setting("single_selections", "1"))
	if n < 1 {
		return 1
	}
	return n
}

// ---------------------------------------------------------------- roles

func (s *Store) Roles() ([]Role, error) {
	rows, err := s.DB.Query(
		`SELECT id, name, description, winners_count, selections_allowed, sort_order
		 FROM roles ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.WinnersCount,
			&r.SelectionsAllowed, &r.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Role(id int64) (Role, error) {
	var r Role
	err := s.DB.QueryRow(
		`SELECT id, name, description, winners_count, selections_allowed, sort_order
		 FROM roles WHERE id = ?`, id).
		Scan(&r.ID, &r.Name, &r.Description, &r.WinnersCount, &r.SelectionsAllowed, &r.SortOrder)
	return r, err
}

func normalizeRole(r *Role) error {
	r.Name = CleanText(r.Name, 120)
	if r.Name == "" {
		return errors.New("اسم المنصب مطلوب")
	}
	r.Description = CleanMultiline(r.Description, 600)
	if r.WinnersCount < 1 {
		r.WinnersCount = 1
	}
	if r.SelectionsAllowed < 1 {
		r.SelectionsAllowed = r.WinnersCount
	}
	return nil
}

func (s *Store) CreateRole(r Role, actor string) (Role, error) {
	if err := normalizeRole(&r); err != nil {
		return r, err
	}
	var maxOrder int
	_ = s.DB.QueryRow(`SELECT COALESCE(MAX(sort_order),0) FROM roles`).Scan(&maxOrder)
	res, err := s.DB.Exec(
		`INSERT INTO roles(name, description, winners_count, selections_allowed, sort_order)
		 VALUES (?,?,?,?,?)`,
		r.Name, r.Description, r.WinnersCount, r.SelectionsAllowed, maxOrder+10)
	if err != nil {
		return r, err
	}
	r.ID, _ = res.LastInsertId()
	r.SortOrder = maxOrder + 10
	s.Audit(actor, "create_role", r.Name)
	return r, nil
}

func (s *Store) UpdateRole(r Role, actor string) error {
	if err := normalizeRole(&r); err != nil {
		return err
	}
	_, err := s.DB.Exec(
		`UPDATE roles SET name=?, description=?, winners_count=?, selections_allowed=?, sort_order=?
		 WHERE id=?`,
		r.Name, r.Description, r.WinnersCount, r.SelectionsAllowed, r.SortOrder, r.ID)
	if err == nil {
		s.Audit(actor, "update_role", r.Name)
	}
	return err
}

func (s *Store) DeleteRole(id int64, actor string) error {
	if s.VotingStatus() == VotingOpen {
		return errors.New("لا يمكن تعديل المناصب أثناء فتح التصويت")
	}
	r, err := s.Role(id)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`DELETE FROM roles WHERE id=?`, id)
	if err == nil {
		s.Audit(actor, "delete_role", r.Name)
	}
	return err
}

// ---------------------------------------------------------------- participants

// Participants lists candidates for a role. roleID 0 means the single-page mode
// (candidates with no role). withVotes attaches tallies, which only the
// elections admin is ever allowed to see.
func (s *Store) Participants(roleID int64, withVotes bool) ([]Participant, error) {
	var rows interface {
		Next() bool
		Scan(...any) error
		Close() error
		Err() error
	}
	var err error
	base := `SELECT p.id, COALESCE(p.role_id,0), p.name, p.description, p.photo, p.sort_order,
	         (SELECT COUNT(*) FROM votes v WHERE v.participant_id = p.id)
	         FROM participants p `
	if roleID > 0 {
		rows, err = s.DB.Query(base+`WHERE p.role_id = ? ORDER BY p.sort_order, p.id`, roleID)
	} else {
		rows, err = s.DB.Query(base + `WHERE p.role_id IS NULL ORDER BY p.sort_order, p.id`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Participant{}
	total := 0
	for rows.Next() {
		var p Participant
		var votes int
		if err := rows.Scan(&p.ID, &p.RoleID, &p.Name, &p.Description, &p.Photo, &p.SortOrder, &votes); err != nil {
			return nil, err
		}
		if withVotes {
			p.Votes = votes
			total += votes
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if withVotes {
		for i := range out {
			if total == 0 {
				out[i].Share = "0.0"
			} else {
				out[i].Share = strconv.FormatFloat(float64(out[i].Votes)*100/float64(total), 'f', 1, 64)
			}
		}
	}
	return out, nil
}

// AllParticipants is used by the elections dashboard listing.
func (s *Store) AllParticipants(withVotes bool) ([]Participant, error) {
	rows, err := s.DB.Query(
		`SELECT p.id, COALESCE(p.role_id,0), p.name, p.description, p.photo, p.sort_order,
		        (SELECT COUNT(*) FROM votes v WHERE v.participant_id = p.id)
		 FROM participants p ORDER BY COALESCE(p.role_id,0), p.sort_order, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Participant{}
	for rows.Next() {
		var p Participant
		var votes int
		if err := rows.Scan(&p.ID, &p.RoleID, &p.Name, &p.Description, &p.Photo, &p.SortOrder, &votes); err != nil {
			return nil, err
		}
		if withVotes {
			p.Votes = votes
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Participant(id int64) (Participant, error) {
	var p Participant
	err := s.DB.QueryRow(
		`SELECT id, COALESCE(role_id,0), name, description, photo, sort_order
		 FROM participants WHERE id = ?`, id).
		Scan(&p.ID, &p.RoleID, &p.Name, &p.Description, &p.Photo, &p.SortOrder)
	return p, err
}

func (s *Store) CreateParticipant(p Participant, actor string) (Participant, error) {
	p.Name = CleanText(p.Name, 120)
	if p.Name == "" {
		return p, errors.New("اسم المرشح مطلوب")
	}
	p.Description = CleanMultiline(p.Description, 1200)
	var maxOrder int
	_ = s.DB.QueryRow(`SELECT COALESCE(MAX(sort_order),0) FROM participants`).Scan(&maxOrder)
	var roleArg any
	if p.RoleID > 0 {
		roleArg = p.RoleID
	}
	res, err := s.DB.Exec(
		`INSERT INTO participants(role_id, name, description, photo, sort_order) VALUES (?,?,?,?,?)`,
		roleArg, p.Name, p.Description, p.Photo, maxOrder+10)
	if err != nil {
		return p, err
	}
	p.ID, _ = res.LastInsertId()
	p.SortOrder = maxOrder + 10
	s.Audit(actor, "create_participant", p.Name)
	return p, nil
}

func (s *Store) UpdateParticipant(p Participant, actor string) error {
	p.Name = CleanText(p.Name, 120)
	if p.Name == "" {
		return errors.New("اسم المرشح مطلوب")
	}
	p.Description = CleanMultiline(p.Description, 1200)
	var roleArg any
	if p.RoleID > 0 {
		roleArg = p.RoleID
	}
	_, err := s.DB.Exec(
		`UPDATE participants SET role_id=?, name=?, description=?, photo=?, sort_order=? WHERE id=?`,
		roleArg, p.Name, p.Description, p.Photo, p.SortOrder, p.ID)
	if err == nil {
		s.Audit(actor, "update_participant", p.Name)
	}
	return err
}

func (s *Store) DeleteParticipant(id int64, actor string) (string, error) {
	if s.VotingStatus() == VotingOpen {
		return "", errors.New("لا يمكن حذف مرشح أثناء فتح التصويت")
	}
	p, err := s.Participant(id)
	if err != nil {
		return "", err
	}
	if _, err := s.DB.Exec(`DELETE FROM participants WHERE id=?`, id); err != nil {
		return "", err
	}
	s.Audit(actor, "delete_participant", p.Name)
	return p.Photo, nil
}

// ---------------------------------------------------------------- ballots

func (s *Store) HasVoted(userID int64) bool {
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM ballots WHERE user_id = ?`, userID).Scan(&n)
	return n > 0
}

// CastBallot records a member's complete ballot in one transaction. A member
// may submit exactly once; the unique constraint on ballots.user_id is the
// backstop if two requests race.
func (s *Store) CastBallot(userID int64, selections map[int64][]int64) error {
	if s.VotingStatus() != VotingOpen {
		return errors.New("التصويت غير متاح حالياً")
	}
	// A membership that has been withdrawn carries no vote. Checked here at the
	// point of writing, not only in the handler, so no route can bypass it.
	var status string
	if err := s.DB.QueryRow(`SELECT status FROM users WHERE id = ?`, userID).Scan(&status); err != nil {
		return errors.New("العضوية غير موجودة")
	}
	if status != MemberActive {
		return errors.New("لا يمكنك التصويت. يرجى مراجعة المأتم")
	}
	if s.HasVoted(userID) {
		return errors.New("لقد قمت بالتصويت مسبقاً")
	}

	// Validate the whole ballot before writing anything.
	type check struct {
		roleID  int64
		allowed int
		valid   map[int64]bool
	}
	var checks []check
	if s.VotingMode() == "single" {
		ps, err := s.Participants(0, false)
		if err != nil {
			return err
		}
		valid := map[int64]bool{}
		for _, p := range ps {
			valid[p.ID] = true
		}
		checks = append(checks, check{0, s.SingleSelections(), valid})
	} else {
		roles, err := s.Roles()
		if err != nil {
			return err
		}
		for _, r := range roles {
			ps, err := s.Participants(r.ID, false)
			if err != nil {
				return err
			}
			valid := map[int64]bool{}
			for _, p := range ps {
				valid[p.ID] = true
			}
			checks = append(checks, check{r.ID, r.SelectionsAllowed, valid})
		}
	}

	for _, c := range checks {
		picks := selections[c.roleID]
		if len(picks) == 0 {
			return errors.New("يجب اختيار مرشح في كل صفحة قبل إرسال التصويت")
		}
		if len(picks) > c.allowed {
			return fmt.Errorf("لا يمكن اختيار أكثر من %d مرشح", c.allowed)
		}
		seen := map[int64]bool{}
		for _, id := range picks {
			if !c.valid[id] {
				return errors.New("أحد الاختيارات غير صالح")
			}
			if seen[id] {
				return errors.New("لا يمكن اختيار المرشح نفسه مرتين")
			}
			seen[id] = true
		}
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO ballots(user_id, created_at) VALUES (?,?)`, userID, Now())
	if err != nil {
		return errors.New("لقد قمت بالتصويت مسبقاً")
	}
	ballotID, _ := res.LastInsertId()
	for _, c := range checks {
		var roleArg any
		if c.roleID > 0 {
			roleArg = c.roleID
		}
		for _, pid := range selections[c.roleID] {
			if _, err := tx.Exec(
				`INSERT INTO votes(ballot_id, user_id, role_id, participant_id, created_at)
				 VALUES (?,?,?,?,?)`, ballotID, userID, roleArg, pid, Now()); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Turnout reports how many eligible members have voted. Only the elections
// admin sees this, alongside the tallies.
func (s *Store) Turnout() (voted int, eligible int, err error) {
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM ballots`).Scan(&voted); err != nil {
		return
	}
	// Only active members are entitled to vote, so they alone form the
	// denominator for turnout.
	err = s.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE status = ?`, MemberActive).Scan(&eligible)
	return
}

// ResetVotes clears all ballots so a misconfigured round can be re-run.
func (s *Store) ResetVotes(actor string) error {
	if s.VotingStatus() == VotingOpen {
		return errors.New("أغلق التصويت أولاً قبل مسح النتائج")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM votes`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM ballots`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.Audit(actor, "reset_votes", "مسح جميع الأصوات")
	return nil
}
