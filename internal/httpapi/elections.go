package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"matam-alredha/internal/store"
)

func (s *Server) electionRoutes(m *http.ServeMux) {
	k := store.KindElections
	m.HandleFunc("POST /api/elections/login", s.handleAdminLogin(k))
	m.HandleFunc("POST /api/elections/logout", s.handleAdminLogout(k))
	m.HandleFunc("GET /api/elections/session", s.requireAdmin(k, s.handleAdminSession))
	m.HandleFunc("POST /api/elections/password", s.requireAdmin(k, s.changePassword))

	m.HandleFunc("GET /api/elections/overview", s.requireAdmin(k, s.handleElectionOverview))
	m.HandleFunc("PUT /api/elections/settings", s.requireAdmin(k, s.handleElectionSettings))
	m.HandleFunc("POST /api/elections/status", s.requireAdmin(k, s.handleElectionStatus))
	m.HandleFunc("POST /api/elections/reset", s.requireAdmin(k, s.handleResetVotes))

	m.HandleFunc("POST /api/elections/roles", s.requireAdmin(k, s.handleCreateRole))
	m.HandleFunc("PUT /api/elections/roles/{id}", s.requireAdmin(k, s.handleUpdateRole))
	m.HandleFunc("DELETE /api/elections/roles/{id}", s.requireAdmin(k, s.handleDeleteRole))

	m.HandleFunc("POST /api/elections/participants", s.requireAdmin(k, s.handleCreateParticipant))
	m.HandleFunc("PUT /api/elections/participants/{id}", s.requireAdmin(k, s.handleUpdateParticipant))
	m.HandleFunc("DELETE /api/elections/participants/{id}", s.requireAdmin(k, s.handleDeleteParticipant))
	m.HandleFunc("POST /api/elections/photo", s.requireAdmin(k, s.handleUploadPhoto))
}

