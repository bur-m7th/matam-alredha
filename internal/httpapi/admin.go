package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"matam-alredha/internal/auth"
	"matam-alredha/internal/store"
)

func (s *Server) membershipRoutes(m *http.ServeMux) {
	b := "/" + s.cfg.AdminPath + "/api"
	m.HandleFunc("POST "+b+"/login", s.handleAdminLogin(store.KindMembership))
	m.HandleFunc("POST "+b+"/logout", s.handleAdminLogout(store.KindMembership))
	m.HandleFunc("GET "+b+"/session", s.requireAdmin(store.KindMembership, s.handleAdminSession))
	m.HandleFunc("POST "+b+"/password", s.requireAdmin(store.KindMembership, s.handleAdminPassword))

	m.HandleFunc("GET "+b+"/overview", s.requireAdmin(store.KindMembership, s.handleOverview))
	m.HandleFunc("GET "+b+"/applications", s.requireAdmin(store.KindMembership, s.handleListApplications))
	m.HandleFunc("POST "+b+"/applications/{id}/approve", s.requireAdmin(store.KindMembership, s.handleApprove))
	m.HandleFunc("POST "+b+"/applications/{id}/reject", s.requireAdmin(store.KindMembership, s.handleReject))
	m.HandleFunc("DELETE "+b+"/applications/{id}", s.requireAdmin(store.KindMembership, s.handleDeleteApplication))

	m.HandleFunc("GET "+b+"/members", s.requireAdmin(store.KindMembership, s.handleListMembers))
	m.HandleFunc("PUT "+b+"/members/{id}", s.requireAdmin(store.KindMembership, s.handleUpdateMember))
	m.HandleFunc("DELETE "+b+"/members/{id}", s.requireAdmin(store.KindMembership, s.handleDeleteMember))
	m.HandleFunc("GET "+b+"/export", s.requireAdmin(store.KindMembership, s.handleExport))

	m.HandleFunc("GET "+b+"/form", s.requireAdmin(store.KindMembership, s.handleGetForm))
	m.HandleFunc("PUT "+b+"/form/settings", s.requireAdmin(store.KindMembership, s.handleFormSettings))
	m.HandleFunc("POST "+b+"/form/fields", s.requireAdmin(store.KindMembership, s.handleAddField))
	m.HandleFunc("PUT "+b+"/form/fields/{key}", s.requireAdmin(store.KindMembership, s.handleUpdateField))
	m.HandleFunc("DELETE "+b+"/form/fields/{key}", s.requireAdmin(store.KindMembership, s.handleDeleteField))
	m.HandleFunc("POST "+b+"/form/reorder", s.requireAdmin(store.KindMembership, s.handleReorderFields))

	m.HandleFunc("PUT "+b+"/registration", s.requireAdmin(store.KindMembership, s.handleRegistrationControl))
}

// ---------------------------------------------------------------- auth

type adminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleAdminLogin(kind string) http.HandlerFunc {
	cookieName, subject := cookieAdminMember, subjectAdminMem
	if kind == store.KindElections {
		cookieName, subject = cookieAdminElection, subjectAdminElec
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.checkOrigin(w, r) {
			return
		}
		if !s.limiter.allow("adminlogin:"+kind+":"+clientIP(r), 8, 15*time.Minute) {
			fail(w, http.StatusTooManyRequests, "عدد محاولات الدخول تجاوز الحد. الرجاء المحاولة بعد قليل")
			return
		}
		var req adminLoginRequest
		if err := decode(w, r, &req); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		a, hash, err := s.st.AdminByUsername(req.Username, kind)
		if err != nil || !auth.VerifyPassword(hash, req.Password) {
			// Same message either way, so a wrong username is indistinguishable.
			fail(w, http.StatusUnauthorized, "اسم المستخدم أو كلمة المرور غير صحيحة")
			return
		}
		token, expires, err := s.st.CreateSession(subject, a.ID)
		if err != nil {
			fail(w, http.StatusInternalServerError, "تعذر إنشاء الجلسة")
			return
		}
		s.setSessionCookie(w, cookieName, token, expires)
		s.st.Audit(a.Username, "login", kind)
		ok(w, a)
	}
}

