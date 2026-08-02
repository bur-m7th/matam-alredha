package store

import (
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// Candidacy states.
//
// There is deliberately no rejection. A candidacy is either accepted, or sent
// back to the member with a note so they can correct it and submit again. That
// mirrors how the committee actually works: nobody is turned away outright.
const (
	CandidacySubmitted = "submitted"
	CandidacyAccepted  = "accepted"
	CandidacyReturned  = "returned"
)

// MinCandidateAge is the age a member must have reached to stand.
const MinCandidateAge = 21

// Gate states for the candidacy window.
const (
	GateClosed = "closed"
	GateOpen   = "open"
)

// Candidacy is one member's request to stand in the election.
type Candidacy struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	Name      string `json:"name"`
	RoleID    int64  `json:"role_id"`
	RoleName  string `json:"role_name"`
	Photo     string `json:"photo"`
	Thumb     string `json:"thumb"`
	Status    string `json:"status"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	DecidedBy string `json:"decided_by"`
}

func CandidacyStatusLabel(s string) string {
	switch s {
	case CandidacyAccepted:
		return "مقبول"
	case CandidacyReturned:
		return "معاد للتعديل"
	default:
		return "قيد المراجعة"
	}
}

// CandidacyGate reports whether members may submit or amend a candidacy.
func (s *Store) CandidacyGate() string {
	if s.Setting("candidacy_gate", GateClosed) == GateOpen {
		return GateOpen
	}
	return GateClosed
}

// SetCandidacyGate opens or closes the candidacy window.
//
// The window cannot be opened before roles exist, because a member has to pick
// one. It cannot be reopened once voting has begun, since the ballot would then
// change underneath people who had already voted.
func (s *Store) SetCandidacyGate(gate, actor string) error {
	if gate != GateOpen && gate != GateClosed {
		return errors.New("حالة غير معروفة")
	}
	if gate == GateOpen {
		roles, err := s.Roles()
		if err != nil {
			return err
		}
		if len(roles) == 0 {
			return errors.New("يجب إضافة المناصب أولاً قبل فتح باب الترشح")
		}
		if s.VotingStatus() != VotingDraft {
			return errors.New("لا يمكن إعادة فتح باب الترشح بعد بدء التصويت")
		}
	}
	if err := s.SetSetting("candidacy_gate", gate); err != nil {
		return err
	}
	word := "فتح باب الترشح"
	if gate == GateClosed {
		word = "إغلاق باب الترشح"
	}
	s.Audit(actor, "candidacy_gate", word)
	return nil
}

// CandidateAge returns a member's age in whole years.
func CandidateAge(dob string) int {
	t, err := time.Parse("2006-01-02", dob)
	if err != nil {
		return 0
	}
	now := time.Now().In(Bahrain)
	years := now.Year() - t.Year()
	if now.YearDay() < t.YearDay() {
		years--
	}
	return years
}

// EligibleToStand reports whether a member may submit a candidacy, and why not
// when they may not.
func (s *Store) EligibleToStand(u User) (bool, string) {
	if u.Status != MemberActive {
		return false, "يرجى مراجعة المأتم"
	}
	age := CandidateAge(u.DOB)
	if age < MinCandidateAge {
		return false, "يشترط أن يكون عمر المترشح " + strconv.Itoa(MinCandidateAge) + " سنة فأكثر"
	}
	return true, ""
}

const candidacyCols = `c.id, c.user_id, u.name, c.role_id,
	COALESCE(r.name, ''), c.photo, c.thumb, c.status, c.note,
	c.created_at, c.updated_at, c.decided_by`

func scanCandidacy(sc interface{ Scan(...any) error }) (Candidacy, error) {
	var c Candidacy
	err := sc.Scan(&c.ID, &c.UserID, &c.Name, &c.RoleID, &c.RoleName,
		&c.Photo, &c.Thumb, &c.Status, &c.Note,
		&c.CreatedAt, &c.UpdatedAt, &c.DecidedBy)
	return c, err
}

// CandidacyForUser returns a member's own candidacy, if they have one.
func (s *Store) CandidacyForUser(userID int64) (Candidacy, bool, error) {
	c, err := scanCandidacy(s.DB.QueryRow(`SELECT `+candidacyCols+`
		FROM candidacies c
		JOIN users u ON u.id = c.user_id
		LEFT JOIN roles r ON r.id = c.role_id
		WHERE c.user_id = ?`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Candidacy{}, false, nil
	}
	if err != nil {
		return Candidacy{}, false, err
	}
	return c, true, nil
}

// Candidacies lists every submission, newest first, optionally filtered.
func (s *Store) Candidacies(status string) ([]Candidacy, error) {
	q := `SELECT ` + candidacyCols + `
		FROM candidacies c
		JOIN users u ON u.id = c.user_id
		LEFT JOIN roles r ON r.id = c.role_id`
	args := []any{}
	if status != "" && status != "all" {
		q += ` WHERE c.status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY c.updated_at DESC, c.id DESC`

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Candidacy{}
	for rows.Next() {
		c, err := scanCandidacy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Candidacy(id int64) (Candidacy, error) {
	return scanCandidacy(s.DB.QueryRow(`SELECT `+candidacyCols+`
		FROM candidacies c
		JOIN users u ON u.id = c.user_id
		LEFT JOIN roles r ON r.id = c.role_id
		WHERE c.id = ?`, id))
}

// SubmitCandidacy creates or resubmits a member's application. Resubmitting
// after a return clears the note and puts it back in the queue.
func (s *Store) SubmitCandidacy(u User, roleID int64, photo, thumb string) error {
	if s.CandidacyGate() != GateOpen {
		return errors.New("باب الترشح مغلق حالياً")
	}
	if ok, why := s.EligibleToStand(u); !ok {
		return errors.New(why)
	}
	if _, err := s.Role(roleID); err != nil {
		return errors.New("المنصب المختار غير موجود")
	}

	existing, found, err := s.CandidacyForUser(u.ID)
	if err != nil {
		return err
	}
	now := Now()

	if !found {
		_, err := s.DB.Exec(`INSERT INTO candidacies
			(user_id, role_id, photo, thumb, status, note, created_at, updated_at, decided_by)
			VALUES (?,?,?,?,?,'',?,?,'')`,
			u.ID, roleID, photo, thumb, CandidacySubmitted, now, now)
		return err
	}

	if existing.Status == CandidacyAccepted {
		return errors.New("طلب ترشحك مقبول بالفعل")
	}
	// Keep the previous photo when the member resubmits without a new one.
	if photo == "" {
		photo, thumb = existing.Photo, existing.Thumb
	}
	_, err = s.DB.Exec(`UPDATE candidacies
		SET role_id = ?, photo = ?, thumb = ?, status = ?, note = '', updated_at = ?
		WHERE id = ?`,
		roleID, photo, thumb, CandidacySubmitted, now, existing.ID)
	return err
}

// ChangeCandidacyRole moves a candidacy to a different post.
//
// byMember is true when the member is changing their own; they may only do so
// while the candidacy window is open. The elections admin may change it at any
// time, which is how a post is corrected after the window has closed.
func (s *Store) ChangeCandidacyRole(id, roleID int64, byMember bool, actor string) error {
	if byMember && s.CandidacyGate() != GateOpen {
		return errors.New("لا يمكن تغيير المنصب بعد إغلاق باب الترشح")
	}
	c, err := s.Candidacy(id)
	if err != nil {
		return errors.New("طلب الترشح غير موجود")
	}
	if _, err := s.Role(roleID); err != nil {
		return errors.New("المنصب المختار غير موجود")
	}
	if byMember && c.Status == CandidacyAccepted {
		return errors.New("لا يمكن تغيير المنصب بعد قبول الترشح. يرجى مراجعة لجنة الانتخابات")
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE candidacies SET role_id = ?, updated_at = ? WHERE id = ?`,
		roleID, Now(), id); err != nil {
		return err
	}
	// An accepted candidacy already has a ballot entry, which has to follow.
	if c.Status == CandidacyAccepted {
		if _, err := tx.Exec(`UPDATE participants SET role_id = ? WHERE candidacy_id = ?`,
			roleID, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.Audit(actor, "candidacy_role", "تغيير منصب المترشح: "+c.Name)
	return nil
}

// AcceptCandidacy admits the member to the ballot. The participant row is what
// the voting and results machinery already works from, so accepting simply
// creates or refreshes it.
func (s *Store) AcceptCandidacy(id int64, actor string) error {
	c, err := s.Candidacy(id)
	if err != nil {
		return errors.New("طلب الترشح غير موجود")
	}
	if c.Status == CandidacyAccepted {
		return errors.New("الطلب مقبول بالفعل")
	}
	if c.RoleID == 0 {
		return errors.New("يجب تحديد منصب المترشح أولاً")
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := Now()
	if _, err := tx.Exec(`UPDATE candidacies
		SET status = ?, note = '', updated_at = ?, decided_by = ? WHERE id = ?`,
		CandidacyAccepted, now, actor, id); err != nil {
		return err
	}

	var participantID int64
	err = tx.QueryRow(`SELECT id FROM participants WHERE candidacy_id = ?`, id).Scan(&participantID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.Exec(`INSERT INTO participants
			(role_id, candidacy_id, name, description, photo, thumb, sort_order, created_at)
			VALUES (?,?,?,'',?,?,0,?)`,
			c.RoleID, id, c.Name, c.Photo, c.Thumb, now); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if _, err := tx.Exec(`UPDATE participants
			SET role_id = ?, name = ?, photo = ?, thumb = ? WHERE id = ?`,
			c.RoleID, c.Name, c.Photo, c.Thumb, participantID); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	s.Audit(actor, "candidacy_accept", "قبول ترشح: "+c.Name)
	return nil
}

// ReturnCandidacy sends the application back for amendment with a note. The
// member can then edit and submit again. Any ballot entry is withdrawn.
func (s *Store) ReturnCandidacy(id int64, note, actor string) error {
	note = CleanMultiline(note, 500)
	if note == "" {
		return errors.New("يرجى كتابة ملاحظة توضح المطلوب من المترشح")
	}
	c, err := s.Candidacy(id)
	if err != nil {
		return errors.New("طلب الترشح غير موجود")
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE candidacies
		SET status = ?, note = ?, updated_at = ?, decided_by = ? WHERE id = ?`,
		CandidacyReturned, note, Now(), actor, id); err != nil {
		return err
	}
	// Remove them from the ballot along with any votes already recorded, so a
	// withdrawn candidate cannot keep a tally.
	if _, err := tx.Exec(
		`DELETE FROM votes WHERE participant_id IN (SELECT id FROM participants WHERE candidacy_id = ?)`,
		id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM participants WHERE candidacy_id = ?`, id); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	s.Audit(actor, "candidacy_return", "إعادة طلب ترشح للتعديل: "+c.Name)
	return nil
}

// CandidacyCounts drives the dashboard summary.
func (s *Store) CandidacyCounts() (submitted, accepted, returned int) {
	rows, err := s.DB.Query(`SELECT status, COUNT(*) FROM candidacies GROUP BY status`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if rows.Scan(&st, &n) != nil {
			continue
		}
		switch st {
		case CandidacySubmitted:
			submitted = n
		case CandidacyAccepted:
			accepted = n
		case CandidacyReturned:
			returned = n
		}
	}
	return
}

// ---------------------------------------------------------------- voter view

// DisplayFields records which candidate details voters are shown. The name is
// always shown; a ballot without names would be unusable.
type DisplayFields struct {
	Photo bool `json:"photo"`
	Role  bool `json:"role"`
	Order bool `json:"order"`
}

func (s *Store) DisplayFieldSettings() DisplayFields {
	return DisplayFields{
		Photo: s.SettingBool("show_candidate_photo", true),
		Role:  s.SettingBool("show_candidate_role", true),
		Order: s.SettingBool("show_candidate_order", false),
	}
}

func (s *Store) SetDisplayFields(d DisplayFields, actor string) error {
	set := func(k string, v bool) error {
		val := "0"
		if v {
			val = "1"
		}
		return s.SetSetting(k, val)
	}
	if err := set("show_candidate_photo", d.Photo); err != nil {
		return err
	}
	if err := set("show_candidate_role", d.Role); err != nil {
		return err
	}
	if err := set("show_candidate_order", d.Order); err != nil {
		return err
	}
	s.Audit(actor, "display_fields", "تحديث بيانات المرشحين الظاهرة للناخبين")
	return nil
}