// resultSection carries tallies. This payload is only ever produced inside a
// handler wrapped by requireAdmin(elections) — members never receive counts.
type resultSection struct {
	RoleID      int64               `json:"role_id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Winners     int                 `json:"winners"`
	Selections  int                 `json:"selections"`
	Candidates  []store.Participant `json:"candidates"`
	TotalVotes  int                 `json:"total_votes"`
}

type electionOverview struct {
	Admin        store.Admin     `json:"admin"`
	Mode         string          `json:"mode"`
	Status       string          `json:"status"`
	Title        string          `json:"title"`
	Announcement string          `json:"announcement"`
	ClosedMsg    string          `json:"closed_message"`
	SingleWin    int             `json:"single_winners"`
	SingleSel    int             `json:"single_selections"`
	Roles        []store.Role    `json:"roles"`
	Sections     []resultSection `json:"sections"`
	Voted        int             `json:"voted"`
	Eligible     int             `json:"eligible"`
	OrgName      string          `json:"org_name"`
}

func (s *Server) handleElectionOverview(w http.ResponseWriter, r *http.Request, a store.Admin) {
	roles, err := s.st.Roles()
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر تحميل المناصب")
		return
	}
	o := electionOverview{
		Admin:        a,
		Mode:         s.st.VotingMode(),
		Status:       s.st.VotingStatus(),
		Title:        s.st.Setting("voting_title", ""),
		Announcement: s.st.Setting("voting_announcement", ""),
		ClosedMsg:    s.st.Setting("voting_closed_message", ""),
		SingleWin:    s.st.SingleWinners(),
		SingleSel:    s.st.SingleSelections(),
		Roles:        roles,
		OrgName:      s.st.Setting("org_name", ""),
	}
	o.Voted, o.Eligible, _ = s.st.Turnout()

	if o.Mode == "single" {
		cands, err := s.st.Participants(0, true)
		if err != nil {
			fail(w, http.StatusInternalServerError, "تعذر تحميل المرشحين")
			return
		}
		o.Sections = []resultSection{{
			RoleID:     0,
			Name:       "قائمة المرشحين",
			Winners:    o.SingleWin,
			Selections: o.SingleSel,
			Candidates: cands,
			TotalVotes: sumVotes(cands),
		}}
	} else {
		for _, role := range roles {
			cands, err := s.st.Participants(role.ID, true)
			if err != nil {
				fail(w, http.StatusInternalServerError, "تعذر تحميل المرشحين")
				return
			}
			o.Sections = append(o.Sections, resultSection{
				RoleID:      role.ID,
				Name:        role.Name,
				Description: role.Description,
				Winners:     role.WinnersCount,
				Selections:  role.SelectionsAllowed,
				Candidates:  cands,
				TotalVotes:  sumVotes(cands),
			})
		}
		// Candidates left without a role would be invisible in this mode.
		if orphans, err := s.st.Participants(0, true); err == nil && len(orphans) > 0 {
			o.Sections = append(o.Sections, resultSection{
				RoleID:     0,
				Name:       "مرشحون بدون منصب",
				Winners:    0,
				Candidates: orphans,
				TotalVotes: sumVotes(orphans),
			})
		}
	}
	ok(w, o)
}

func sumVotes(ps []store.Participant) int {
	total := 0
	for _, p := range ps {
		total += p.Votes
	}
	return total
}

// ---------------------------------------------------------------- settings

type electionSettingsRequest struct {
	Mode         string `json:"mode"`
	Title        string `json:"title"`
	Announcement string `json:"announcement"`
	ClosedMsg    string `json:"closed_message"`
	SingleWin    int    `json:"single_winners"`
	SingleSel    int    `json:"single_selections"`
}

func (s *Server) handleElectionSettings(w http.ResponseWriter, r *http.Request, a store.Admin) {
	var req electionSettingsRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.st.VotingStatus() == store.VotingOpen && req.Mode != s.st.VotingMode() {
		fail(w, http.StatusBadRequest, "لا يمكن تغيير نظام التصويت أثناء فتح التصويت")
		return
	}
	if req.Mode != "roles" && req.Mode != "single" {
		fail(w, http.StatusBadRequest, "نظام تصويت غير معروف")
		return
	}
	title := store.CleanText(req.Title, 200)
	if title == "" {
		fail(w, http.StatusBadRequest, "عنوان التصويت مطلوب")
		return
	}
	if req.SingleWin < 1 {
		req.SingleWin = 1
	}
	if req.SingleSel < 1 {
		req.SingleSel = req.SingleWin
	}
	for k, v := range map[string]string{
		"voting_mode":           req.Mode,
		"voting_title":          title,
		"voting_announcement":   store.CleanMultiline(req.Announcement, 2000),
		"voting_closed_message": store.CleanMultiline(req.ClosedMsg, 400),
		"single_winners_count":  strconv.Itoa(req.SingleWin),
		"single_selections":     strconv.Itoa(req.SingleSel),
	} {
		if err := s.st.SetSetting(k, v); err != nil {
			fail(w, http.StatusInternalServerError, "تعذر حفظ الإعدادات")
			return
		}
	}
	s.st.Audit(a.Username, "election_settings", req.Mode)
	ok(w, map[string]string{"message": "تم حفظ إعدادات التصويت"})
}

type statusRequest struct {
	Status string `json:"status"`
}

func (s *Server) handleElectionStatus(w http.ResponseWriter, r *http.Request, a store.Admin) {
	var req statusRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.SetVotingStatus(req.Status, a.Username); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	messages := map[string]string{
		store.VotingDraft:  "تم إيقاف الإعلان. التصويت غير ظاهر للأعضاء",
		store.VotingOpen:   "تم فتح التصويت وأصبح متاحاً للأعضاء المعتمدين",
		store.VotingClosed: "تم إغلاق التصويت",
	}
	ok(w, map[string]string{"message": messages[req.Status]})
}

func (s *Server) handleResetVotes(w http.ResponseWriter, r *http.Request, a store.Admin) {
	if err := s.st.ResetVotes(a.Username); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"message": "تم مسح جميع الأصوات"})
}

// ---------------------------------------------------------------- roles

func (s *Server) handleCreateRole(w http.ResponseWriter, r *http.Request, a store.Admin) {
	var role store.Role
	if err := decode(w, r, &role); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.st.CreateRole(role, a.Username)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, created)
}

func (s *Server) handleUpdateRole(w http.ResponseWriter, r *http.Request, a store.Admin) {
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, http.StatusBadRequest, "معرف غير صالح")
		return
	}
	var role store.Role
	if err := decode(w, r, &role); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	role.ID = id
	if err := s.st.UpdateRole(role, a.Username); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"message": "تم حفظ المنصب"})
}

func (s *Server) handleDeleteRole(w http.ResponseWriter, r *http.Request, a store.Admin) {
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, http.StatusBadRequest, "معرف غير صالح")
		return
	}
	if err := s.st.DeleteRole(id, a.Username); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"message": "تم حذف المنصب ومرشحيه"})
}

// ---------------------------------------------------------------- candidates

func (s *Server) handleCreateParticipant(w http.ResponseWriter, r *http.Request, a store.Admin) {
	var p store.Participant
	if err := decode(w, r, &p); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validatePhotoRef(p.Photo); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.st.CreateParticipant(p, a.Username)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, created)
}

func (s *Server) handleUpdateParticipant(w http.ResponseWriter, r *http.Request, a store.Admin) {
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, http.StatusBadRequest, "معرف غير صالح")
		return
	}
	var p store.Participant
	if err := decode(w, r, &p); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	p.ID = id
	if err := s.validatePhotoRef(p.Photo); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	old, _ := s.st.Participant(id)
	if err := s.st.UpdateParticipant(p, a.Username); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if old.Photo != "" && old.Photo != p.Photo {
		s.removePhoto(old.Photo)
	}
	ok(w, map[string]string{"message": "تم حفظ بيانات المرشح"})
}

func (s *Server) handleDeleteParticipant(w http.ResponseWriter, r *http.Request, a store.Admin) {
	id, err := pathID(r, "id")
	if err != nil {
		fail(w, http.StatusBadRequest, "معرف غير صالح")
		return
	}
	photo, err := s.st.DeleteParticipant(id, a.Username)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.removePhoto(photo)
	ok(w, map[string]string{"message": "تم حذف المرشح"})
}

// ---------------------------------------------------------------- photos

var allowedImages = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// handleUploadPhoto accepts one image and returns the path to reference it.
// The stored name is random, and the extension comes from sniffed content
// rather than from the client-supplied filename.
func (s *Server) handleUploadPhoto(w http.ResponseWriter, r *http.Request, a store.Admin) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUpload)
	if err := r.ParseMultipartForm(s.cfg.MaxUpload); err != nil {
		fail(w, http.StatusBadRequest, "حجم الصورة كبير جداً. الحد الأقصى خمسة ميغابايت")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, _, err := r.FormFile("photo")
	if err != nil {
		fail(w, http.StatusBadRequest, "لم يتم اختيار صورة")
		return
	}
	defer file.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	head = head[:n]
	ext, allowed := allowedImages[http.DetectContentType(head)]
	if !allowed {
		fail(w, http.StatusBadRequest, "صيغة الصورة غير مدعومة. استخدم JPG أو PNG أو WEBP")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		fail(w, http.StatusInternalServerError, "تعذر قراءة الصورة")
		return
	}

	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		fail(w, http.StatusInternalServerError, "تعذر حفظ الصورة")
		return
	}
	name := hex.EncodeToString(raw) + ext
	if err := os.MkdirAll(s.cfg.UploadDir, 0o755); err != nil {
		fail(w, http.StatusInternalServerError, "تعذر حفظ الصورة")
		return
	}
	dst, err := os.Create(filepath.Join(s.cfg.UploadDir, name))
	if err != nil {
		fail(w, http.StatusInternalServerError, "تعذر حفظ الصورة")
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, io.LimitReader(file, s.cfg.MaxUpload)); err != nil {
		fail(w, http.StatusInternalServerError, "تعذر حفظ الصورة")
		return
	}
	ok(w, map[string]string{"photo": "/uploads/" + name})
}

// validatePhotoRef makes sure a stored photo path cannot escape the upload dir.
func (s *Server) validatePhotoRef(p string) error {
	if p == "" {
		return nil
	}
	if !strings.HasPrefix(p, "/uploads/") {
		return errString("مسار الصورة غير صالح")
	}
	name := strings.TrimPrefix(p, "/uploads/")
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return errString("مسار الصورة غير صالح")
	}
	if _, err := os.Stat(filepath.Join(s.cfg.UploadDir, name)); err != nil {
		return errString("الصورة غير موجودة على الخادم")
	}
	return nil
}

func (s *Server) removePhoto(p string) {
	if s.validatePhotoRef(p) != nil {
		return
	}
	_ = os.Remove(filepath.Join(s.cfg.UploadDir, strings.TrimPrefix(p, "/uploads/")))
}