func (s *Server) handleAdminLogout(kind string) http.HandlerFunc {
	cookieName := cookieAdminMember
	if kind == store.KindElections {
		cookieName = cookieAdminElection
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.checkOrigin(w, r) {
			return
		}
		if c, err := r.Cookie(cookieName); err == nil {
			s.st.DeleteSession(c.Value)
		}
		s.clearCookie(w, cookieName)
		ok(w, map[string]string{"status": "ok"})
	}
}

func (s *Server) handleAdminSession(w http.ResponseWriter, r *http.Request, a store.Admin) {
	ok(w, a)
}

type passwordRequest struct {
	OldPassword     string `json:"old_password"`
	NewPassword     string `json:"new_password"`
	ConfirmPassword string `json:"confirm_password"`
}

func (s *Server) handleAdminPassword(w http.ResponseWriter, r *http.Request, a store.Admin) {
	s.changePassword(w, r, a)
}

// changePassword is shared by both dashboards; each only ever receives its own
// admin record from the middleware.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, a store.Admin) {
	var req passwordRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.NewPassword != req.ConfirmPassword {
		failField(w, http.StatusBadRequest, "confirm_password", "تأكيد كلمة المرور غير مطابق")
		return
	}
	if err := s.st.ChangeAdminPassword(a.ID, req.OldPassword, req.NewPassword); err != nil {
		// Point the message at the field the operator actually needs to fix.
		field := "new_password"
		if errors.Is(err, store.ErrWrongPassword) {
			field = "old_password"
		}
		failField(w, http.StatusBadRequest, field, err.Error())
		return
	}
	s.st.Audit(a.Username, "change_password", a.Kind)
	ok(w, map[string]string{"message": "تم تغيير كلمة المرور بنجاح. الرجاء تسجيل الدخول مرة أخرى"})
}

// ---------------------------------------------------------------- overview

type overview struct {
	Admin          store.Admin        `json:"admin"`
	Pending        int                `json:"pending"`
	Approved       int                `json:"approved"`
	Rejected       int                `json:"rejected"`
	Members        int                `json:"members"`
	Open           bool               `json:"registration_open"`
	ManualOpen     bool               `json:"manual_open"`
	CloseAt        string             `json:"close_at"`
	CloseAtDisplay string             `json:"close_at_display"`
	ClosedMessage  string             `json:"closed_message"`
	OrgName        string             `json:"org_name"`
	Recent         []store.AuditEntry `json:"recent"`
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, a store.Admin) {
	counts, err := s.st.Counts()
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر تحميل البيانات")
		return
	}
	open, closeAt, hasSchedule := s.registrationOpen()
	recent, _ := s.st.RecentAudit(12)
	o := overview{
		Admin:         a,
		Pending:       counts["pending"],
		Approved:      counts["approved"],
		Rejected:      counts["rejected"],
		Members:       counts["members"],
		Open:          open,
		ManualOpen:    s.st.SettingBool("registration_open", true),
		ClosedMessage: s.st.Setting("registration_closed_message", ""),
		OrgName:       s.st.Setting("org_name", ""),
		Recent:        recent,
	}
	if hasSchedule {
		o.CloseAt = closeAt.Format("2006-01-02T15:04")
		o.CloseAtDisplay = arabicDateTime(closeAt)
	}
	ok(w, o)
}

// ---------------------------------------------------------------- applications

func (s *Server) handleListApplications(w http.ResponseWriter, r *http.Request, a store.Admin) {
	status := r.URL.Query().Get("status")
	switch status {
	case "", "pending", "approved", "rejected":
	default:
		fail(w, http.StatusBadRequest, "حالة غير معروفة")
		return
	}
	apps, err := s.st.Applications(status, r.URL.Query().Get("q"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر تحميل الطلبات")
		return
	}
	ok(w, map[string]any{"applications": apps})
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request, a store.Admin) {
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, http.StatusBadRequest, "معرف غير صالح")
		return
	}
	u, err := s.st.ApproveApplication(id, a.Username)
	if err != nil {
		var dup store.ErrDuplicate
		if asDuplicate(err, &dup) {
			fail(w, http.StatusConflict, dup.Message)
			return
		}
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.syncExport() // keep the workbook in step with the members table
	ok(w, map[string]any{"message": "تم اعتماد العضوية وإضافة العضو إلى الكشف", "user": u})
}

type rejectRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) handleReject(w http.ResponseWriter, r *http.Request, a store.Admin) {
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, http.StatusBadRequest, "معرف غير صالح")
		return
	}
	var req rejectRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.RejectApplication(id, req.Reason, a.Username); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"message": "تم رفض الطلب"})
}

func (s *Server) handleDeleteApplication(w http.ResponseWriter, r *http.Request, a store.Admin) {
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, http.StatusBadRequest, "معرف غير صالح")
		return
	}
	if err := s.st.DeleteApplication(id, a.Username); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"message": "تم حذف الطلب"})
}

// ---------------------------------------------------------------- members

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request, a store.Admin) {
	users, err := s.st.Users(r.URL.Query().Get("q"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر تحميل الأعضاء")
		return
	}
	ok(w, map[string]any{"members": users})
}

func (s *Server) handleUpdateMember(w http.ResponseWriter, r *http.Request, a store.Admin) {
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, http.StatusBadRequest, "معرف غير صالح")
		return
	}
	var req registerRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	fields, err := s.st.Fields(false)
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر تحميل الاستمارة")
		return
	}
	person, fieldKey, msg := buildPerson(fields, req.Values)
	if msg != "" {
		failField(w, http.StatusBadRequest, fieldKey, msg)
		return
	}
	if err := s.st.UpdateUser(id, person, a.Username); err != nil {
		var dup store.ErrDuplicate
		if asDuplicate(err, &dup) {
			failField(w, http.StatusConflict, dup.Field, dup.Message)
			return
		}
		fail(w, http.StatusInternalServerError, "تعذر حفظ التعديلات")
		return
	}
	s.syncExport()
	ok(w, map[string]string{"message": "تم حفظ التعديلات"})
}

func (s *Server) handleDeleteMember(w http.ResponseWriter, r *http.Request, a store.Admin) {
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, http.StatusBadRequest, "معرف غير صالح")
		return
	}
	if err := s.st.DeleteUser(id, a.Username); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.syncExport() // the row disappears from the workbook as well
	ok(w, map[string]string{"message": "تم حذف العضو من قاعدة البيانات ومن كشف الأعضاء"})
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request, a store.Admin) {
	data, err := s.st.ExportBytes()
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر إنشاء الملف")
		return
	}
	name := fmt.Sprintf("اعضاء_الجمعية_العمومية_%s.xlsx", time.Now().In(store.Bahrain).Format("2006-01-02"))
	w.Header().Set("Content-Type",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition",
		"attachment; filename=\"members.xlsx\"; filename*=UTF-8''"+urlEncode(name))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
	s.st.Audit(a.Username, "export", fmt.Sprintf("تصدير %d بايت", len(data)))
}

func urlEncode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

// ---------------------------------------------------------------- form editor

type formPayload struct {
	Title     string            `json:"title"`
	Intro     string            `json:"intro"`
	Terms     string            `json:"terms"`
	AgreeText string            `json:"agree_text"`
	Success   string            `json:"success_message"`
	Fields    []store.FormField `json:"fields"`
}

func (s *Server) handleGetForm(w http.ResponseWriter, r *http.Request, a store.Admin) {
	fields, err := s.st.Fields(false)
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر تحميل الاستمارة")
		return
	}
	ok(w, formPayload{
		Title:     s.st.Setting("form_title", ""),
		Intro:     s.st.Setting("form_intro", ""),
		Terms:     s.st.Setting("form_terms", ""),
		AgreeText: s.st.Setting("form_agree_text", ""),
		Success:   s.st.Setting("form_success_message", ""),
		Fields:    fields,
	})
}

type formSettingsRequest struct {
	Title     string `json:"title"`
	Intro     string `json:"intro"`
	Terms     string `json:"terms"`
	AgreeText string `json:"agree_text"`
	Success   string `json:"success_message"`
}

