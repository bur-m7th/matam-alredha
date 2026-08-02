package store

import (
	"database/sql"
	"errors"
)

// Membership states a member record can be in.
//
// A suspended or cancelled member keeps their row in the register and in the
// spreadsheet; only a deletion removes them. The distinction matters because
// the ma'tam needs a record of who was withdrawn and when.
const (
	MemberActive    = "active"
	MemberSuspended = "suspended"
	MemberCancelled = "cancelled"
)

// Request states, extending the original pending/approved/rejected set.
const (
	AppPending   = "pending"
	AppApproved  = "approved"
	AppRejected  = "rejected"
	AppSuspended = "suspended"
	AppCancelled = "cancelled"
)

// MemberStatusLabel is the Arabic wording used in the dashboard, the
// spreadsheet and the printed form.
func MemberStatusLabel(status string) string {
	switch status {
	case MemberSuspended:
		return "تم التحفّظ"
	case MemberCancelled:
		return "عضوية ملغاة"
	default:
		return "عضوية نشطة"
	}
}

func AppStatusLabel(status string) string {
	switch status {
	case AppApproved:
		return "معتمد"
	case AppRejected:
		return "مرفوض"
	case AppSuspended:
		return "تم التحفّظ"
	case AppCancelled:
		return "ملغى"
	default:
		return "قيد المراجعة"
	}
}

var errBadStatus = errors.New("حالة غير معروفة")

func validMemberStatus(status string) bool {
	return status == MemberActive || status == MemberSuspended || status == MemberCancelled
}

// SetUserStatus withdraws or restores a membership. Suspending or cancelling
// also drops the member's open sessions so the change takes effect at once
// rather than whenever their current session happens to expire.
func (s *Store) SetUserStatus(id int64, status, note, actor string) error {
	if !validMemberStatus(status) {
		return errBadStatus
	}
	u, err := s.User(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("العضو غير موجود")
		}
		return err
	}
	if u.Status == status {
		return errors.New("العضو بهذه الحالة أصلاً")
	}

	stamp := ""
	if status != MemberActive {
		stamp = Now()
	}
	if _, err := s.DB.Exec(
		`UPDATE users SET status = ?, status_note = ?, status_at = ? WHERE id = ?`,
		status, CleanMultiline(note, 400), stamp, id); err != nil {
		return err
	}

	// A withdrawn member must lose access immediately.
	if status != MemberActive {
		if _, err := s.DB.Exec(
			`DELETE FROM sessions WHERE subject_kind = 'user' AND subject_id = ?`, id); err != nil {
			return err
		}
	}

	s.Audit(actor, "member_status", MemberStatusLabel(status)+": "+u.Name)
	return nil
}

// SetApplicationStatus puts a request on hold or cancels it. A request that is
// already approved cannot be changed here: the member record exists by then,
// so the membership itself has to be suspended instead.
func (s *Store) SetApplicationStatus(id int64, status, note, actor string) error {
	if status != AppPending && status != AppSuspended && status != AppCancelled {
		return errBadStatus
	}
	a, err := s.Application(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("الطلب غير موجود")
		}
		return err
	}
	if a.Status == AppApproved {
		return errors.New("الطلب معتمد بالفعل. لتعليق العضوية استخدم قائمة الأعضاء")
	}
	if a.Status == status {
		return errors.New("الطلب بهذه الحالة أصلاً")
	}

	decidedAt := ""
	if status != AppPending {
		decidedAt = Now()
	}
	if _, err := s.DB.Exec(
		`UPDATE applications SET status = ?, reject_reason = ?, decided_at = ?, decided_by = ? WHERE id = ?`,
		status, CleanMultiline(note, 400), decidedAt, actor, id); err != nil {
		return err
	}
	s.Audit(actor, "application_status", AppStatusLabel(status)+": "+a.Name)
	return nil
}

// ActiveMemberCount is the electorate: suspended and cancelled members are not
// entitled to vote and must not inflate the turnout denominator.
func (s *Store) ActiveMemberCount() (int, error) {
	var n int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM users WHERE status = ?`, MemberActive).Scan(&n)
	return n, err
}
