package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Core keys map onto dedicated columns. They can be relabelled but never removed,
// because the login credentials and the Excel sheet depend on them.
var coreFieldKeys = map[string]bool{
	"name": true, "cpr": true, "dob": true, "phone": true, "email": true,
	"house": true, "road": true, "block": true,
	"volunteer": true, "volunteer_field": true,
	"affiliated": true, "affiliation": true,
}

// Locked keys additionally cannot be disabled: they are the login credentials.
var lockedFieldKeys = map[string]bool{
	"name": true, "cpr": true, "dob": true, "phone": true,
}

var defaultFields = []FormField{
	{Key: "name", Label: "الاسم الرباعي", Type: "arabic_name", Required: true,
		HelpText: "يكتب الاسم باللغة العربية فقط، رباعياً كما في البطاقة الذكية", Placeholder: "الاسم الأول واسم الأب والجد واللقب", SortOrder: 10},
	{Key: "cpr", Label: "الرقم الشخصي", Type: "cpr", Required: true,
		HelpText: "تسعة أرقام كما في البطاقة الذكية", Placeholder: "000000000", SortOrder: 20},
	{Key: "dob", Label: "تاريخ الميلاد", Type: "date", Required: true,
		HelpText: "بصيغة يوم-شهر-سنة", SortOrder: 30},
	{Key: "phone", Label: "رقم الواتساب", Type: "phone", Required: true,
		HelpText: "ثمانية أرقام بدون رمز الدولة", Placeholder: "00000000", SortOrder: 40},
	{Key: "email", Label: "البريد الإلكتروني", Type: "email", Required: false,
		HelpText: "اختياري", SortOrder: 50},
	{Key: "house", Label: "المنزل", Type: "text", Required: true, SortOrder: 60},
	{Key: "road", Label: "الطريق", Type: "text", Required: true, SortOrder: 70},
	{Key: "block", Label: "المجمع", Type: "text", Required: true, SortOrder: 80},
	{Key: "volunteer", Label: "هل سبق وإن عملت في مجال العمل التطوعي ؟", Type: "yesno", Required: true, SortOrder: 90},
	{Key: "volunteer_field", Label: "المجال", Type: "text", Required: true, SortOrder: 100,
		DependsOn: "volunteer", DependsVal: "yes"},
	{Key: "affiliated", Label: "هل أنت منتسب لمؤسسة أخرى؟", Type: "yesno", Required: true, SortOrder: 110},
	{Key: "affiliation", Label: "المؤسسة", Type: "text", Required: true, SortOrder: 120,
		DependsOn: "affiliated", DependsVal: "yes"},
}

