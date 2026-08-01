package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrDuplicate is returned when a personal number or phone is already in use.
type ErrDuplicate struct{ Field, Message string }

func (e ErrDuplicate) Error() string { return e.Message }

// CheckUnique enforces the rule that a personal number and a phone number may
// each appear once, across both approved members and pending requests.
// excludeApp lets an application be re-checked without matching itself.
func (s *Store) CheckUnique(cpr, phone string, excludeApp int64) error {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE cpr = ?`, cpr).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrDuplicate{"cpr", "الرقم الشخصي مسجل مسبقاً في الجمعية العمومية"}
	}
	err = s.DB.QueryRow(
		`SELECT COUNT(*) FROM applications WHERE cpr = ? AND status = 'pending' AND id <> ?`,
		cpr, excludeApp).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrDuplicate{"cpr", "يوجد طلب قيد المراجعة بنفس الرقم الشخصي"}
	}
	err = s.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE phone = ?`, phone).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrDuplicate{"phone", "رقم التواصل مسجل مسبقاً في الجمعية العمومية"}
	}
	err = s.DB.QueryRow(
		`SELECT COUNT(*) FROM applications WHERE phone = ? AND status = 'pending' AND id <> ?`,
		phone, excludeApp).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrDuplicate{"phone", "يوجد طلب قيد المراجعة بنفس رقم التواصل"}
	}
	return nil
}

func (s *Store) CreateApplication(p Person) (int64, error) {
	if err := s.CheckUnique(p.CPR, p.Phone, 0); err != nil {
		return 0, err
	}
	res, err := s.DB.Exec(`
		INSERT INTO applications
		 (name, cpr, dob, phone, email, house, road, block,
		  volunteer, volunteer_field, affiliated, affiliation, extra, status, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,'pending',?)`,
		p.Name, p.CPR, p.DOB, p.Phone, p.Email, p.House, p.Road, p.Block,
		boolInt(p.Volunteer), p.VolunteerField, boolInt(p.Affiliated), p.Affiliation,
		encodeExtra(p.Extra), Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const appCols = `id, name, cpr, dob, phone, email, house, road, block,
	volunteer, volunteer_field, affiliated, affiliation, extra,
	status, reject_reason, created_at, decided_at, decided_by, meeting_no`

func scanApplication(sc interface{ Scan(...any) error }) (Application, error) {
	var a Application
	var vol, aff int
	var extra string
	err := sc.Scan(&a.ID, &a.Name, &a.CPR, &a.DOB, &a.Phone, &a.Email, &a.House, &a.Road, &a.Block,
		&vol, &a.VolunteerField, &aff, &a.Affiliation, &extra,
		&a.Status, &a.RejectReason, &a.CreatedAt, &a.DecidedAt, &a.DecidedBy, &a.MeetingNo)
	if err != nil {
		return a, err
	}
	a.Volunteer, a.Affiliated = vol == 1, aff == 1
	a.Extra = decodeExtra(extra)
	return a, nil
}

// Applications lists requests filtered by status ("" means all) and free-text search.
func (s *Store) Applications(status, search string) ([]Application, error) {
	q := `SELECT ` + appCols + ` FROM applications WHERE 1=1`
	args := []any{}
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	if search = strings.TrimSpace(search); search != "" {
		q += ` AND (name LIKE ? OR cpr LIKE ? OR phone LIKE ?)`
		like := "%" + search + "%"
		args = append(args, like, like, like)
	}
	q += ` ORDER BY CASE status WHEN 'pending' THEN 0 ELSE 1 END, id DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Application{}
	for rows.Next() {
		a, err := scanApplication(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Application(id int64) (Application, error) {
	row := s.DB.QueryRow(`SELECT `+appCols+` FROM applications WHERE id = ?`, id)
	return scanApplication(row)
}

// ApproveApplication promotes a pending request into a member record. This is
// the only path that creates a login, and it is what the export reflects.
func (s *Store) ApproveApplication(id int64, actor, meetingNo string) (User, error) {
	a, err := s.Application(id)
	if err != nil {
		return User{}, err
	}
	if a.Status != "pending" {
		return User{}, errors.New("تمت معالجة هذا الطلب مسبقاً")
	}
	if err := s.CheckUnique(a.CPR, a.Phone, a.ID); err != nil {
		return User{}, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
		INSERT INTO users
		 (application_id, name, cpr, dob, phone, email, house, road, block,
		  volunteer, volunteer_field, affiliated, affiliation, extra, meeting_no, source, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'form',?)`,
		a.ID, a.Name, a.CPR, a.DOB, a.Phone, a.Email, a.House, a.Road, a.Block,
		boolInt(a.Volunteer), a.VolunteerField, boolInt(a.Affiliated), a.Affiliation,
		encodeExtra(a.Extra), meetingNo, Now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, ErrDuplicate{"cpr", "الرقم الشخصي مسجل مسبقاً"}
		}
		return User{}, err
	}
	uid, _ := res.LastInsertId()
	if _, err := tx.Exec(
		`UPDATE applications SET status='approved', decided_at=?, decided_by=?, reject_reason='', meeting_no=? WHERE id=?`,
		Now(), actor, meetingNo, id); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	s.Audit(actor, "approve_application", fmt.Sprintf("طلب %d - %s", id, a.Name))
	return s.User(uid)
}

