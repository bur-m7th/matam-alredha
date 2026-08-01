package httpapi

import (
	"net/http"
	"strings"
	"time"

	"matam-alredha/internal/auth"
	"matam-alredha/internal/store"
)

type publicConfig struct {
	OrgName        string `json:"org_name"`
	FormTitle      string `json:"form_title"`
	Open           bool   `json:"registration_open"`
	CloseAt        string `json:"close_at"`
	CloseAtDisplay string `json:"close_at_display"`
	ClosedMessage  string `json:"closed_message"`
	Author         string `json:"footer_author"`
	Instagram      string `json:"footer_instagram"`
}

// registrationOpen resolves the manual switch together with any scheduled
// closing date. A schedule in the past closes the form on its own.
func (s *Server) registrationOpen() (bool, time.Time, bool) {
	manual := s.st.SettingBool("registration_open", true)
	closeAt, hasSchedule := store.ParseTime(s.st.Setting("registration_close_at", ""))
	if !manual {
		return false, closeAt, hasSchedule
	}
	if hasSchedule && time.Now().In(store.Bahrain).After(closeAt) {
		return false, closeAt, hasSchedule
	}
	return true, closeAt, hasSchedule
}

func arabicDateTime(t time.Time) string {
	months := []string{"يناير", "فبراير", "مارس", "أبريل", "مايو", "يونيو",
		"يوليو", "أغسطس", "سبتمبر", "أكتوبر", "نوفمبر", "ديسمبر"}
	return t.Format("15:04") + " - " + t.Format("2") + " " + months[int(t.Month())-1] + " " + t.Format("2006")
}

func (s *Server) handlePublicConfig(w http.ResponseWriter, r *http.Request) {
	open, closeAt, hasSchedule := s.registrationOpen()
	cfg := publicConfig{
		OrgName:       s.st.Setting("org_name", ""),
		FormTitle:     s.st.Setting("form_title", ""),
		Open:          open,
		ClosedMessage: s.st.Setting("registration_closed_message", ""),
		Author:        s.st.Setting("footer_author", ""),
		Instagram:     s.st.Setting("footer_instagram", ""),
	}
	if hasSchedule {
		cfg.CloseAt = closeAt.Format(time.RFC3339)
		cfg.CloseAtDisplay = arabicDateTime(closeAt)
	}
	ok(w, cfg)
}

type publicForm struct {
	Title         string            `json:"title"`
	Intro         string            `json:"intro"`
	Terms         string            `json:"terms"`
	AgreeText     string            `json:"agree_text"`
	SuccessMsg    string            `json:"success_message"`
	ClosedMessage string            `json:"closed_message"`
	Open          bool              `json:"open"`
	Fields        []store.FormField `json:"fields"`
}

func (s *Server) handlePublicForm(w http.ResponseWriter, r *http.Request) {
	fields, err := s.st.Fields(true)
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر تحميل الاستمارة")
		return
	}
	open, _, _ := s.registrationOpen()
	ok(w, publicForm{
		Title:         s.st.Setting("form_title", ""),
		Intro:         s.st.Setting("form_intro", ""),
		Terms:         s.st.Setting("form_terms", ""),
		AgreeText:     s.st.Setting("form_agree_text", ""),
		SuccessMsg:    s.st.Setting("form_success_message", ""),
		ClosedMessage: s.st.Setting("registration_closed_message", ""),
		Open:          open,
		Fields:        fields,
	})
}

// ---------------------------------------------------------------- registration

