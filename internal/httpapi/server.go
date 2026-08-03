package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"matam-alredha/internal/store"
)

// Cookie names are distinct per audience. A membership-admin cookie is never
// even read by the elections routes, so the two dashboards cannot cross over.
const (
	cookieUser          = "mtm_member"
	cookieAdminMember   = "mtm_admin_membership"
	cookieAdminElection = "mtm_admin_elections"

	subjectUser      = "user"
	subjectAdminMem  = "admin:membership"
	subjectAdminElec = "admin:elections"
)

type Config struct {
	Store *store.Store
	// RegisterLimit caps registrations per IP per hour. A whole village may
	// share one connection, so this is deliberately generous.
	RegisterLimit int

	// AdminPath and ElectionsPath are the single URL segments the two
	// dashboards live under. Both the page and its API sit beneath them, so a
	// visitor who does not know the segment sees an ordinary 404 everywhere.
	AdminPath     string
	ElectionsPath string

	// TrustProxy tells the server it sits behind a reverse proxy or a
	// Cloudflare tunnel, so forwarded client-address headers are meaningful.
	// It must stay false when the server is reachable directly: otherwise a
	// caller can forge the header and sidestep every rate limit.
	TrustProxy bool
	WebFS      fs.FS
	UploadDir  string
	ExportPath string
	Secure     bool // set the Secure flag on cookies (behind HTTPS)
	MaxUpload  int64
}

type Server struct {
	cfg     Config
	st      *store.Store
	mux     *http.ServeMux
	limiter *rateLimiter
}

func New(cfg Config) *Server {
	if cfg.MaxUpload == 0 {
		cfg.MaxUpload = 5 << 20
	}
	if cfg.RegisterLimit <= 0 {
		cfg.RegisterLimit = 40
	}
	cfg.AdminPath = sanitizePath(cfg.AdminPath, "admin")
	cfg.ElectionsPath = sanitizePath(cfg.ElectionsPath, "elections")
	if cfg.AdminPath == cfg.ElectionsPath {
		cfg.ElectionsPath += "-elections"
	}
	s := &Server{
		cfg:     cfg,
		st:      cfg.Store,
		mux:     http.NewServeMux(),
		limiter: newRateLimiter(),
	}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; img-src 'self' data:; "+
			"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
			"font-src 'self' https://fonts.gstatic.com data:; "+
			"script-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	m := s.mux

	// Pages.
	//
	// The root address is the first thing a member ever sees, and it should
	// show what they can actually do: apply, while applications are being
	// taken, or sign in once that window has closed. "/login" is a fixed
	// address for the sign-in form regardless of that state, since a member
	// who already has an account should always be able to reach it directly.
	m.HandleFunc("GET /{$}", s.landingPage)
	m.HandleFunc("GET /login", s.page("login.html"))
	m.HandleFunc("GET /register", s.registerPage)
	m.HandleFunc("GET /submitted", s.page("submitted.html"))
	m.HandleFunc("GET /member", s.page("member.html"))
	m.HandleFunc("GET /vote", s.page("vote.html"))
	m.HandleFunc("GET /candidacy", s.page("candidacy.html"))
	// The dashboards are unlinked from every public page and live at whatever
	// segment the operator configured.
	m.HandleFunc("GET /"+s.cfg.AdminPath, s.page("admin.html"))
	m.HandleFunc("GET /"+s.cfg.ElectionsPath, s.page("elections.html"))

	// Keep the whole site out of search indexes.
	m.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
	})

	// Static assets and uploaded photos
	m.Handle("GET /static/", http.FileServer(http.FS(s.cfg.WebFS)))
	// Uploaded photos are content-addressed by a random name and never mutate,
	// so a long cache lifetime costs nothing and spares the server a request
	// per photo on every ballot view.
	m.Handle("GET /uploads/", http.StripPrefix("/uploads/",
		s.noIndex(cacheForever(http.FileServer(http.Dir(s.cfg.UploadDir))))))

	// Public API
	m.HandleFunc("GET /api/public/config", s.handlePublicConfig)

	// Liveness probe for the container runtime. Deliberately reveals nothing.
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.st.DB.Ping(); err != nil {
			http.Error(w, "unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	m.HandleFunc("GET /api/public/form", s.handlePublicForm)
	m.HandleFunc("POST /api/public/register", s.handleRegister)
	m.HandleFunc("POST /api/auth/login", s.handleMemberLogin)
	m.HandleFunc("POST /api/auth/logout", s.handleMemberLogout)

	// Hidden staff entrance. Credentials decide which dashboard to open.
	m.HandleFunc("POST /api/auth/staff", s.handleStaffGate)

	// Standing for election: the member's own candidacy.
	m.HandleFunc("GET /api/member/candidacy", s.requireMember(s.handleMyCandidacy))
	m.HandleFunc("POST /api/member/candidacy", s.requireMember(s.handleSubmitCandidacy))
	m.HandleFunc("PUT /api/member/candidacy/role", s.requireMember(s.handleChangeMyRole))

	// Member API
	m.HandleFunc("GET /api/member/me", s.requireMember(s.handleMemberMe))
	m.HandleFunc("GET /api/member/ballot", s.requireMember(s.handleBallotForm))
	m.HandleFunc("POST /api/member/ballot", s.requireMember(s.handleCastBallot))

	s.membershipRoutes(m)
	s.electionRoutes(m)
}

// ---------------------------------------------------------------- pages

func (s *Server) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(s.cfg.WebFS, path.Join("pages", name))
		if err != nil {
			http.Error(w, "الصفحة غير موجودة", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	}
}

// reservedPaths cannot be used as a dashboard segment; they already mean
// something else.
var reservedPaths = map[string]bool{
	"register": true, "submitted": true, "member": true, "vote": true,
	"static": true, "uploads": true, "api": true, "healthz": true, "robots.txt": true,
	"login": true,
}

// sanitizePath reduces an operator-supplied value to one safe URL segment.
func sanitizePath(v, fallback string) string {
	v = strings.Trim(strings.TrimSpace(v), "/")
	v = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return -1
	}, v)
	if v == "" || reservedPaths[strings.ToLower(v)] {
		return fallback
	}
	return v
}

