package store

import "time"

// Bahrain has a fixed UTC+3 offset with no daylight saving.
var Bahrain = time.FixedZone("AST", 3*60*60)

func Now() string { return time.Now().In(Bahrain).Format(time.RFC3339) }

func ParseTime(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Admin is a dashboard operator. Kind is either "membership" or "elections";
// the two kinds never share routes, sessions or data.
type Admin struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"`
	CreatedAt   string `json:"created_at"`
}

type FormField struct {
	ID          int64  `json:"id"`
	Key         string `json:"key"`
	Label       string `json:"label"`
	HelpText    string `json:"help_text"`
	Placeholder string `json:"placeholder"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Enabled     bool   `json:"enabled"`
	IsCore      bool   `json:"is_core"`
	Locked      bool   `json:"locked"`
	Options     string `json:"options"`
	DependsOn   string `json:"depends_on"`
	DependsVal  string `json:"depends_val"`
	SortOrder   int    `json:"sort_order"`
}

// Person carries the fields shared by an application and an approved member.
type Person struct {
	Name           string            `json:"name"`
	CPR            string            `json:"cpr"`
	DOB            string            `json:"dob"`
	Phone          string            `json:"phone"`
	Email          string            `json:"email"`
	House          string            `json:"house"`
	Road           string            `json:"road"`
	Block          string            `json:"block"`
	Volunteer      bool              `json:"volunteer"`
	VolunteerField string            `json:"volunteer_field"`
	Affiliated     bool              `json:"affiliated"`
	Affiliation    string            `json:"affiliation"`
	Extra          map[string]string `json:"extra"`
}

type Application struct {
	Person
	ID           int64  `json:"id"`
	MeetingNo    string `json:"meeting_no"`
	Status       string `json:"status"`
	RejectReason string `json:"reject_reason"`
	CreatedAt    string `json:"created_at"`
	DecidedAt    string `json:"decided_at"`
	DecidedBy    string `json:"decided_by"`
}

type User struct {
	Person
	ID            int64  `json:"id"`
	MeetingNo     string `json:"meeting_no"`
	Status        string `json:"status"`
	StatusNote    string `json:"status_note"`
	StatusAt      string `json:"status_at"`
	ApplicationID int64  `json:"application_id"`
	Source        string `json:"source"`
	CreatedAt     string `json:"created_at"`
	HasVoted      bool   `json:"has_voted"`
}

type Role struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	WinnersCount      int    `json:"winners_count"`
	SelectionsAllowed int    `json:"selections_allowed"`
	SortOrder         int    `json:"sort_order"`
}

type Participant struct {
	ID          int64  `json:"id"`
	RoleID      int64  `json:"role_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Photo       string `json:"photo"`
	Thumb       string `json:"thumb,omitempty"`
	CandidacyID int64  `json:"candidacy_id,omitempty"`
	SortOrder   int    `json:"sort_order"`
	Votes       int    `json:"votes,omitempty"`
	Share       string `json:"share,omitempty"`
}

type AuditEntry struct {
	ID        int64  `json:"id"`
	Actor     string `json:"actor"`
	Action    string `json:"action"`
	Details   string `json:"details"`
	CreatedAt string `json:"created_at"`
}

type Session struct {
	Token       string
	SubjectKind string
	SubjectID   int64
	ExpiresAt   time.Time
}
