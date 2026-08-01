// Command matam-alredha runs the general assembly membership and elections
// system for مأتم الرضا عليه السلام - المالكية.
package main

import (
	"context"
	cryptorand "crypto/rand"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"matam-alredha/internal/httpapi"
	"matam-alredha/internal/store"
)

//go:embed web
var webAssets embed.FS

//go:embed seed/members.json
var seedData []byte

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// healthcheck lets the container image probe itself without shipping curl.
func healthcheck() {
	addr := env("LISTEN_ADDR", ":8080")
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		log.Printf("healthcheck: %v", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("healthcheck: status %d", resp.StatusCode)
		os.Exit(1)
	}
	os.Exit(0)
}

func main() {
	log.SetFlags(log.LstdFlags)

	for _, arg := range os.Args[1:] {
		if arg == "-healthcheck" || arg == "--healthcheck" {
			healthcheck()
		}
	}

	dataDir := env("DATA_DIR", "./data")
	addr := env("LISTEN_ADDR", ":8080")
	dbPath := filepath.Join(dataDir, "matam.db")
	uploadDir := filepath.Join(dataDir, "uploads")
	exportPath := filepath.Join(dataDir, "اعضاء_الجمعية_العمومية.xlsx")

	for _, dir := range []string{dataDir, uploadDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("cannot create %s: %v", dir, err)
		}
	}

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("cannot open database: %v", err)
	}
	defer st.Close()

	// Import the historical membership list on first boot only.
	if n, err := st.SeedMembers(seedData); err != nil {
		log.Fatalf("cannot import member list: %v", err)
	} else if n > 0 {
		log.Printf("imported %d existing members from the supplied sheet", n)
	}

	bootstrapAdmins(st)

	if err := st.SyncExportFile(exportPath); err != nil {
		log.Printf("warning: could not write the export workbook: %v", err)
	}

	webFS, err := fs.Sub(webAssets, "web")
	if err != nil {
		log.Fatalf("cannot mount web assets: %v", err)
	}

	srv := httpapi.New(httpapi.Config{
		Store:         st,
		WebFS:         webFS,
		UploadDir:     uploadDir,
		ExportPath:    exportPath,
		Secure:        env("COOKIE_SECURE", "false") == "true",
		MaxUpload:     int64(mustAtoi(env("MAX_UPLOAD_MB", "5"))) << 20,
		RegisterLimit: mustAtoi(env("REGISTER_LIMIT_PER_HOUR", "40")),
		AdminPath:     env("ADMIN_PATH", "admin"),
		ElectionsPath: env("ELECTIONS_PATH", "elections"),
	})

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           logRequests(srv),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			st.PurgeExpiredSessions()
		}
	}()

	go func() {
		log.Printf("listening on %s", addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
}

// bootstrapAdmins creates the two operator accounts on first boot. Passwords
// come from the environment; a generated one is printed once so it can be
// changed from inside the dashboard.
func bootstrapAdmins(st *store.Store) {
	type spec struct {
		kind, userEnv, passEnv, defUser, display string
	}
	specs := []spec{
		{store.KindMembership, "ADMIN_USERNAME", "ADMIN_PASSWORD", "admin", "إدارة العضوية"},
		{store.KindElections, "ELECTIONS_USERNAME", "ELECTIONS_PASSWORD", "elections", "لجنة الانتخابات"},
	}
	// Set ADMIN_PASSWORD_RESET=true (or ELECTIONS_PASSWORD_RESET) to recover from
	// a lockout: the matching password from the environment is applied to the
	// existing account on the next boot. Unset it again afterwards.
	for _, sp := range specs {
		username := env(sp.userEnv, sp.defUser)
		password := env(sp.passEnv, "")

		if strings.EqualFold(env(strings.TrimSuffix(sp.passEnv, "_PASSWORD")+"_PASSWORD_RESET", "false"), "true") {
			if password == "" {
				log.Fatalf("%s: password reset requested but %s is empty", sp.display, sp.passEnv)
			}
			if err := st.ForceResetPassword(sp.kind, username, password); err != nil {
				log.Fatalf("%s: password reset failed: %v", sp.display, err)
			}
			log.Printf("=== %s: password reset applied for username %q. Remove the reset variable and restart. ===",
				sp.display, username)
			continue
		}

		generated := false
		if password == "" {
			password = randomPassword()
			generated = true
		}
		created, err := st.EnsureAdmin(username, password, sp.display, sp.kind)
		if err != nil {
			log.Fatalf("cannot create %s account: %v", sp.kind, err)
		}
		if created && generated {
			log.Printf("=== %s account created: username %q password %q — change it after first login ===",
				sp.display, username, password)
		} else if created {
			log.Printf("%s account created with username %q", sp.display, username)
		}
	}
}

const pwAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomPassword() string {
	b := make([]byte, 14)
	if _, err := cryptorand.Read(b); err != nil {
		return "Change-me-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	for i := range b {
		b[i] = pwAlphabet[int(b[i])%len(pwAlphabet)]
	}
	return string(b)
}

func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 5
	}
	return n
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status >= 400 || strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
