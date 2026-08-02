package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// Store wraps the SQLite database used by the whole application.
type Store struct {
	DB *sql.DB
}

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS admins (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	username      TEXT NOT NULL,
	display_name  TEXT NOT NULL DEFAULT '',
	password_hash TEXT NOT NULL,
	kind          TEXT NOT NULL CHECK (kind IN ('membership','elections')),
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL,
	UNIQUE (username, kind)
);

-- Editable definition of the public registration form.
CREATE TABLE IF NOT EXISTS form_fields (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	field_key    TEXT NOT NULL UNIQUE,
	label        TEXT NOT NULL,
	help_text    TEXT NOT NULL DEFAULT '',
	placeholder  TEXT NOT NULL DEFAULT '',
	type         TEXT NOT NULL,
	required     INTEGER NOT NULL DEFAULT 0,
	enabled      INTEGER NOT NULL DEFAULT 1,
	is_core      INTEGER NOT NULL DEFAULT 0,
	locked       INTEGER NOT NULL DEFAULT 0,
	options      TEXT NOT NULL DEFAULT '',
	depends_on   TEXT NOT NULL DEFAULT '',
	depends_val  TEXT NOT NULL DEFAULT '',
	sort_order   INTEGER NOT NULL DEFAULT 0
);

-- One row per membership request submitted through the public form.
CREATE TABLE IF NOT EXISTS applications (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	name             TEXT NOT NULL,
	cpr              TEXT NOT NULL,
	dob              TEXT NOT NULL,
	phone            TEXT NOT NULL,
	email            TEXT NOT NULL DEFAULT '',
	house            TEXT NOT NULL DEFAULT '',
	road             TEXT NOT NULL DEFAULT '',
	block            TEXT NOT NULL DEFAULT '',
	volunteer        INTEGER NOT NULL DEFAULT 0,
	volunteer_field  TEXT NOT NULL DEFAULT '',
	affiliated       INTEGER NOT NULL DEFAULT 0,
	affiliation      TEXT NOT NULL DEFAULT '',
	extra            TEXT NOT NULL DEFAULT '{}',
	status           TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','suspended','cancelled')),
	reject_reason    TEXT NOT NULL DEFAULT '',
	created_at       TEXT NOT NULL,
	decided_at       TEXT NOT NULL DEFAULT '',
	decided_by       TEXT NOT NULL DEFAULT '',
	meeting_no       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_applications_status ON applications(status);
CREATE INDEX IF NOT EXISTS idx_applications_cpr    ON applications(cpr);
CREATE INDEX IF NOT EXISTS idx_applications_phone  ON applications(phone);

-- Approved members. These rows are also the login credentials.
CREATE TABLE IF NOT EXISTS users (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	application_id   INTEGER,
	name             TEXT NOT NULL,
	cpr              TEXT NOT NULL UNIQUE,
	dob              TEXT NOT NULL,
	phone            TEXT NOT NULL,
	email            TEXT NOT NULL DEFAULT '',
	house            TEXT NOT NULL DEFAULT '',
	road             TEXT NOT NULL DEFAULT '',
	block            TEXT NOT NULL DEFAULT '',
	volunteer        INTEGER NOT NULL DEFAULT 0,
	volunteer_field  TEXT NOT NULL DEFAULT '',
	affiliated       INTEGER NOT NULL DEFAULT 0,
	affiliation      TEXT NOT NULL DEFAULT '',
	extra            TEXT NOT NULL DEFAULT '{}',
	meeting_no       TEXT NOT NULL DEFAULT '',
	status           TEXT NOT NULL DEFAULT 'active',
	status_note      TEXT NOT NULL DEFAULT '',
	status_at        TEXT NOT NULL DEFAULT '',
	source           TEXT NOT NULL DEFAULT 'form',
	created_at       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_users_phone ON users(phone);

CREATE TABLE IF NOT EXISTS sessions (
	token        TEXT PRIMARY KEY,
	subject_kind TEXT NOT NULL,
	subject_id   INTEGER NOT NULL,
	created_at   TEXT NOT NULL,
	expires_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_expiry ON sessions(expires_at);

-- Election positions. In "single" mode exactly one implicit position is used.
CREATE TABLE IF NOT EXISTS roles (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	name              TEXT NOT NULL,
	description       TEXT NOT NULL DEFAULT '',
	winners_count     INTEGER NOT NULL DEFAULT 1,
	selections_allowed INTEGER NOT NULL DEFAULT 1,
	sort_order        INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS participants (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	role_id     INTEGER REFERENCES roles(id) ON DELETE CASCADE,
	name        TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	photo       TEXT NOT NULL DEFAULT '',
	sort_order  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_participants_role ON participants(role_id);

CREATE TABLE IF NOT EXISTS ballots (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id    INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS votes (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	ballot_id      INTEGER NOT NULL REFERENCES ballots(id) ON DELETE CASCADE,
	user_id        INTEGER NOT NULL,
	role_id        INTEGER,
	participant_id INTEGER NOT NULL REFERENCES participants(id) ON DELETE CASCADE,
	created_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_votes_participant ON votes(participant_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_votes_unique ON votes(user_id, participant_id);

CREATE TABLE IF NOT EXISTS audit_log (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	actor      TEXT NOT NULL,
	action     TEXT NOT NULL,
	details    TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);
`

// Open prepares the database file, applies the schema and installs defaults.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite writes are serialised; a single connection avoids "database is locked".
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := s.installDefaults(); err != nil {
		return nil, err
	}
	return s, nil
}

// migrate adds columns introduced after a database was first created. CREATE
// TABLE IF NOT EXISTS leaves existing tables untouched, so new columns have to
// be added explicitly for installations that are already running.
func (s *Store) migrate() error {
	additions := []struct{ table, column, definition string }{
		{"users", "meeting_no", "TEXT NOT NULL DEFAULT ''"},
		{"applications", "meeting_no", "TEXT NOT NULL DEFAULT ''"},
		{"users", "status", "TEXT NOT NULL DEFAULT 'active'"},
		{"users", "status_note", "TEXT NOT NULL DEFAULT ''"},
		{"users", "status_at", "TEXT NOT NULL DEFAULT ''"},
	}
	if err := s.widenApplicationStatus(); err != nil {
		return err
	}
	for _, a := range additions {
		has, err := s.hasColumn(a.table, a.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := s.DB.Exec("ALTER TABLE " + a.table + " ADD COLUMN " + a.column + " " + a.definition); err != nil {
			return err
		}
	}
	return nil
}

// widenApplicationStatus rebuilds the applications table when it still carries
// the original three-value CHECK constraint. SQLite has no way to alter a
// constraint in place, so the table is copied into a new one. Done inside a
// transaction with foreign keys off, so a failure leaves the original intact.
func (s *Store) widenApplicationStatus() error {
	var ddl string
	err := s.DB.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='applications'`).Scan(&ddl)
	if err != nil {
		return nil // table not created yet; the schema above is already current
	}
	if strings.Contains(ddl, "'suspended'") {
		return nil // already widened
	}

	if _, err := s.DB.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer s.DB.Exec(`PRAGMA foreign_keys = ON`)

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	steps := []string{
		`CREATE TABLE applications_new (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			name             TEXT NOT NULL,
			cpr              TEXT NOT NULL,
			dob              TEXT NOT NULL,
			phone            TEXT NOT NULL,
			email            TEXT NOT NULL DEFAULT '',
			house            TEXT NOT NULL DEFAULT '',
			road             TEXT NOT NULL DEFAULT '',
			block            TEXT NOT NULL DEFAULT '',
			volunteer        INTEGER NOT NULL DEFAULT 0,
			volunteer_field  TEXT NOT NULL DEFAULT '',
			affiliated       INTEGER NOT NULL DEFAULT 0,
			affiliation      TEXT NOT NULL DEFAULT '',
			extra            TEXT NOT NULL DEFAULT '{}',
			status           TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','suspended','cancelled')),
			reject_reason    TEXT NOT NULL DEFAULT '',
			created_at       TEXT NOT NULL,
			decided_at       TEXT NOT NULL DEFAULT '',
			decided_by       TEXT NOT NULL DEFAULT '',
			meeting_no       TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO applications_new
		 SELECT id, name, cpr, dob, phone, email, house, road, block,
		        volunteer, volunteer_field, affiliated, affiliation, extra,
		        status, reject_reason, created_at, decided_at, decided_by, meeting_no
		 FROM applications`,
		`DROP TABLE applications`,
		`ALTER TABLE applications_new RENAME TO applications`,
		`CREATE INDEX IF NOT EXISTS idx_applications_status ON applications(status)`,
		`CREATE INDEX IF NOT EXISTS idx_applications_cpr    ON applications(cpr)`,
		`CREATE INDEX IF NOT EXISTS idx_applications_phone  ON applications(phone)`,
	}
	for _, q := range steps {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.DB.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) Close() error { return s.DB.Close() }

// ---------------------------------------------------------------- settings

func (s *Store) Setting(key, fallback string) string {
	var v string
	err := s.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return fallback
	}
	return v
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.DB.Exec(
		`INSERT INTO settings(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) SettingBool(key string, fallback bool) bool {
	v := s.Setting(key, "")
	if v == "" {
		return fallback
	}
	return v == "1" || strings.EqualFold(v, "true")
}

func (s *Store) AllSettings() (map[string]string, error) {
	rows, err := s.DB.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) setDefault(key, value string) error {
	_, err := s.DB.Exec(`INSERT OR IGNORE INTO settings(key, value) VALUES(?, ?)`, key, value)
	return err
}

// Terms text as supplied by the institution, with pictograms removed.
const DefaultTerms = `تعلن إدارة مأتم الإمام الرضا عليه السلام عن بدء العمل على تأسيس الجمعية العمومية للمأتم لما لها من فائدة في تطوير هذا الصرح الإسلامي.

امتيازات عضو الجمعية العمومية:
١- يحق للعضو الترشح لمجلس إدارة المأتم.
٢- يحق للعضو التصويت في انتخابات المأتم.
٣- يحق للعضو الاطلاع على التقرير المالي والإداري للدورة المنتهية ومناقشته.

شروط التقدم للحصول على العضوية:
١- أن يكون مسلماً عاقلاً بالغاً.
٢- أن يكون من أهل أو سكنة قرية المالكية أو أحد العاملين في لجان المأتم والمشهود لهم بذلك.
٣- أن يكون حسن السمعة والسيرة والسلوك.

مدة العضوية:
تبلغ مدة صلاحية العضوية في الجمعية العمومية مدة دورة انتخابية كاملة (ثلاث سنوات) يتم تجديدها حسب رغبة العضو.

ملاحظة: لا توجد رسوم مالية للانضمام للجمعية العمومية.`

func (s *Store) installDefaults() error {
	defaults := map[string]string{
		"org_name":                    "مأتم الرضا عليه السلام - المالكية",
		"form_title":                  "استمارة طلب العضوية بالجمعية العمومية لمأتم الرضا عليه السلام - المالكية",
		"form_intro":                  "يرجى تعبئة البيانات التالية بدقة. ستتم مراجعة الطلب من قبل إدارة المأتم قبل اعتماد العضوية.",
		"form_terms":                  DefaultTerms,
		"form_agree_text":             "أقر أنا المتقدم لطلب العضوية بمأتم الرضا عليه السلام بأن جميع البيانات أعلاه صحيحة وإنني قد أطلعت على شروط العضوية",
		"form_success_message":        "لقد تم ارسال طلب التقديم بنجاح",
		"registration_open":           "1",
		"registration_close_at":       "",
		"registration_closed_message": "باب التسجيل في الجمعية العمومية مغلق حالياً.",
		"suspended_message":           "يرجى مراجعة المأتم",
		"user_waiting_message":        "لا يوجد شيء حتى الآن، بانتظار الاعلان من لجنة الانتخابات",
		"voting_mode":                 "roles",
		"voting_status":               "draft",
		"voting_title":                "انتخابات مجلس إدارة مأتم الرضا عليه السلام",
		"voting_announcement":         "",
		"voting_closed_message":       "انتهت فترة التصويت. شكراً لمشاركتكم.",
		"single_winners_count":        "1",
		"single_selections":           "1",
		"footer_author":               "محمود حميد بوراشد - Mahmood Hameed Burashid",
		"footer_instagram":            "@m7burashid",
	}
	for k, v := range defaults {
		if err := s.setDefault(k, v); err != nil {
			return err
		}
	}
	return s.installDefaultFields()
}

// ---------------------------------------------------------------- audit

func (s *Store) Audit(actor, action, details string) {
	_, _ = s.DB.Exec(
		`INSERT INTO audit_log(actor, action, details, created_at) VALUES(?,?,?,?)`,
		actor, action, details, Now())
}

func (s *Store) RecentAudit(limit int) ([]AuditEntry, error) {
	rows, err := s.DB.Query(
		`SELECT id, actor, action, details, created_at FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var a AuditEntry
		if err := rows.Scan(&a.ID, &a.Actor, &a.Action, &a.Details, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- helpers

func decodeExtra(raw string) map[string]string {
	out := map[string]string{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func encodeExtra(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}