type registerRequest struct {
	Values map[string]string `json:"values"`
	Agree  bool              `json:"agree"`
	// MeetingNo is only ever sent by the membership dashboard when editing a
	// member; the public form ignores it.
	MeetingNo string `json:"meeting_no"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.checkOrigin(w, r) {
		return
	}
	if !s.limiter.allow("register:"+clientIP(r), s.cfg.RegisterLimit, time.Hour) {
		fail(w, http.StatusTooManyRequests, "تم إرسال عدة طلبات. الرجاء المحاولة بعد قليل")
		return
	}
	if open, _, _ := s.registrationOpen(); !open {
		fail(w, http.StatusForbidden, s.st.Setting("registration_closed_message", "باب التسجيل مغلق حالياً"))
		return
	}

	var req registerRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if !req.Agree {
		failField(w, http.StatusBadRequest, "agree", "يجب الموافقة على شروط العضوية")
		return
	}

	fields, err := s.st.Fields(true)
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر تحميل الاستمارة")
		return
	}

	person, fieldErr, msg := buildPerson(fields, req.Values)
	if msg != "" {
		failField(w, http.StatusBadRequest, fieldErr, msg)
		return
	}

	if _, err := s.st.CreateApplication(person); err != nil {
		var dup store.ErrDuplicate
		if asDuplicate(err, &dup) {
			failField(w, http.StatusConflict, dup.Field, dup.Message)
			return
		}
		fail(w, http.StatusInternalServerError, "تعذر حفظ الطلب. الرجاء المحاولة مرة أخرى")
		return
	}
	ok(w, map[string]string{"message": s.st.Setting("form_success_message", "لقد تم ارسال طلب التقديم بنجاح")})
}

func asDuplicate(err error, dst *store.ErrDuplicate) bool {
	d, isDup := err.(store.ErrDuplicate)
	if isDup {
		*dst = d
	}
	return isDup
}

// buildPerson validates the submitted values against the current form
// definition. Custom fields are collected into Extra.
func buildPerson(fields []store.FormField, values map[string]string) (store.Person, string, string) {
	p := store.Person{Extra: map[string]string{}}
	answers := map[string]string{}
	for _, f := range fields {
		answers[f.Key] = strings.TrimSpace(values[f.Key])
	}

	isYes := func(key string) bool { return answers[key] == "yes" }

	// A conditional field only applies when its controlling answer matches.
	applies := func(f store.FormField) bool {
		if f.DependsOn == "" {
			return true
		}
		return answers[f.DependsOn] == f.DependsVal
	}

	for _, f := range fields {
		v := answers[f.Key]
		if !applies(f) {
			continue
		}
		if f.Required && v == "" && f.Type != "yesno" {
			return p, f.Key, f.Label + " مطلوب"
		}

		switch f.Key {
		case "name":
			name, err := store.ValidateArabicName(v)
			if err != nil {
				return p, f.Key, err.Error()
			}
			p.Name = name
		case "cpr":
			cpr, err := store.ValidateCPR(v)
			if err != nil {
				return p, f.Key, err.Error()
			}
			p.CPR = cpr
		case "dob":
			dob, err := store.ValidateDOB(v)
			if err != nil {
				return p, f.Key, err.Error()
			}
			p.DOB = dob
		case "phone":
			phone, err := store.ValidatePhone(v)
			if err != nil {
				return p, f.Key, err.Error()
			}
			p.Phone = phone
		case "email":
			email, err := store.ValidateEmail(v)
			if err != nil {
				return p, f.Key, err.Error()
			}
			p.Email = email
		// Address parts are numbers in Bahrain. Normalise Arabic-Indic digits so
		// the sheet stays consistent with the 378 imported rows.
		case "house":
			p.House = store.CleanText(store.NormalizeDigits(v), 40)
		case "road":
			p.Road = store.CleanText(store.NormalizeDigits(v), 40)
		case "block":
			p.Block = store.CleanText(store.NormalizeDigits(v), 40)
		case "volunteer", "affiliated":
			if v != "yes" && v != "no" {
				return p, f.Key, "الرجاء الإجابة على: " + f.Label
			}
			if f.Key == "volunteer" {
				p.Volunteer = v == "yes"
			} else {
				p.Affiliated = v == "yes"
			}
		case "volunteer_field":
			if isYes("volunteer") {
				if v == "" {
					return p, f.Key, f.Label + " مطلوب"
				}
				p.VolunteerField = store.CleanText(v, 120)
			}
		case "affiliation":
			if isYes("affiliated") {
				if v == "" {
					return p, f.Key, f.Label + " مطلوب"
				}
				p.Affiliation = store.CleanText(v, 120)
			}
		default: // admin-added field
			if f.Type == "textarea" {
				p.Extra[f.Key] = store.CleanMultiline(v, 600)
			} else {
				p.Extra[f.Key] = store.CleanText(v, 200)
			}
		}
	}

	if p.Name == "" || p.CPR == "" || p.DOB == "" || p.Phone == "" {
		return p, "", "البيانات الأساسية غير مكتملة"
	}
	return p, "", ""
}

// ---------------------------------------------------------------- member auth

type memberLogin struct {
	Phone string `json:"phone"`
	DOB   string `json:"dob"`
	CPR   string `json:"cpr"`
}

func (s *Server) handleMemberLogin(w http.ResponseWriter, r *http.Request) {
	if !s.checkOrigin(w, r) {
		return
	}
	ip := clientIP(r)
	if !s.limiter.allow("login:"+ip, 40, 15*time.Minute) {
		fail(w, http.StatusTooManyRequests, "عدد محاولات الدخول تجاوز الحد. الرجاء المحاولة بعد قليل")
		return
	}
	var req memberLogin
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	phone, errP := store.ValidatePhone(req.Phone)
	cpr, errC := store.ValidateCPR(req.CPR)
	dob := strings.TrimSpace(store.NormalizeDigits(req.DOB))
	dob = strings.ReplaceAll(dob, "/", "-")
	if len(dob) == 10 && dob[2] == '-' {
		dob = dob[6:10] + "-" + dob[3:5] + "-" + dob[0:2]
	}
	if errP != nil || errC != nil || dob == "" {
		fail(w, http.StatusBadRequest, "الرجاء إدخال البيانات كاملة وبشكل صحيح")
		return
	}

	u, err := s.st.FindUserByCredentials(phone, dob, cpr)
	if err != nil {
		status := s.st.PendingApplicationStatus(cpr, phone)
		switch status {
		case "pending":
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":  "طلبك قيد المراجعة من قبل إدارة المأتم. سيتم إشعارك عند اعتماد العضوية",
				"status": "pending",
			})
		case "rejected":
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":  "لم يتم اعتماد طلب العضوية. للمراجعة يرجى التواصل مع إدارة المأتم",
				"status": "rejected",
			})
		default:
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":  "لا يوجد حساب بهذه البيانات. يمكنك تقديم طلب عضوية جديد",
				"status": "not_found",
			})
		}
		return
	}

	token, expires, err := s.st.CreateSession(subjectUser, u.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر إنشاء الجلسة")
		return
	}
	s.setSessionCookie(w, cookieUser, token, expires)
	ok(w, map[string]any{"name": u.Name})
}

func (s *Server) handleMemberLogout(w http.ResponseWriter, r *http.Request) {
	if !s.checkOrigin(w, r) {
		return
	}
	if c, err := r.Cookie(cookieUser); err == nil {
		s.st.DeleteSession(c.Value)
	}
	s.clearCookie(w, cookieUser)
	ok(w, map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------- member area

type memberState struct {
	Name       string `json:"name"`
	CPR        string `json:"cpr"`
	Message    string `json:"message"`
	VotingOpen bool   `json:"voting_open"`
	HasVoted   bool   `json:"has_voted"`
	Title      string `json:"voting_title"`
	Announce   string `json:"announcement"`
	Closed     bool   `json:"voting_closed"`
	OrgName    string `json:"org_name"`
	Author     string `json:"footer_author"`
	Instagram  string `json:"footer_instagram"`
}

func (s *Server) handleMemberMe(w http.ResponseWriter, r *http.Request, u store.User) {
	status := s.st.VotingStatus()
	st := memberState{
		Name:       u.Name,
		CPR:        u.CPR,
		Message:    s.st.Setting("user_waiting_message", ""),
		VotingOpen: status == store.VotingOpen,
		HasVoted:   s.st.HasVoted(u.ID),
		Title:      s.st.Setting("voting_title", ""),
		Announce:   s.st.Setting("voting_announcement", ""),
		Closed:     status == store.VotingClosed,
		OrgName:    s.st.Setting("org_name", ""),
		Author:     s.st.Setting("footer_author", ""),
		Instagram:  s.st.Setting("footer_instagram", ""),
	}
	if st.Closed {
		st.Message = s.st.Setting("voting_closed_message", st.Message)
	}
	ok(w, st)
}

type ballotSection struct {
	RoleID      int64               `json:"role_id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	MaxChoices  int                 `json:"max_choices"`
	Winners     int                 `json:"winners"`
	Candidates  []store.Participant `json:"candidates"`
}