func (s *Server) handleFormSettings(w http.ResponseWriter, r *http.Request, a store.Admin) {
	var req formSettingsRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	title := store.CleanText(req.Title, 300)
	agree := store.CleanText(req.AgreeText, 500)
	success := store.CleanText(req.Success, 300)
	if title == "" || agree == "" || success == "" {
		fail(w, http.StatusBadRequest, "عنوان الاستمارة ونص الإقرار ورسالة النجاح مطلوبة")
		return
	}
	for k, v := range map[string]string{
		"form_title":           title,
		"form_intro":           store.CleanMultiline(req.Intro, 1000),
		"form_terms":           store.CleanMultiline(req.Terms, 8000),
		"form_agree_text":      agree,
		"form_success_message": success,
	} {
		if err := s.st.SetSetting(k, v); err != nil {
			fail(w, http.StatusInternalServerError, "تعذر حفظ الإعدادات")
			return
		}
	}
	s.st.Audit(a.Username, "update_form_settings", "")
	ok(w, map[string]string{"message": "تم حفظ إعدادات الاستمارة"})
}

func (s *Server) handleAddField(w http.ResponseWriter, r *http.Request, a store.Admin) {
	var f store.FormField
	if err := decode(w, r, &f); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.st.AddField(f)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.Audit(a.Username, "add_field", created.Key)
	ok(w, created)
}

func (s *Server) handleUpdateField(w http.ResponseWriter, r *http.Request, a store.Admin) {
	var f store.FormField
	if err := decode(w, r, &f); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	f.Key = r.PathValue("key")
	if err := s.st.UpdateField(f); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.Audit(a.Username, "update_field", f.Key)
	ok(w, map[string]string{"message": "تم حفظ الحقل"})
}

func (s *Server) handleDeleteField(w http.ResponseWriter, r *http.Request, a store.Admin) {
	key := r.PathValue("key")
	if err := s.st.DeleteField(key); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.Audit(a.Username, "delete_field", key)
	ok(w, map[string]string{"message": "تم حذف الحقل"})
}

type reorderRequest struct {
	Keys []string `json:"keys"`
}

func (s *Server) handleReorderFields(w http.ResponseWriter, r *http.Request, a store.Admin) {
	var req reorderRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.ReorderFields(req.Keys); err != nil {
		fail(w, http.StatusInternalServerError, "تعذر حفظ الترتيب")
		return
	}
	ok(w, map[string]string{"message": "تم حفظ الترتيب"})
}

// ---------------------------------------------------------------- registration

type registrationRequest struct {
	Action        string `json:"action"` // open | close | schedule | clear_schedule
	CloseAt       string `json:"close_at"`
	ClosedMessage string `json:"closed_message"`
}

func (s *Server) handleRegistrationControl(w http.ResponseWriter, r *http.Request, a store.Admin) {
	var req registrationRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if msg := store.CleanMultiline(req.ClosedMessage, 400); msg != "" {
		_ = s.st.SetSetting("registration_closed_message", msg)
	}

	switch req.Action {
	case "open":
		_ = s.st.SetSetting("registration_open", "1")
		_ = s.st.SetSetting("registration_close_at", "")
		s.st.Audit(a.Username, "registration", "فتح التسجيل")
	case "close":
		_ = s.st.SetSetting("registration_open", "0")
		_ = s.st.SetSetting("registration_close_at", "")
		s.st.Audit(a.Username, "registration", "إغلاق التسجيل فوراً")
	case "schedule":
		t, err := time.ParseInLocation("2006-01-02T15:04", strings.TrimSpace(req.CloseAt), store.Bahrain)
		if err != nil {
			failField(w, http.StatusBadRequest, "close_at", "صيغة التاريخ غير صحيحة")
			return
		}
		if t.Before(time.Now().In(store.Bahrain)) {
			failField(w, http.StatusBadRequest, "close_at", "يجب أن يكون موعد الإغلاق في المستقبل")
			return
		}
		_ = s.st.SetSetting("registration_open", "1")
		_ = s.st.SetSetting("registration_close_at", t.Format(time.RFC3339))
		s.st.Audit(a.Username, "registration", "جدولة الإغلاق: "+arabicDateTime(t))
	case "clear_schedule":
		_ = s.st.SetSetting("registration_close_at", "")
		s.st.Audit(a.Username, "registration", "إلغاء موعد الإغلاق")
	default:
		fail(w, http.StatusBadRequest, "إجراء غير معروف")
		return
	}
	ok(w, map[string]string{"message": "تم تحديث حالة التسجيل"})
}