func (s *Store) RejectApplication(id int64, reason, actor string) error {
	a, err := s.Application(id)
	if err != nil {
		return err
	}
	if a.Status == "approved" {
		return errors.New("لا يمكن رفض طلب معتمد. احذف العضو بدلاً من ذلك")
	}
	_, err = s.DB.Exec(
		`UPDATE applications SET status='rejected', reject_reason=?, decided_at=?, decided_by=? WHERE id=?`,
		CleanText(reason, 300), Now(), actor, id)
	if err == nil {
		s.Audit(actor, "reject_application", fmt.Sprintf("طلب %d - %s", id, a.Name))
	}
	return err
}

func (s *Store) DeleteApplication(id int64, actor string) error {
	a, err := s.Application(id)
	if err != nil {
		return err
	}
	if a.Status == "approved" {
		return errors.New("لا يمكن حذف طلب معتمد. احذف العضو بدلاً من ذلك")
	}
	_, err = s.DB.Exec(`DELETE FROM applications WHERE id=?`, id)
	if err == nil {
		s.Audit(actor, "delete_application", fmt.Sprintf("طلب %d - %s", id, a.Name))
	}
	return err
}

// ---------------------------------------------------------------- members

const userCols = `u.id, u.application_id, u.name, u.cpr, u.dob, u.phone, u.email,
	u.house, u.road, u.block, u.volunteer, u.volunteer_field, u.affiliated,
	u.affiliation, u.extra, u.meeting_no, u.source, u.created_at,
	EXISTS(SELECT 1 FROM ballots b WHERE b.user_id = u.id)`

func scanUser(sc interface{ Scan(...any) error }) (User, error) {
	var u User
	var appID sql.NullInt64
	var vol, aff, voted int
	var extra string
	err := sc.Scan(&u.ID, &appID, &u.Name, &u.CPR, &u.DOB, &u.Phone, &u.Email,
		&u.House, &u.Road, &u.Block, &vol, &u.VolunteerField, &aff,
		&u.Affiliation, &extra, &u.MeetingNo, &u.Source, &u.CreatedAt, &voted)
	if err != nil {
		return u, err
	}
	u.ApplicationID = appID.Int64
	u.Volunteer, u.Affiliated, u.HasVoted = vol == 1, aff == 1, voted == 1
	u.Extra = decodeExtra(extra)
	return u, nil
}

func (s *Store) Users(search string) ([]User, error) {
	q := `SELECT ` + userCols + ` FROM users u WHERE 1=1`
	args := []any{}
	if search = strings.TrimSpace(search); search != "" {
		q += ` AND (u.name LIKE ? OR u.cpr LIKE ? OR u.phone LIKE ?)`
		like := "%" + search + "%"
		args = append(args, like, like, like)
	}
	q += ` ORDER BY u.id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) User(id int64) (User, error) {
	return scanUser(s.DB.QueryRow(`SELECT `+userCols+` FROM users u WHERE u.id = ?`, id))
}

// FindUserByCredentials authenticates a member. Identity is the triple of phone,
// date of birth and personal number; there is no password for members.
func (s *Store) FindUserByCredentials(phone, dob, cpr string) (User, error) {
	return scanUser(s.DB.QueryRow(
		`SELECT `+userCols+` FROM users u WHERE u.phone = ? AND u.dob = ? AND u.cpr = ?`,
		phone, dob, cpr))
}

// PendingApplicationFor tells a failed login whether a request is already waiting.
func (s *Store) PendingApplicationStatus(cpr, phone string) string {
	var status string
	err := s.DB.QueryRow(
		`SELECT status FROM applications WHERE cpr = ? OR phone = ? ORDER BY id DESC LIMIT 1`,
		cpr, phone).Scan(&status)
	if err != nil {
		return ""
	}
	return status
}

// DeleteUser removes a member everywhere: the member record, their ballot and
// votes, and the originating request. The export is rebuilt from this table,
// so the row disappears from the spreadsheet too.
func (s *Store) DeleteUser(id int64, actor string) error {
	u, err := s.User(id)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM votes WHERE user_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM ballots WHERE user_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE subject_kind='user' AND subject_id = ?`, id); err != nil {
		return err
	}
	if u.ApplicationID > 0 {
		if _, err := tx.Exec(`DELETE FROM applications WHERE id = ?`, u.ApplicationID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.Audit(actor, "delete_user", fmt.Sprintf("عضو %d - %s (%s)", id, u.Name, u.CPR))
	return nil
}

// UpdateUser lets the membership admin correct a member's details.
func (s *Store) UpdateUser(id int64, p Person, meetingNo, actor string) error {
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE cpr=? AND id<>?`, p.CPR, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrDuplicate{"cpr", "الرقم الشخصي مستخدم من قبل عضو آخر"}
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE phone=? AND id<>?`, p.Phone, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrDuplicate{"phone", "رقم التواصل مستخدم من قبل عضو آخر"}
	}
	_, err := s.DB.Exec(`
		UPDATE users SET name=?, cpr=?, dob=?, phone=?, email=?, house=?, road=?, block=?,
		 volunteer=?, volunteer_field=?, affiliated=?, affiliation=?, extra=?, meeting_no=? WHERE id=?`,
		p.Name, p.CPR, p.DOB, p.Phone, p.Email, p.House, p.Road, p.Block,
		boolInt(p.Volunteer), p.VolunteerField, boolInt(p.Affiliated), p.Affiliation,
		encodeExtra(p.Extra), meetingNo, id)
	if err == nil {
		s.Audit(actor, "update_user", fmt.Sprintf("عضو %d - %s", id, p.Name))
	}
	return err
}

// Counts feeds the membership dashboard summary.
func (s *Store) Counts() (map[string]int, error) {
	out := map[string]int{}
	rows, err := s.DB.Query(`SELECT status, COUNT(*) FROM applications GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	var members int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&members); err != nil {
		return nil, err
	}
	out["members"] = members
	return out, nil
}
