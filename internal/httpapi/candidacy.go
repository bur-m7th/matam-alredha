package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"matam-alredha/internal/imaging"
	"matam-alredha/internal/store"
)

// ---------------------------------------------------------------- member side

// roleEligibility explains, per post, whether this member may choose it.
type roleEligibility struct {
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason"`
	MinAge   int    `json:"min_age"`
}

type candidacyView struct {
	GateOpen  bool             `json:"gate_open"`
	Eligible  bool             `json:"eligible"`
	Reason    string           `json:"reason"`
	Intro     string           `json:"intro"`
	Name      string           `json:"name"`
	MinAge    int              `json:"min_age"`
	Roles     []store.Role     `json:"roles"`
	Mine      *store.Candidacy `json:"mine"`
	CanAmend  bool             `json:"can_amend"`
	PhotoMin  int              `json:"photo_min"`
	PhotoMax  int              `json:"photo_max"`
	PhotoSize int              `json:"photo_max_bytes"`

	RoleEligibility map[string]roleEligibility `json:"role_eligibility"`
}

func (s *Server) handleMyCandidacy(w http.ResponseWriter, r *http.Request, u store.User) {
	eligible, reason := s.st.EligibleToStand(u)
	view := candidacyView{
		GateOpen:  s.st.CandidacyGate() == store.GateOpen,
		Eligible:  eligible,
		Reason:    reason,
		Intro:     s.st.Setting("candidacy_intro", ""),
		Name:      u.Name,
		MinAge:    store.DefaultMinCandidateAge,
		PhotoMin:  imaging.MinSide,
		PhotoMax:  imaging.MaxSide,
		PhotoSize: imaging.MaxUploadBytes,
	}
	roles, err := s.st.Roles()
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر تحميل المناصب")
		return
	}
	view.Roles = roles

	// The page needs to know which posts this member is old enough for, so it
	// can grey out the rest and say why rather than failing on submit.
	view.RoleEligibility = make(map[string]roleEligibility, len(roles))
	for _, r := range roles {
		okRole, why := s.st.EligibleForRole(u, r)
		view.RoleEligibility[strconv.FormatInt(r.ID, 10)] = roleEligibility{
			Eligible: okRole,
			Reason:   why,
			MinAge:   r.MinAge,
		}
	}

	c, found, err := s.st.CandidacyForUser(u.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر تحميل طلب الترشح")
		return
	}
	if found {
		view.Mine = &c
		// A member may edit while the window is open and they are not yet
		// accepted; after acceptance only the committee can change anything.
		view.CanAmend = view.GateOpen && c.Status != store.CandidacyAccepted
	}
	ok(w, view)
}

// handleSubmitCandidacy accepts the multipart form: a role and an optional new
// photograph. The name is never sent by the browser; it comes from the member's
// own record, which is what the form tells them.
func (s *Server) handleSubmitCandidacy(w http.ResponseWriter, r *http.Request, u store.User) {
	if !s.checkOrigin(w, r) {
		return
	}
	if s.st.CandidacyGate() != store.GateOpen {
		fail(w, http.StatusForbidden, "باب الترشح مغلق حالياً")
		return
	}
	if eligible, reason := s.st.EligibleToStand(u); !eligible {
		fail(w, http.StatusForbidden, reason)
		return
	}
	if err := r.ParseMultipartForm(imaging.MaxUploadBytes + (1 << 20)); err != nil {
		fail(w, http.StatusBadRequest, "تعذر قراءة النموذج")
		return
	}

	roleID, err := strconv.ParseInt(r.FormValue("role_id"), 10, 64)
	if err != nil || roleID <= 0 {
		fail(w, http.StatusBadRequest, "الرجاء اختيار المنصب")
		return
	}

	existing, hadOne, err := s.st.CandidacyForUser(u.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر قراءة الطلب الحالي")
		return
	}

	photoRef, thumbRef := "", ""
	file, _, ferr := r.FormFile("photo")
	if ferr == nil {
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, imaging.MaxUploadBytes+1))
		if err != nil {
			fail(w, http.StatusBadRequest, "تعذر قراءة الصورة")
			return
		}
		res, err := imaging.Process(raw)
		if err != nil {
			// These messages are written for the member, so pass them through.
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		photoRef, thumbRef, err = s.storePhotoPair(res)
		if err != nil {
			fail(w, http.StatusInternalServerError, "تعذر حفظ الصورة")
			return
		}
	} else if !hadOne {
		fail(w, http.StatusBadRequest, "الرجاء إرفاق صورة شخصية")
		return
	}

	if err := s.st.SubmitCandidacy(u, roleID, photoRef, thumbRef); err != nil {
		s.removePhoto(photoRef)
		s.removePhoto(thumbRef)
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	// A replaced photo is no longer referenced by anything.
	if photoRef != "" && hadOne && existing.Photo != "" {
		s.removePhoto(existing.Photo)
		s.removePhoto(existing.Thumb)
	}
	ok(w, map[string]string{"message": "تم إرسال طلب الترشح بنجاح"})
}

