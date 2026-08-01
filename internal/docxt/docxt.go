// Package docxt fills placeholders in a Word template supplied by the operator.
//
// The hard part is that Word does not store a paragraph's text as one string.
// It splits it across any number of <w:r> runs whenever the spell checker, a
// revision id, or a formatting change happens to land mid-word. A template that
// visibly reads {{name}} very often looks like this in word/document.xml:
//
//	<w:r><w:t>{{na</w:t></w:r><w:r><w:t>me}}</w:t></w:r>
//
// so a naive bytes.Replace over the file finds nothing. This package therefore
// works paragraph by paragraph: it concatenates the text of every <w:t> in the
// paragraph, searches that, and then splices the replacement back into the
// individual nodes. Everything outside a placeholder keeps its own run and its
// own formatting; the substituted value inherits the formatting of the run the
// placeholder started in, which is what an operator intends when they style a
// placeholder.
package docxt

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
)

// MaxTemplateSize caps an uploaded template. Real forms are a few dozen KB.
const MaxTemplateSize = 10 << 20

var (
	ErrNotDocx  = errors.New("الملف ليس مستند Word صالح")
	ErrTooLarge = errors.New("حجم الملف كبير جداً")
)

// partPattern matches the XML parts that carry visible text.
func isTextPart(name string) bool {
	switch {
	case name == "word/document.xml":
		return true
	case strings.HasPrefix(name, "word/header") && strings.HasSuffix(name, ".xml"):
		return true
	case strings.HasPrefix(name, "word/footer") && strings.HasSuffix(name, ".xml"):
		return true
	}
	return false
}