// cacheForever marks immutable assets so browsers reuse them without asking.
func cacheForever(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		h.ServeHTTP(w, r)
	})
}

func (s *Server) noIndex(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		h.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- responses

type apiError struct {
	Error string `json:"error"`
	Field string `json:"field,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

func failField(w http.ResponseWriter, status int, field, msg string) {
	writeJSON(w, status, apiError{Error: msg, Field: field})
}

func ok(w http.ResponseWriter, v any) { writeJSON(w, http.StatusOK, v) }

// decode reads a JSON body with a size cap.
func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("تعذر قراءة البيانات المرسلة")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("البيانات المرسلة غير صالحة")
	}
	return nil
}

// ---------------------------------------------------------------- sessions

func (s *Server) setSessionCookie(w http.ResponseWriter, name, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) sessionFor(r *http.Request, cookieName, wantKind string) (store.Session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return store.Session{}, false
	}
	sess, valid := s.st.LookupSession(c.Value)
	if !valid || sess.SubjectKind != wantKind {
		return store.Session{}, false
	}
	return sess, true
}

func (s *Server) requireMember(next func(http.ResponseWriter, *http.Request, store.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.checkOrigin(w, r) {
			return
		}
		sess, okSess := s.sessionFor(r, cookieUser, subjectUser)
		if !okSess {
			fail(w, http.StatusUnauthorized, "الرجاء تسجيل الدخول")
			return
		}
		u, err := s.st.User(sess.SubjectID)
		if err != nil {
			s.st.DeleteSession(sess.Token)
			s.clearCookie(w, cookieUser)
			fail(w, http.StatusUnauthorized, "انتهت الجلسة. الرجاء تسجيل الدخول مرة أخرى")
			return
		}
		next(w, r, u)
	}
}

func (s *Server) requireAdmin(kind string, next func(http.ResponseWriter, *http.Request, store.Admin)) http.HandlerFunc {
	cookieName, subject := cookieAdminMember, subjectAdminMem
	if kind == store.KindElections {
		cookieName, subject = cookieAdminElection, subjectAdminElec
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.checkOrigin(w, r) {
			return
		}
		sess, okSess := s.sessionFor(r, cookieName, subject)
		if !okSess {
			fail(w, http.StatusUnauthorized, "الرجاء تسجيل الدخول")
			return
		}
		a, err := s.st.Admin(sess.SubjectID)
		if err != nil || a.Kind != kind {
			s.st.DeleteSession(sess.Token)
			s.clearCookie(w, cookieName)
			fail(w, http.StatusForbidden, "لا تملك صلاحية الوصول إلى هذه الصفحة")
			return
		}
		next(w, r, a)
	}
}

// checkOrigin is the CSRF guard for state-changing requests. Combined with
// SameSite=Strict cookies it stops cross-site form posts.
func (s *Server) checkOrigin(w http.ResponseWriter, r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		// No Origin on a same-origin fetch from an older browser; the custom
		// header below is required by every client script.
		if r.Header.Get("X-Requested-With") == "matam" {
			return true
		}
		fail(w, http.StatusForbidden, "طلب غير موثوق")
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		fail(w, http.StatusForbidden, "طلب غير موثوق")
		return false
	}
	if !sameHost(u.Host, r.Host) {
		fail(w, http.StatusForbidden, "طلب غير موثوق")
		return false
	}
	return true
}

func sameHost(a, b string) bool {
	ha, _, err := net.SplitHostPort(a)
	if err != nil {
		ha = a
	}
	hb, _, err := net.SplitHostPort(b)
	if err != nil {
		hb = b
	}
	return strings.EqualFold(ha, hb)
}

// ---------------------------------------------------------------- rate limit

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
}

func newRateLimiter() *rateLimiter {
	rl := &rateLimiter{buckets: map[string][]time.Time{}}
	go func() {
		for range time.Tick(10 * time.Minute) {
			rl.mu.Lock()
			for k, v := range rl.buckets {
				if len(v) == 0 || time.Since(v[len(v)-1]) > time.Hour {
					delete(rl.buckets, k)
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

// allow reports whether an action may proceed, keeping at most n attempts
// within the window.
func (rl *rateLimiter) allow(key string, n int, window time.Duration) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	kept := rl.buckets[key][:0]
	for _, t := range rl.buckets[key] {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= n {
		rl.buckets[key] = kept
		return false
	}
	rl.buckets[key] = append(kept, now)
	return true
}

// clientIP identifies the caller for rate-limiting purposes.
//
// Forwarded headers are only consulted when the operator has declared that a
// proxy is in front; a directly reachable server must ignore them, since they
// are trivially forged and would let one caller appear as thousands.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		// Cloudflare's own header is preferred: it carries a single address and
		// cannot be extended by the caller the way X-Forwarded-For can.
		if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
			return cf
		}
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			// Left-most entry is the original client.
			if i := strings.IndexByte(fwd, ','); i > 0 {
				return strings.TrimSpace(fwd[:i])
			}
			return strings.TrimSpace(fwd)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func pathID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.PathValue(name), 10, 64)
}

// syncExport rewrites the master workbook after any change to the members list.
func (s *Server) syncExport() {
	if err := s.st.SyncExportFile(s.cfg.ExportPath); err != nil {
		log.Printf("export sync failed: %v", err)
	}
}