// handleChangeMyRole lets a member move their own candidacy to another post
// while the window is open.
func (s *Server) handleChangeMyRole(w http.ResponseWriter, r *http.Request, u store.User) {
	if !s.checkOrigin(w, r) {
		return
	}
	var req struct {
		RoleID int64 `json:"role_id"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	c, found, err := s.st.CandidacyForUser(u.ID)
	if err != nil || !found {
		fail(w, http.StatusNotFound, "لا يوجد طلب ترشح")
		return
	}
	if err := s.st.ChangeCandidacyRole(c.ID, req.RoleID, true, u.Name); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"message": "تم تغيير المنصب"})
}

// storePhotoPair writes the processed display copy and thumbnail, returning the
// public references for each.
func (s *Server) storePhotoPair(res imaging.Result) (string, string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	name := hex.EncodeToString(raw) + ".jpg"
	thumbName := "t_" + name

	if err := os.MkdirAll(s.cfg.UploadDir, 0o755); err != nil {
		return "", "", err
	}

	if err := os.WriteFile(filepath.Join(s.cfg.UploadDir, name), res.Display, 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(s.cfg.UploadDir, thumbName), res.Thumb, 0o600); err != nil {
		os.Remove(filepath.Join(s.cfg.UploadDir, name))
		return "", "", err
	}
	return "/uploads/" + name, "/uploads/" + thumbName, nil
}

// ---------------------------------------------------------------- admin side

func (s *Server) handleListCandidacies(w http.ResponseWriter, r *http.Request, a store.Admin) {
	list, err := s.st.Candidacies(r.URL.Query().Get("status"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر تحميل طلبات الترشح")
		return
	}
	submitted, accepted, returned := s.st.CandidacyCounts()
	roles, _ := s.st.Roles()
	ok(w, map[string]any{
		"candidacies": list,
		"roles":       roles,
		"gate_open":   s.st.CandidacyGate() == store.GateOpen,
		"counts": map[string]int{
			"submitted": submitted,
			"accepted":  accepted,
			"returned":  returned,
		},
	})
}

func (s *Server) handleAcceptCandidacy(w http.ResponseWriter, r *http.Request, a store.Admin) {
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, http.StatusBadRequest, "معرف غير صالح")
		return
	}
	if err := s.st.AcceptCandidacy(id, a.Username); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"message": "تم قبول الترشح"})
}

// handleReturnCandidacy is the only alternative to acceptance. Nothing is
// rejected or deleted: the application goes back with a note.
func (s *Server) handleReturnCandidacy(w http.ResponseWriter, r *http.Request, a store.Admin) {
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, http.StatusBadRequest, "معرف غير صالح")
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.ReturnCandidacy(id, req.Note, a.Username); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"message": "تمت إعادة الطلب للمترشح مع الملاحظة"})
}

func (s *Server) handleAdminCandidacyRole(w http.ResponseWriter, r *http.Request, a store.Admin) {
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, http.StatusBadRequest, "معرف غير صالح")
		return
	}
	var req struct {
		RoleID int64 `json:"role_id"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.ChangeCandidacyRole(id, req.RoleID, false, a.Username); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"message": "تم تغيير المنصب"})
}

func (s *Server) handleCandidacyGate(w http.ResponseWriter, r *http.Request, a store.Admin) {
	var req struct {
		Gate  string `json:"gate"`
		Intro string `json:"intro"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Intro != "" {
		_ = s.st.SetSetting("candidacy_intro", store.CleanMultiline(req.Intro, 1000))
	}
	if req.Gate != "" {
		if err := s.st.SetCandidacyGate(req.Gate, a.Username); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	msg := "تم فتح باب الترشح"
	if s.st.CandidacyGate() == store.GateClosed {
		msg = "باب الترشح مغلق"
	}
	ok(w, map[string]any{"message": msg, "gate_open": s.st.CandidacyGate() == store.GateOpen})
}

func (s *Server) handleDisplayFields(w http.ResponseWriter, r *http.Request, a store.Admin) {
	if r.Method == http.MethodGet {
		ok(w, s.st.DisplayFieldSettings())
		return
	}
	var req store.DisplayFields
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.SetDisplayFields(req, a.Username); err != nil {
		fail(w, http.StatusInternalServerError, "تعذر حفظ الإعداد")
		return
	}
	ok(w, map[string]string{"message": "تم حفظ البيانات الظاهرة للناخبين"})
}
