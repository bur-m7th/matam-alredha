package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"matam-alredha/internal/auth"
)

const (
	KindMembership = "membership"
	KindElections  = "elections"
)

// EnsureAdmin creates an operator account on first boot if it does not exist.
func (s *Store) EnsureAdmin(username, password, displayName, kind string) (created bool, err error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return false, errors.New("اسم المستخدم مطلوب")
	}
	var n int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM admins WHERE kind = ?`, kind).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return false, err
	}
	_, err = s.DB.Exec(
		`INSERT INTO admins(username, display_name, password_hash, kind, created_at, updated_at)
		 VALUES (?,?,?,?,?,?)`,
		username, displayName, hash, kind, Now(), Now())
	if err != nil {
		return false, err
	}
	return true, nil
}

// ForceResetPassword overwrites an account's password without knowing the old
// one. It exists for lockout recovery from the server console and is never
// reachable over HTTP. All sessions for the account are dropped.
func (s *Store) ForceResetPassword(kind, username, newPassword string) error {
	if err := auth.ValidatePasswordStrength(newPassword); err != nil {
		return err
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	res, err := s.DB.Exec(
		`UPDATE admins SET password_hash = ?, username = ? WHERE kind = ?`,
		hash, username, kind)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("لا يوجد حساب بهذا النوع")
	}
	var id int64
	if err := s.DB.QueryRow(`SELECT id FROM admins WHERE kind = ?`, kind).Scan(&id); err == nil {
		_, _ = s.DB.Exec(`DELETE FROM sessions WHERE subject_kind = ?`, "admin:"+kind)
		s.Audit("system", "admin_password_reset", "تمت إعادة تعيين كلمة المرور من الخادم")
		_ = id
	}
	return nil
}

func (s *Store) AdminByUsername(username, kind string) (Admin, string, error) {
	var a Admin
	var hash string
	err := s.DB.QueryRow(
		`SELECT id, username, display_name, kind, created_at, password_hash
		 FROM admins WHERE username = ? AND kind = ?`,
		strings.ToLower(strings.TrimSpace(username)), kind).
		Scan(&a.ID, &a.Username, &a.DisplayName, &a.Kind, &a.CreatedAt, &hash)
	return a, hash, err
}

func (s *Store) Admin(id int64) (Admin, error) {
	var a Admin
	err := s.DB.QueryRow(
		`SELECT id, username, display_name, kind, created_at FROM admins WHERE id = ?`, id).
		Scan(&a.ID, &a.Username, &a.DisplayName, &a.Kind, &a.CreatedAt)
	return a, err
}

// ChangeAdminPassword requires the current password, as specified.
func (s *Store) ChangeAdminPassword(id int64, oldPassword, newPassword string) error {
	var hash string
	if err := s.DB.QueryRow(`SELECT password_hash FROM admins WHERE id = ?`, id).Scan(&hash); err != nil {
		return err
	}
	if !auth.VerifyPassword(hash, oldPassword) {
		return ErrWrongPassword
	}
	if oldPassword == newPassword {
		return errors.New("كلمة المرور الجديدة مطابقة للحالية")
	}
	if err := auth.ValidatePasswordStrength(newPassword); err != nil {
		return err
	}
	newHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	if _, err := s.DB.Exec(
		`UPDATE admins SET password_hash = ?, updated_at = ? WHERE id = ?`,
		newHash, Now(), id); err != nil {
		return err
	}
	// Invalidate every other session belonging to this operator.
	_, _ = s.DB.Exec(`DELETE FROM sessions WHERE subject_kind LIKE 'admin:%' AND subject_id = ?`, id)
	return nil
}

// ---------------------------------------------------------------- sessions

const sessionTTL = 12 * time.Hour

// CreateSession issues an opaque token. subjectKind is "user",
// "admin:membership" or "admin:elections" — the kinds never interchange.
func (s *Store) CreateSession(subjectKind string, subjectID int64) (string, time.Time, error) {
	token, err := auth.NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().In(Bahrain).Add(sessionTTL)
	_, err = s.DB.Exec(
		`INSERT INTO sessions(token, subject_kind, subject_id, created_at, expires_at) VALUES (?,?,?,?,?)`,
		token, subjectKind, subjectID, Now(), expires.Format(time.RFC3339))
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

func (s *Store) LookupSession(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	var sess Session
	var expires string
	err := s.DB.QueryRow(
		`SELECT token, subject_kind, subject_id, expires_at FROM sessions WHERE token = ?`, token).
		Scan(&sess.Token, &sess.SubjectKind, &sess.SubjectID, &expires)
	if err != nil {
		return Session{}, false
	}
	exp, ok := ParseTime(expires)
	if !ok || time.Now().In(Bahrain).After(exp) {
		_, _ = s.DB.Exec(`DELETE FROM sessions WHERE token = ?`, token)
		return Session{}, false
	}
	sess.ExpiresAt = exp
	return sess, true
}

func (s *Store) DeleteSession(token string) {
	_, _ = s.DB.Exec(`DELETE FROM sessions WHERE token = ?`, token)
}

// PurgeExpiredSessions is called periodically by the server.
func (s *Store) PurgeExpiredSessions() {
	_, _ = s.DB.Exec(`DELETE FROM sessions WHERE expires_at < ?`, Now())
}

func (s *Store) AdminExists(kind string) bool {
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM admins WHERE kind = ?`, kind).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

var ErrNoAdmin = sql.ErrNoRows

// ErrWrongPassword lets the handler blame the right input box.
var ErrWrongPassword = errors.New("كلمة المرور الحالية غير صحيحة")
