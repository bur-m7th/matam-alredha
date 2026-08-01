package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"matam-alredha/internal/auth"
	"matam-alredha/internal/docxt"
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

	m.HandleFunc("GET "+b+"/template", s.requireAdmin(store.KindMembership, s.handleGetTemplate))
	m.HandleFunc("POST "+b+"/template", s.requireAdmin(store.KindMembership, s.handleUploadTemplate))
	m.HandleFunc("PUT "+b+"/signatories", s.requireAdmin(store.KindMembership, s.handleSignatories))
	m.HandleFunc("DELETE "+b+"/template", s.requireAdmin(store.KindMembership, s.handleDeleteTemplate))
	m.HandleFunc("GET "+b+"/members/{id}/document", s.requireAdmin(store.KindMembership, s.handleMemberDocument))
	m.HandleFunc("GET "+b+"/applications/{id}/document", s.requireAdmin(store.KindMembership, s.handleApplicationDocument))

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
	// The board meeting number is optional; it only appears on the printed form.
	var req struct {
		MeetingNo string `json:"meeting_no"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	u, err := s.st.ApproveApplication(id, a.Username, store.CleanText(req.MeetingNo, 40))
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
	if err := s.st.UpdateUser(id, person, store.CleanText(req.MeetingNo, 40), a.Username); err != nil {
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

// ---------------------------------------------------------------- print template

// templateInfo describes the uploaded Word template for the dashboard.
type templateInfo struct {
	Exists       bool     `json:"exists"`
	Filename     string   `json:"filename"`
	UploadedAt   string   `json:"uploaded_at"`
	Used         []string `json:"used"`      // placeholders found in the document
	Available    []string `json:"available"` // placeholders the system can supply
	Unrecognised []string `json:"unrecognised"`
	Secretary    string   `json:"secretary"`
	Chairman     string   `json:"chairman"`
}

func (s *Server) templatePath() string {
	return filepath.Join(filepath.Dir(s.cfg.ExportPath), "print-template.docx")
}

func (s *Server) handleGetTemplate(w http.ResponseWriter, r *http.Request, a store.Admin) {
	info := templateInfo{Available: s.st.DocumentKeys()}
	info.Secretary = s.st.Setting("doc_secretary", "")
	info.Chairman = s.st.Setting("doc_chairman", "")
	info.Filename = s.st.Setting("print_template_name", "")
	info.UploadedAt = s.st.Setting("print_template_at", "")

	data, err := os.ReadFile(s.templatePath())
	if err != nil {
		ok(w, info)
		return
	}
	info.Exists = true
	used, err := docxt.Placeholders(data)
	if err != nil {
		info.Exists = false
		ok(w, info)
		return
	}
	info.Used = used
	known := map[string]bool{}
	for _, k := range info.Available {
		known[k] = true
	}
	for _, u := range used {
		if !known[u] {
			info.Unrecognised = append(info.Unrecognised, u)
		}
	}
	ok(w, info)
}

func (s *Server) handleUploadTemplate(w http.ResponseWriter, r *http.Request, a store.Admin) {
	if err := r.ParseMultipartForm(docxt.MaxTemplateSize); err != nil {
		fail(w, http.StatusBadRequest, "تعذر قراءة الملف")
		return
	}
	file, header, err := r.FormFile("template")
	if err != nil {
		fail(w, http.StatusBadRequest, "الرجاء اختيار ملف")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, docxt.MaxTemplateSize+1))
	if err != nil {
		fail(w, http.StatusBadRequest, "تعذر قراءة الملف")
		return
	}
	if err := docxt.Validate(data); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	// Write through a temporary file so a failed upload cannot leave a
	// half-written template behind.
	tmp := s.templatePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		fail(w, http.StatusInternalServerError, "تعذر حفظ الملف")
		return
	}
	if err := os.Rename(tmp, s.templatePath()); err != nil {
		os.Remove(tmp)
		fail(w, http.StatusInternalServerError, "تعذر حفظ الملف")
		return
	}

	name := filepath.Base(header.Filename)
	_ = s.st.SetSetting("print_template_name", name)
	_ = s.st.SetSetting("print_template_at", store.Now())
	s.st.Audit(a.Username, "template_upload", "تم رفع نموذج الطباعة: "+name)

	s.handleGetTemplate(w, r, a)
}

func (s *Server) handleSignatories(w http.ResponseWriter, r *http.Request, a store.Admin) {
	var req struct {
		Secretary string `json:"secretary"`
		Chairman  string `json:"chairman"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "بيانات غير صالحة")
		return
	}
	_ = s.st.SetSetting("doc_secretary", store.CleanText(req.Secretary, 80))
	_ = s.st.SetSetting("doc_chairman", store.CleanText(req.Chairman, 80))
	s.st.Audit(a.Username, "signatories", "تم تحديث أسماء المعتمدين")
	ok(w, map[string]string{"message": "تم حفظ الأسماء"})
}

func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request, a store.Admin) {
	if err := os.Remove(s.templatePath()); err != nil && !os.IsNotExist(err) {
		fail(w, http.StatusInternalServerError, "تعذر حذف الملف")
		return
	}
	_ = s.st.SetSetting("print_template_name", "")
	_ = s.st.SetSetting("print_template_at", "")
	s.st.Audit(a.Username, "template_delete", "تم حذف نموذج الطباعة")
	ok(w, map[string]string{"message": "تم حذف نموذج الطباعة"})
}

// renderDocument fills the stored template and sends it as a download.
func (s *Server) renderDocument(w http.ResponseWriter, p store.Person, status, decidedAt string, no int, meetingNo string) {
	data, err := os.ReadFile(s.templatePath())
	if err != nil {
		fail(w, http.StatusNotFound, "لم يتم رفع نموذج الطباعة بعد")
		return
	}
	filled, err := docxt.Fill(data, s.st.DocumentValues(p, status, decidedAt, no, meetingNo))
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر تعبئة النموذج: "+err.Error())
		return
	}

	name := sanitizeFilename(p.Name) + ".docx"
	w.Header().Set("Content-Type",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition",
		"attachment; filename=\"membership-form.docx\"; filename*=UTF-8''"+url.PathEscape(name))
	w.Header().Set("Content-Length", strconv.Itoa(len(filled)))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(filled)
}

// sanitizeFilename keeps Arabic letters but drops anything that would upset a
// Content-Disposition header or a file system.
func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r', '\t':
			return '-'
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, name)
	if name == "" {
		name = "استمارة"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	return name
}

func (s *Server) handleMemberDocument(w http.ResponseWriter, r *http.Request, a store.Admin) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, http.StatusBadRequest, "معرّف غير صالح")
		return
	}
	u, err := s.st.User(id)
	if err != nil {
		fail(w, http.StatusNotFound, "العضو غير موجود")
		return
	}
	// The membership number is the member's permanent id, which for the
	// imported members matches their row in the original register.
	s.renderDocument(w, u.Person, store.PrintStatusApproved, u.CreatedAt, int(u.ID), u.MeetingNo)
}

func (s *Server) handleApplicationDocument(w http.ResponseWriter, r *http.Request, a store.Admin) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, http.StatusBadRequest, "معرّف غير صالح")
		return
	}
	app, err := s.st.Application(id)
	if err != nil {
		fail(w, http.StatusNotFound, "الطلب غير موجود")
		return
	}
	status := store.PrintStatusPending
	switch app.Status {
	case "approved":
		status = store.PrintStatusApproved
	case "rejected":
		status = store.PrintStatusRejected
	}
	s.renderDocument(w, app.Person, status, app.DecidedAt, 0, app.MeetingNo)
}