type ballotForm struct {
	Title    string          `json:"title"`
	Announce string          `json:"announcement"`
	Mode     string          `json:"mode"`
	HasVoted bool            `json:"has_voted"`
	Sections []ballotSection `json:"sections"`
}

// handleBallotForm returns the ballot without any tallies. Vote counts are
// never included in a member-facing payload.
func (s *Server) handleBallotForm(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.st.VotingStatus() != store.VotingOpen {
		fail(w, http.StatusForbidden, "التصويت غير متاح حالياً")
		return
	}
	form := ballotForm{
		Title:    s.st.Setting("voting_title", ""),
		Announce: s.st.Setting("voting_announcement", ""),
		Mode:     s.st.VotingMode(),
		HasVoted: s.st.HasVoted(u.ID),
	}
	if form.Mode == "single" {
		cands, err := s.st.Participants(0, false)
		if err != nil {
			fail(w, http.StatusInternalServerError, "تعذر تحميل قائمة المرشحين")
			return
		}
		form.Sections = []ballotSection{{
			RoleID:     0,
			Name:       s.st.Setting("voting_title", "المرشحون"),
			MaxChoices: s.st.SingleSelections(),
			Winners:    s.st.SingleWinners(),
			Candidates: cands,
		}}
	} else {
		roles, err := s.st.Roles()
		if err != nil {
			fail(w, http.StatusInternalServerError, "تعذر تحميل المناصب")
			return
		}
		for _, role := range roles {
			cands, err := s.st.Participants(role.ID, false)
			if err != nil {
				fail(w, http.StatusInternalServerError, "تعذر تحميل قائمة المرشحين")
				return
			}
			form.Sections = append(form.Sections, ballotSection{
				RoleID:      role.ID,
				Name:        role.Name,
				Description: role.Description,
				MaxChoices:  role.SelectionsAllowed,
				Winners:     role.WinnersCount,
				Candidates:  cands,
			})
		}
	}
	ok(w, form)
}