func (s *Store) installDefaultFields() error {
	for _, f := range defaultFields {
		_, err := s.DB.Exec(`
			INSERT OR IGNORE INTO form_fields
			  (field_key, label, help_text, placeholder, type, required, enabled, is_core, locked, options, depends_on, depends_val, sort_order)
			VALUES (?,?,?,?,?,?,1,1,?,'',?,?,?)`,
			f.Key, f.Label, f.HelpText, f.Placeholder, f.Type, boolInt(f.Required),
			boolInt(lockedFieldKeys[f.Key]), f.DependsOn, f.DependsVal, f.SortOrder)
		if err != nil {
			return fmt.Errorf("seed field %s: %w", f.Key, err)
		}
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Fields returns the form definition ordered as it appears on the page.
// When onlyEnabled is true, hidden fields are omitted (public form).
func (s *Store) Fields(onlyEnabled bool) ([]FormField, error) {
	q := `SELECT id, field_key, label, help_text, placeholder, type, required, enabled,
	             is_core, locked, options, depends_on, depends_val, sort_order
	      FROM form_fields`
	if onlyEnabled {
		q += ` WHERE enabled = 1`
	}
	q += ` ORDER BY sort_order, id`
	rows, err := s.DB.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FormField{}
	for rows.Next() {
		var f FormField
		var req, en, core, locked int
		if err := rows.Scan(&f.ID, &f.Key, &f.Label, &f.HelpText, &f.Placeholder, &f.Type,
			&req, &en, &core, &locked, &f.Options, &f.DependsOn, &f.DependsVal, &f.SortOrder); err != nil {
			return nil, err
		}
		f.Required, f.Enabled, f.IsCore, f.Locked = req == 1, en == 1, core == 1, locked == 1
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) FieldByKey(key string) (FormField, error) {
	fields, err := s.Fields(false)
	if err != nil {
		return FormField{}, err
	}
	for _, f := range fields {
		if f.Key == key {
			return f, nil
		}
	}
	return FormField{}, sql.ErrNoRows
}

var validFieldTypes = map[string]bool{
	"text": true, "textarea": true, "number": true, "date": true, "email": true,
	"phone": true, "cpr": true, "arabic_name": true, "yesno": true, "select": true,
}

// UpdateField applies an admin edit. Structural properties of core fields
// (their key and type) are never modified.
func (s *Store) UpdateField(f FormField) error {
	existing, err := s.FieldByKey(f.Key)
	if err != nil {
		return err
	}
	if existing.Locked {
		f.Enabled = true
		f.Required = true
	}
	if existing.IsCore {
		f.Type = existing.Type
		f.DependsOn = existing.DependsOn
		f.DependsVal = existing.DependsVal
	}
	if !validFieldTypes[f.Type] {
		return errors.New("نوع الحقل غير معروف")
	}
	if strings.TrimSpace(f.Label) == "" {
		return errors.New("عنوان الحقل مطلوب")
	}
	_, err = s.DB.Exec(`
		UPDATE form_fields
		SET label=?, help_text=?, placeholder=?, type=?, required=?, enabled=?,
		    options=?, depends_on=?, depends_val=?, sort_order=?
		WHERE field_key=?`,
		strings.TrimSpace(f.Label), f.HelpText, f.Placeholder, f.Type,
		boolInt(f.Required), boolInt(f.Enabled), f.Options,
		f.DependsOn, f.DependsVal, f.SortOrder, f.Key)
	return err
}

// AddField creates a custom field. Its answers live in the `extra` JSON column.
func (s *Store) AddField(f FormField) (FormField, error) {
	key := sanitizeKey(f.Key)
	if key == "" {
		return FormField{}, errors.New("معرف الحقل غير صالح")
	}
	if coreFieldKeys[key] {
		return FormField{}, errors.New("هذا المعرف محجوز لحقل أساسي")
	}
	if strings.TrimSpace(f.Label) == "" {
		return FormField{}, errors.New("عنوان الحقل مطلوب")
	}
	if !validFieldTypes[f.Type] {
		return FormField{}, errors.New("نوع الحقل غير معروف")
	}
	var maxOrder int
	_ = s.DB.QueryRow(`SELECT COALESCE(MAX(sort_order), 0) FROM form_fields`).Scan(&maxOrder)
	res, err := s.DB.Exec(`
		INSERT INTO form_fields
		  (field_key, label, help_text, placeholder, type, required, enabled, is_core, locked, options, depends_on, depends_val, sort_order)
		VALUES (?,?,?,?,?,?,?,0,0,?,?,?,?)`,
		key, strings.TrimSpace(f.Label), f.HelpText, f.Placeholder, f.Type,
		boolInt(f.Required), boolInt(f.Enabled), f.Options, f.DependsOn, f.DependsVal, maxOrder+10)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return FormField{}, errors.New("يوجد حقل آخر بنفس المعرف")
		}
		return FormField{}, err
	}
	id, _ := res.LastInsertId()
	f.ID, f.Key, f.SortOrder = id, key, maxOrder+10
	return f, nil
}

func (s *Store) DeleteField(key string) error {
	if coreFieldKeys[key] {
		return errors.New("لا يمكن حذف الحقول الأساسية")
	}
	_, err := s.DB.Exec(`DELETE FROM form_fields WHERE field_key = ? AND is_core = 0`, key)
	return err
}

func (s *Store) ReorderFields(keys []string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, k := range keys {
		if _, err := tx.Exec(`UPDATE form_fields SET sort_order=? WHERE field_key=?`, (i+1)*10, k); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// sanitizeKey keeps custom field keys safe for JSON and Excel headers.
func sanitizeKey(in string) string {
	in = strings.TrimSpace(strings.ToLower(in))
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-' || r == ' ':
			b.WriteRune('_')
		}
	}
	k := strings.Trim(b.String(), "_")
	if len(k) > 40 {
		k = k[:40]
	}
	return k
}

// CustomFields returns only the admin-added fields, in display order.
func (s *Store) CustomFields() ([]FormField, error) {
	all, err := s.Fields(false)
	if err != nil {
		return nil, err
	}
	out := []FormField{}
	for _, f := range all {
		if !f.IsCore {
			out = append(out, f)
		}
	}
	return out, nil
}
