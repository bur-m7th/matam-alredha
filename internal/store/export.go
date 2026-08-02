package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"matam-alredha/internal/xlsxw"
)

// The first eight columns match the sheet supplied by the institution, in the
// same order, so the exported file is a drop-in replacement for it.
var baseColumns = []xlsxw.Column{
	{Header: "NO", Width: 6, Numeric: true},
	{Header: "الاسم الرباعي", Width: 34},
	{Header: "الرقم الشخصي", Width: 16},
	{Header: "تاريخ الميلاد", Width: 14},
	{Header: "رقم التواصل", Width: 14},
	{Header: "المنزل", Width: 10},
	{Header: "طريق", Width: 10},
	{Header: "المجمع", Width: 10},
}

var extendedColumns = []xlsxw.Column{
	{Header: "البريد الإلكتروني", Width: 26},
	{Header: "العمل التطوعي", Width: 12},
	{Header: "المجال", Width: 20},
	{Header: "منتسب لمؤسسة أخرى", Width: 16},
	{Header: "المؤسسة", Width: 20},
	{Header: "تاريخ الاعتماد", Width: 14},
	{Header: "حالة العضوية", Width: 14},
	{Header: "ملاحظة الحالة", Width: 28},
}

func yesNo(b bool) string {
	if b {
		return "نعم"
	}
	return "لا"
}

// MembersSheet builds the workbook contents from the current members table.
// Because it is always rebuilt from the database, deleting a member removes the
// row from the spreadsheet as well.
func (s *Store) MembersSheet() (xlsxw.Sheet, error) {
	users, err := s.Users("")
	if err != nil {
		return xlsxw.Sheet{}, err
	}
	custom, err := s.CustomFields()
	if err != nil {
		return xlsxw.Sheet{}, err
	}

	cols := append([]xlsxw.Column{}, baseColumns...)
	cols = append(cols, extendedColumns...)
	for _, f := range custom {
		cols = append(cols, xlsxw.Column{Header: f.Label, Width: 20})
	}

	rows := make([][]string, 0, len(users))
	for i, u := range users {
		row := []string{
			strconv.Itoa(i + 1),
			u.Name,
			u.CPR, // written as text, so leading zeros survive
			FormatDOB(u.DOB),
			u.Phone,
			u.House,
			u.Road,
			u.Block,
			u.Email,
			yesNo(u.Volunteer),
			u.VolunteerField,
			yesNo(u.Affiliated),
			u.Affiliation,
			shortDate(u.CreatedAt),
			MemberStatusLabel(u.Status),
			u.StatusNote,
		}
		for _, f := range custom {
			row = append(row, u.Extra[f.Key])
		}
		rows = append(rows, row)
	}

	return xlsxw.Sheet{
		Name:    "أعضاء الجمعية العمومية",
		RTL:     true,
		Columns: cols,
		Rows:    rows,
	}, nil
}

func shortDate(rfc string) string {
	t, ok := ParseTime(rfc)
	if !ok {
		return ""
	}
	return t.Format("02-01-2006")
}

// ExportBytes renders the current members list as a .xlsx file.
func (s *Store) ExportBytes() ([]byte, error) {
	sheet, err := s.MembersSheet()
	if err != nil {
		return nil, err
	}
	return xlsxw.Bytes(sheet)
}

// SyncExportFile rewrites the master workbook on disk. It runs after every
// approval, member edit and deletion so the file on the server always mirrors
// the database.
func (s *Store) SyncExportFile(path string) error {
	data, err := s.ExportBytes()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace export file: %w", err)
	}
	return nil
}