var (
	paragraphRE   = regexp.MustCompile(`(?s)<w:p[ >].*?</w:p>|<w:p/>`)
	textNodeRE    = regexp.MustCompile(`(?s)<w:t(?:\s[^>]*)?>.*?</w:t>|<w:t(?:\s[^>]*)?/>`)
	placeholderRE = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_\-]+)\s*\}\}`)
)

// textSpan locates the character range of one <w:t> node's content within the
// paragraph's concatenated text, plus the byte range of that content in the XML.
type textSpan struct {
	xmlStart, xmlEnd int // byte range of the inner text inside the paragraph XML
	txtStart, txtEnd int // rune-free byte range within the concatenated string
	selfClosing      bool
}

// Fill replaces every {{key}} in the template with values[key]. Placeholders
// with no matching key are left untouched so the operator can see what they
// mistyped rather than silently losing a field.
func Fill(template []byte, values map[string]string) ([]byte, error) {
	return transform(template, func(part []byte) []byte {
		return fillPart(part, values)
	})
}

// Placeholders reports every distinct {{key}} found in the template, so the
// dashboard can tell the operator which ones the document actually uses.
func Placeholders(template []byte) ([]string, error) {
	seen := map[string]bool{}
	_, err := transform(template, func(part []byte) []byte {
		for _, p := range paragraphRE.FindAll(part, -1) {
			text, _ := paragraphText(p)
			for _, m := range placeholderRE.FindAllStringSubmatch(text, -1) {
				seen[m[1]] = true
			}
		}
		return part
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// Validate checks the upload really is a Word document before it is stored.
func Validate(template []byte) error {
	if len(template) == 0 {
		return ErrNotDocx
	}
	if len(template) > MaxTemplateSize {
		return ErrTooLarge
	}
	zr, err := zip.NewReader(bytes.NewReader(template), int64(len(template)))
	if err != nil {
		return ErrNotDocx
	}
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			return nil
		}
	}
	return ErrNotDocx
}

// transform rewrites every text-bearing part of the archive with fn and copies
// the rest through byte for byte.
func transform(template []byte, fn func([]byte) []byte) ([]byte, error) {
	if len(template) > MaxTemplateSize {
		return nil, ErrTooLarge
	}
	zr, err := zip.NewReader(bytes.NewReader(template), int64(len(template)))
	if err != nil {
		return nil, ErrNotDocx
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hasDocument := false

	for _, f := range zr.File {
		// The template comes from a person, so treat the archive as untrusted:
		// skip directory traversal names and anything that is not a plain file.
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			continue
		}
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, ErrNotDocx
		}
		data, err := io.ReadAll(io.LimitReader(rc, MaxTemplateSize))
		rc.Close()
		if err != nil {
			return nil, ErrNotDocx
		}

		if f.Name == "word/document.xml" {
			hasDocument = true
		}
		if isTextPart(f.Name) {
			data = fn(data)
		}

		// Preserve the original header so timestamps and flags survive.
		hdr := f.FileHeader
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(&hdr)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	if !hasDocument {
		return nil, ErrNotDocx
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// paragraphText concatenates the text of every <w:t> in a paragraph and returns
// the spans needed to map positions in that string back to the XML.
func paragraphText(p []byte) (string, []textSpan) {
	locs := textNodeRE.FindAllIndex(p, -1)
	if locs == nil {
		return "", nil
	}
	var sb strings.Builder
	spans := make([]textSpan, 0, len(locs))
	for _, loc := range locs {
		node := p[loc[0]:loc[1]]
		if bytes.HasSuffix(node, []byte("/>")) {
			// <w:t/> — an empty node. Record it so a replacement can still be
			// written into it if a placeholder happens to start here.
			spans = append(spans, textSpan{
				xmlStart: loc[1], xmlEnd: loc[1],
				txtStart: sb.Len(), txtEnd: sb.Len(),
				selfClosing: true,
			})
			continue
		}
		open := bytes.IndexByte(node, '>')
		closeIdx := bytes.LastIndex(node, []byte("</w:t>"))
		if open < 0 || closeIdx < 0 || open+1 > closeIdx {
			continue
		}
		inner := node[open+1 : closeIdx]
		start := sb.Len()
		sb.Write(inner)
		spans = append(spans, textSpan{
			xmlStart: loc[0] + open + 1,
			xmlEnd:   loc[0] + closeIdx,
			txtStart: start,
			txtEnd:   start + len(inner),
		})
	}
	return sb.String(), spans
}

// fillPart rewrites one XML part, paragraph by paragraph.
func fillPart(part []byte, values map[string]string) []byte {
	locs := paragraphRE.FindAllIndex(part, -1)
	if locs == nil {
		return part
	}
	var out bytes.Buffer
	out.Grow(len(part))
	prev := 0
	for _, loc := range locs {
		out.Write(part[prev:loc[0]])
		out.Write(fillParagraph(part[loc[0]:loc[1]], values))
		prev = loc[1]
	}
	out.Write(part[prev:])
	return out.Bytes()
}

// fillParagraph performs the actual splice. Text is XML-escaped in the source,
// so the search string is unescaped first and the replacement re-escaped.
func fillParagraph(p []byte, values map[string]string) []byte {
	text, spans := paragraphText(p)
	if text == "" || len(spans) == 0 {
		return p
	}
	plain := unescapeXML(text)
	if !strings.Contains(plain, "{{") {
		return p
	}

	matches := placeholderRE.FindAllStringSubmatchIndex(plain, -1)
	if matches == nil {
		return p
	}

	// Positions were computed on the unescaped string; map them back onto the
	// escaped one so the spans still line up.
	mapPos := escapedOffsets(text)

	// edits are (start, end, replacement) against the escaped concatenated text.
	type edit struct {
		start, end int
		value      string
	}
	var edits []edit
	for _, m := range matches {
		key := plain[m[2]:m[3]]
		val, found := values[key]
		if !found {
			continue // leave unknown placeholders visible
		}
		edits = append(edits, edit{
			start: mapPos(m[0]),
			end:   mapPos(m[1]),
			value: escapeXML(val),
		})
	}
	if len(edits) == 0 {
		return p
	}

	// Apply the edits per span. Each placeholder may straddle several spans:
	// the value goes into the first span it touches, and the matched characters
	// are removed from the rest. Within a span the sub-edits are applied from
	// right to left so earlier offsets stay valid.
	type subEdit struct {
		lo, hi int    // span-local byte range to replace
		value  string // empty for every span after the first
	}
	perSpan := make([][]subEdit, len(spans))
	for _, e := range edits {
		placed := false
		for i, s := range spans {
			if s.txtEnd <= e.start || s.txtStart >= e.end {
				continue
			}
			lo := max(e.start, s.txtStart) - s.txtStart
			hi := min(e.end, s.txtEnd) - s.txtStart
			if lo > hi {
				continue
			}
			val := ""
			if !placed {
				val = e.value
				placed = true
			}
			perSpan[i] = append(perSpan[i], subEdit{lo: lo, hi: hi, value: val})
		}
	}

	newInner := make([]string, len(spans))
	for i, s := range spans {
		cur := text[s.txtStart:s.txtEnd]
		subs := perSpan[i]
		sort.Slice(subs, func(a, b int) bool { return subs[a].lo > subs[b].lo })
		for _, sub := range subs {
			if sub.lo > len(cur) || sub.hi > len(cur) {
				continue
			}
			cur = cur[:sub.lo] + sub.value + cur[sub.hi:]
		}
		newInner[i] = cur
	}

	// Reassemble the paragraph XML with the new inner text.
	var out bytes.Buffer
	out.Grow(len(p) + 64)
	prev := 0
	for i, s := range spans {
		if s.selfClosing {
			continue
		}
		out.Write(p[prev:s.xmlStart])
		out.WriteString(newInner[i])
		prev = s.xmlEnd
	}
	out.Write(p[prev:])
	res := out.Bytes()

	// A run whose text became empty must keep xml:space="preserve" semantics;
	// nothing else is required, Word renders an empty <w:t> fine.
	return res
}

// escapedOffsets returns a function converting an offset in the unescaped text
// to the matching offset in the escaped text.
func escapedOffsets(escaped string) func(int) int {
	// Walk the escaped string, recording the escaped index at each plain index.
	idx := make([]int, 0, len(escaped)+1)
	i := 0
	for i < len(escaped) {
		idx = append(idx, i)
		if escaped[i] == '&' {
			if j := strings.IndexByte(escaped[i:], ';'); j > 0 && j <= 6 {
				i += j + 1
				continue
			}
		}
		i++
	}
	idx = append(idx, len(escaped))
	return func(plainPos int) int {
		if plainPos < 0 {
			return 0
		}
		if plainPos >= len(idx) {
			return len(escaped)
		}
		return idx[plainPos]
	}
}

var xmlUnescaper = strings.NewReplacer(
	"&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&amp;", "&",
)

func unescapeXML(s string) string { return xmlUnescaper.Replace(s) }

func escapeXML(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		case '\n':
			// A literal newline is invalid inside <w:t>; Word needs a break.
			b.WriteString("</w:t><w:br/><w:t xml:space=\"preserve\">")
		default:
			if r < 0x20 && r != '\t' {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
