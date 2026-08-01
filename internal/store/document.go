package store

import (
	"strings"
	"time"
)

// Status values that appear in a printed form.
const (
	PrintStatusApproved = "مقبول"
	PrintStatusRejected = "غير مقبول"
	PrintStatusPending  = "قيد المراجعة"
)

// DocumentValues builds the map handed to the Word template filler. Keys match
// the placeholder names an operator writes as {{key}}.
//
// status is one of the PrintStatus constants. The three checkbox keys let a
// template show a tick box row instead of a word, which is how the paper form
// is normally laid out.
func (s *Store) DocumentValues(p Person, status, decidedAt string, no int, meetingNo string) map[string]string {
	now := time.Now().In(Bahrain)

	v := map[string]string{
		"name":            p.Name,
		"cpr":             p.CPR,
		"dob":             FormatDOB(p.DOB),
		"phone":           p.Phone,
		"email":           p.Email,
		"house":           p.House,
		"road":            p.Road,
		"block":           p.Block,
		"address":         addressLine(p),
		"volunteer":       yesNo(p.Volunteer),
		"volunteer_field": p.VolunteerField,
		"affiliated":      yesNo(p.Affiliated),
		"affiliation":     p.Affiliation,

		// Tick-box pairs, so a template can reproduce the paper form's layout
		// instead of printing the word نعم or لا.
		"volunteer_yes":  checkbox(p.Volunteer),
		"volunteer_no":   checkbox(!p.Volunteer),
		"affiliated_yes": checkbox(p.Affiliated),
		"affiliated_no":  checkbox(!p.Affiliated),

		"status":          status,
		"status_accepted": checkbox(status == PrintStatusApproved),
		"status_rejected": checkbox(status == PrintStatusRejected),
		"status_pending":  checkbox(status == PrintStatusPending),

		"meeting_no": meetingNo,
		"secretary":  s.Setting("doc_secretary", ""),
		"chairman":   s.Setting("doc_chairman", ""),

		"org_name":   s.Setting("org_name", ""),
		"form_title": s.Setting("form_title", ""),
		"today":      formatArabicDate(now),
		"today_num":  now.Format("02-01-2006"),
		"no":         "",
	}

	if no > 0 {
		v["no"] = itoa(no)
		v["membership_no"] = itoa(no)
	} else {
		v["membership_no"] = ""
	}
	if t, okTime := ParseTime(decidedAt); okTime {
		v["decision_date"] = formatArabicDate(t.In(Bahrain))
		v["decision_date_num"] = t.In(Bahrain).Format("02-01-2006")
	} else {
		v["decision_date"] = ""
		v["decision_date_num"] = ""
	}

	// Admin-added fields are addressable by their own key.
	for k, val := range p.Extra {
		if _, taken := v[k]; !taken {
			v[k] = val
		}
	}
	// Any custom field the person left blank should still resolve, so the
	// placeholder does not survive into the printed form.
	if custom, err := s.CustomFields(); err == nil {
		for _, f := range custom {
			if _, present := v[f.Key]; !present {
				v[f.Key] = ""
			}
		}
	}
	return v
}

// DocumentKeys lists every placeholder the system can supply, for the reference
// table shown in the dashboard.
func (s *Store) DocumentKeys() []string {
	keys := []string{
		"name", "cpr", "dob", "phone", "email",
		"house", "road", "block", "address",
		"volunteer", "volunteer_yes", "volunteer_no", "volunteer_field",
		"affiliated", "affiliated_yes", "affiliated_no", "affiliation",
		"status", "status_accepted", "status_rejected", "status_pending",
		"decision_date", "decision_date_num", "meeting_no", "membership_no",
		"secretary", "chairman",
		"org_name", "form_title", "today", "today_num", "no",
	}
	if custom, err := s.CustomFields(); err == nil {
		for _, f := range custom {
			keys = append(keys, f.Key)
		}
	}
	return keys
}

func addressLine(p Person) string {
	parts := []string{}
	if p.House != "" {
		parts = append(parts, "منزل "+p.House)
	}
	if p.Road != "" {
		parts = append(parts, "طريق "+p.Road)
	}
	if p.Block != "" {
		parts = append(parts, "مجمع "+p.Block)
	}
	return strings.Join(parts, " - ")
}

// checkbox renders a tick or an empty box, so a template can lay the decision
// out the way the paper form does.
func checkbox(on bool) string {
	if on {
		return "☑"
	}
	return "☐"
}

var arabicMonths = [...]string{
	"يناير", "فبراير", "مارس", "أبريل", "مايو", "يونيو",
	"يوليو", "أغسطس", "سبتمبر", "أكتوبر", "نوفمبر", "ديسمبر",
}

func formatArabicDate(t time.Time) string {
	return itoa(t.Day()) + " " + arabicMonths[int(t.Month())-1] + " " + itoa(t.Year())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