type castRequest struct {
	Selections map[string][]int64 `json:"selections"`
}

func (s *Server) handleCastBallot(w http.ResponseWriter, r *http.Request, u store.User) {
	var req castRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	sel := map[int64][]int64{}
	for k, v := range req.Selections {
		var roleID int64
		if _, err := parseInt(k, &roleID); err != nil {
			fail(w, http.StatusBadRequest, "بيانات التصويت غير صالحة")
			return
		}
		sel[roleID] = v
	}
	if err := s.st.CastBallot(u.ID, sel); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"message": "تم تسجيل تصويتك بنجاح"})
}

func parseInt(s string, dst *int64) (int64, error) {
	var v int64
	var neg bool
	for i, r := range s {
		if i == 0 && r == '-' {
			neg = true
			continue
		}
		if r < '0' || r > '9' {
			return 0, errBadInt
		}
		v = v*10 + int64(r-'0')
	}
	if neg {
		v = -v
	}
	*dst = v
	return v, nil
}

var errBadInt = errString("invalid integer")

type errString string

func (e errString) Error() string { return string(e) }

// ---------------------------------------------------------------- staff gate

// handleStaffGate is the entry point behind the hidden trigger on the public
// pages. The operator supplies only a username and password; the server works
// out which dashboard the account belongs to, opens the session, and returns
// the address to go to. That way the secret paths never appear in any page
// source, and neither operator has to remember a URL.
//
// A wrong username, a wrong password, and a nonexistent account all produce the
// same message, so nothing here reveals whether an account exists.
func (s *Server) handleStaffGate(w http.ResponseWriter, r *http.Request) {
	if !s.checkOrigin(w, r) {
		return
	}
	if !s.limiter.allow("gate:"+clientIP(r), 8, 15*time.Minute) {
		fail(w, http.StatusTooManyRequests, "عدد محاولات الدخول تجاوز الحد. الرجاء المحاولة بعد قليل")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	candidates := []struct {
		kind, cookie, subject, path string
	}{
		{store.KindMembership, cookieAdminMember, subjectAdminMem, s.cfg.AdminPath},
		{store.KindElections, cookieAdminElection, subjectAdminElec, s.cfg.ElectionsPath},
	}
	for _, c := range candidates {
		a, hash, err := s.st.AdminByUsername(req.Username, c.kind)
		if err != nil || !auth.VerifyPassword(hash, req.Password) {
			continue
		}
		token, expires, err := s.st.CreateSession(c.subject, a.ID)
		if err != nil {
			fail(w, http.StatusInternalServerError, "تعذر إنشاء الجلسة")
			return
		}
		s.setSessionCookie(w, c.cookie, token, expires)
		s.st.Audit(a.Username, "login", c.kind+" (بوابة)")
		ok(w, map[string]any{
			"redirect":     "/" + c.path,
			"display_name": a.DisplayName,
		})
		return
	}
	fail(w, http.StatusUnauthorized, "اسم المستخدم أو كلمة المرور غير صحيحة")
}
