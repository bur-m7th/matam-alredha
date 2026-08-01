package store

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// NormalizeDigits converts Arabic-Indic and Eastern Arabic-Indic digits to ASCII
// so that a number typed on an Arabic keyboard validates the same way.
func NormalizeDigits(in string) string {
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= '\u0660' && r <= '\u0669': // ٠-٩
			b.WriteRune('0' + (r - '\u0660'))
		case r >= '\u06F0' && r <= '\u06F9': // ۰-۹
			b.WriteRune('0' + (r - '\u06F0'))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

var nonDigit = regexp.MustCompile(`\D`)

// DigitsOnly strips everything that is not a digit, after normalising numerals.
func DigitsOnly(in string) string {
	return nonDigit.ReplaceAllString(NormalizeDigits(in), "")
}

// NormalizeCPR pads a shortened personal number back to nine digits. Spreadsheet
// software drops leading zeros, which is why so many stored values are short.
func NormalizeCPR(in string) string {
	d := DigitsOnly(in)
	if d == "" {
		return ""
	}
	if len(d) < 9 {
		d = strings.Repeat("0", 9-len(d)) + d
	}
	return d
}

func ValidateCPR(in string) (string, error) {
	d := DigitsOnly(in)
	if d == "" {
		return "", errors.New("الرقم الشخصي مطلوب")
	}
	if len(d) > 9 {
		return "", errors.New("الرقم الشخصي يجب أن يتكون من تسعة أرقام")
	}
	return NormalizeCPR(d), nil
}

func ValidatePhone(in string) (string, error) {
	d := DigitsOnly(in)
	d = strings.TrimPrefix(d, "973")
	if len(d) != 8 {
		return "", errors.New("رقم التواصل يجب أن يتكون من ثمانية أرقام")
	}
	return d, nil
}

// isArabicLetter accepts the Arabic block plus the marks used in vowelled names.
func isArabicLetter(r rune) bool {
	if r >= 0x0621 && r <= 0x064A {
		return true
	}
	if r >= 0x0671 && r <= 0x06D3 {
		return true
	}
	if r >= 0x064B && r <= 0x0652 { // harakat
		return true
	}
	if r == 0x0640 { // tatweel
		return true
	}
	return false
}

// ValidateArabicName enforces an Arabic-only name of at least four parts.
func ValidateArabicName(in string) (string, error) {
	name := strings.Join(strings.Fields(strings.TrimSpace(in)), " ")
	if name == "" {
		return "", errors.New("الاسم مطلوب")
	}
	for _, r := range name {
		if r == ' ' || unicode.Is(unicode.Arabic, r) && isArabicLetter(r) {
			continue
		}
		if isArabicLetter(r) {
			continue
		}
		return "", errors.New("يجب كتابة الاسم باللغة العربية فقط")
	}
	if len(strings.Fields(name)) < 4 {
		return "", errors.New("يجب إدخال الاسم رباعياً")
	}
	if len([]rune(name)) > 120 {
		return "", errors.New("الاسم طويل جداً")
	}
	return name, nil
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s.]+\.[^@\s]+$`)

func ValidateEmail(in string) (string, error) {
	e := strings.TrimSpace(in)
	if e == "" {
		return "", nil
	}
	if !emailRe.MatchString(e) || len(e) > 160 {
		return "", errors.New("صيغة البريد الإلكتروني غير صحيحة")
	}
	return e, nil
}

// ValidateDOB accepts DD-MM-YYYY or YYYY-MM-DD and stores ISO YYYY-MM-DD.
func ValidateDOB(in string) (string, error) {
	v := strings.TrimSpace(NormalizeDigits(in))
	v = strings.ReplaceAll(v, "/", "-")
	if v == "" {
		return "", errors.New("تاريخ الميلاد مطلوب")
	}
	var t time.Time
	var err error
	for _, layout := range []string{"2006-01-02", "02-01-2006", "2-1-2006"} {
		t, err = time.Parse(layout, v)
		if err == nil {
			break
		}
	}
	if err != nil {
		return "", errors.New("صيغة تاريخ الميلاد غير صحيحة")
	}
	now := time.Now().In(Bahrain)
	if t.After(now) {
		return "", errors.New("تاريخ الميلاد غير صحيح")
	}
	if t.Year() < 1900 {
		return "", errors.New("تاريخ الميلاد غير صحيح")
	}
	if age(t, now) < 18 {
		return "", errors.New("يشترط أن يكون المتقدم بالغاً")
	}
	return t.Format("2006-01-02"), nil
}

func age(birth, at time.Time) int {
	y := at.Year() - birth.Year()
	if at.YearDay() < birth.YearDay() {
		y--
	}
	return y
}

// FormatDOB renders an ISO date for display and for the Excel sheet.
func FormatDOB(iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return t.Format("02-01-2006")
}

// CleanText trims and collapses whitespace, and caps the length.
func CleanText(in string, max int) string {
	v := strings.Join(strings.Fields(strings.TrimSpace(in)), " ")
	r := []rune(v)
	if max > 0 && len(r) > max {
		v = string(r[:max])
	}
	return v
}

// CleanMultiline preserves line breaks but trims trailing spaces on each line.
func CleanMultiline(in string, max int) string {
	lines := strings.Split(strings.ReplaceAll(in, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	v := strings.Trim(strings.Join(lines, "\n"), "\n ")
	r := []rune(v)
	if max > 0 && len(r) > max {
		v = string(r[:max])
	}
	return v
}
