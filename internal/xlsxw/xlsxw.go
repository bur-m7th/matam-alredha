// Package xlsxw writes a single-sheet .xlsx file using only the standard
// library.
//
// Every data cell is written as an inline string carried by a cell style whose
// number format is "@" (text). That combination is what keeps a personal number
// such as 000304450 intact: Excel neither reparses the value as a number nor
// strips its leading zeros, on opening or on re-saving.
package xlsxw

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// Column describes one spreadsheet column.
type Column struct {
	Header string
	Width  float64
	// Numeric renders the value as a real number (used for the row counter).
	Numeric bool
}

// Sheet is a table ready to be written.
type Sheet struct {
	Name    string
	RTL     bool
	Columns []Column
	Rows    [][]string
}

// Style slots defined in styles.xml.
const (
	styleDefault = 0
	styleHeader  = 1
	styleText    = 2
	styleNumber  = 3
)

// Write streams the workbook to w.
func Write(w io.Writer, sh Sheet) error {
	if sh.Name == "" {
		sh.Name = "Sheet1"
	}
	zw := zip.NewWriter(w)
	files := []struct {
		name string
		body string
	}{
		{"[Content_Types].xml", contentTypes},
		{"_rels/.rels", rootRels},
		{"xl/workbook.xml", workbookXML(sh.Name)},
		{"xl/_rels/workbook.xml.rels", workbookRels},
		{"xl/styles.xml", stylesXML},
		{"xl/worksheets/sheet1.xml", sheetXML(sh)},
	}
	for _, f := range files {
		fw, err := zw.Create(f.name)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(fw, f.body); err != nil {
			return err
		}
	}
	return zw.Close()
}

// Bytes renders the workbook into memory.
func Bytes(sh Sheet) ([]byte, error) {
	var buf bytes.Buffer
	if err := Write(&buf, sh); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---------------------------------------------------------------- parts

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

const contentTypes = xmlHeader + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
	`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
	`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` +
	`</Types>`

const rootRels = xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
	`</Relationships>`

const workbookRels = xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
	`</Relationships>`

func workbookXML(sheetName string) string {
	return xmlHeader +
		`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
		`<workbookPr/><sheets><sheet name="` + escape(sanitizeSheetName(sheetName)) +
		`" sheetId="1" r:id="rId1"/></sheets></workbook>`
}

// numFmtId 49 is the built-in "@" text format.
const stylesXML = xmlHeader +
	`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
	`<fonts count="2">` +
	`<font><sz val="11"/><color theme="1"/><name val="Arial"/><family val="2"/></font>` +
	`<font><b/><sz val="11"/><color rgb="FFFFFFFF"/><name val="Arial"/><family val="2"/></font>` +
	`</fonts>` +
	`<fills count="3">` +
	`<fill><patternFill patternType="none"/></fill>` +
	`<fill><patternFill patternType="gray125"/></fill>` +
	`<fill><patternFill patternType="solid"><fgColor rgb="FF1F4D3D"/><bgColor indexed="64"/></patternFill></fill>` +
	`</fills>` +
	`<borders count="2">` +
	`<border><left/><right/><top/><bottom/><diagonal/></border>` +
	`<border>` +
	`<left style="thin"><color rgb="FFBFBFBF"/></left><right style="thin"><color rgb="FFBFBFBF"/></right>` +
	`<top style="thin"><color rgb="FFBFBFBF"/></top><bottom style="thin"><color rgb="FFBFBFBF"/></bottom>` +
	`<diagonal/></border>` +
	`</borders>` +
	`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
	`<cellXfs count="4">` +
	`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
	`<xf numFmtId="0" fontId="1" fillId="2" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1" applyAlignment="1">` +
	`<alignment horizontal="center" vertical="center" wrapText="1"/></xf>` +
	`<xf numFmtId="49" fontId="0" fillId="0" borderId="1" xfId="0" applyNumberFormat="1" applyBorder="1" applyAlignment="1">` +
	`<alignment horizontal="right" vertical="center"/></xf>` +
	`<xf numFmtId="0" fontId="0" fillId="0" borderId="1" xfId="0" applyBorder="1" applyAlignment="1">` +
	`<alignment horizontal="center" vertical="center"/></xf>` +
	`</cellXfs>` +
	`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>` +
	`</styleSheet>`

func sheetXML(sh Sheet) string {
	nCols := len(sh.Columns)
	nRows := len(sh.Rows) + 1
	last := colName(nCols-1) + fmt.Sprint(nRows)
	ref := "A1:" + last

	var b strings.Builder
	b.Grow(64 * 1024)
	b.WriteString(xmlHeader)
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	b.WriteString(`<dimension ref="` + ref + `"/>`)
	b.WriteString(`<sheetViews><sheetView`)
	if sh.RTL {
		b.WriteString(` rightToLeft="1"`)
	}
	b.WriteString(` tabSelected="1" workbookViewId="0">`)
	b.WriteString(`<pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/>`)
	b.WriteString(`</sheetView></sheetViews>`)
	b.WriteString(`<sheetFormatPr defaultRowHeight="18"/>`)

	if nCols > 0 {
		b.WriteString(`<cols>`)
		for i, c := range sh.Columns {
			w := c.Width
			if w <= 0 {
				w = 18
			}
			fmt.Fprintf(&b, `<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, i+1, i+1, w)
		}
		b.WriteString(`</cols>`)
	}

	b.WriteString(`<sheetData>`)
	// Header row.
	b.WriteString(`<row r="1" ht="30" customHeight="1">`)
	for i, c := range sh.Columns {
		writeInline(&b, colName(i)+"1", styleHeader, c.Header)
	}
	b.WriteString(`</row>`)

	for ri, row := range sh.Rows {
		rn := ri + 2
		fmt.Fprintf(&b, `<row r="%d">`, rn)
		for ci := range sh.Columns {
			val := ""
			if ci < len(row) {
				val = row[ci]
			}
			ref := colName(ci) + fmt.Sprint(rn)
			if sh.Columns[ci].Numeric && isPlainNumber(val) {
				fmt.Fprintf(&b, `<c r="%s" s="%d"><v>%s</v></c>`, ref, styleNumber, val)
				continue
			}
			writeInline(&b, ref, styleText, val)
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData>`)
	if nCols > 0 && nRows > 1 {
		b.WriteString(`<autoFilter ref="` + ref + `"/>`)
	}
	b.WriteString(`</worksheet>`)
	return b.String()
}

func writeInline(b *strings.Builder, ref string, style int, v string) {
	if v == "" {
		fmt.Fprintf(b, `<c r="%s" s="%d"/>`, ref, style)
		return
	}
	fmt.Fprintf(b, `<c r="%s" s="%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`,
		ref, style, escape(v))
}

func isPlainNumber(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	// A value with a leading zero must stay text, or the zero is lost.
	return !(len(v) > 1 && v[0] == '0')
}

func colName(i int) string {
	name := ""
	for i >= 0 {
		name = string(rune('A'+i%26)) + name
		i = i/26 - 1
	}
	return name
}

var escaper = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;",
)

// escape encodes XML metacharacters and drops control bytes that would make the
// workbook unreadable.
func escape(s string) string {
	var clean strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' {
			clean.WriteRune(r)
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		clean.WriteRune(r)
	}
	return escaper.Replace(clean.String())
}

var sheetNameBad = strings.NewReplacer(
	":", " ", "\\", " ", "/", " ", "?", " ", "*", " ", "[", " ", "]", " ",
)

func sanitizeSheetName(n string) string {
	n = strings.TrimSpace(sheetNameBad.Replace(n))
	r := []rune(n)
	if len(r) > 31 {
		n = string(r[:31])
	}
	if n == "" {
		n = "Sheet1"
	}
	return n
}
