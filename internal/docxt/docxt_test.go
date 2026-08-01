package docxt

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildDocx wraps a document.xml body into a minimal but real .docx archive.
func buildDocx(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	add("[Content_Types].xml", `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`)
	add("_rels/.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`)
	add("word/document.xml", `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`+body+`</w:body></w:document>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func documentXML(t *testing.T, docx []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			defer rc.Close()
			b := new(bytes.Buffer)
			b.ReadFrom(rc)
			return b.String()
		}
	}
	t.Fatal("document.xml missing")
	return ""
}

func TestFillContiguousPlaceholder(t *testing.T) {
	doc := buildDocx(t, `<w:p><w:r><w:t>الاسم: {{name}}</w:t></w:r></w:p>`)
	out, err := Fill(doc, map[string]string{"name": "محمود حميد علي بوراشد"})
	if err != nil {
		t.Fatal(err)
	}
	x := documentXML(t, out)
	if !strings.Contains(x, "محمود حميد علي بوراشد") {
		t.Fatalf("value not substituted: %s", x)
	}
	if strings.Contains(x, "{{name}}") {
		t.Fatal("placeholder still present")
	}
}

// This is the case that defeats a plain bytes.Replace: Word has fragmented the
// placeholder across four runs, none of which contains the whole token.
func TestFillPlaceholderSplitAcrossRuns(t *testing.T) {
	doc := buildDocx(t, `<w:p>`+
		`<w:r><w:t xml:space="preserve">الرقم الشخصي: {{</w:t></w:r>`+
		`<w:r><w:t>c</w:t></w:r>`+
		`<w:r><w:t>p</w:t></w:r>`+
		`<w:r><w:t>r}}</w:t></w:r>`+
		`</w:p>`)
	out, err := Fill(doc, map[string]string{"cpr": "000304450"})
	if err != nil {
		t.Fatal(err)
	}
	x := documentXML(t, out)
	if !strings.Contains(x, "000304450") {
		t.Fatalf("split placeholder not filled: %s", x)
	}
	if strings.Contains(x, "{{") || strings.Contains(x, "}}") {
		t.Fatalf("placeholder residue left behind: %s", x)
	}
	if !strings.Contains(x, "الرقم الشخصي: ") {
		t.Fatal("surrounding text damaged")
	}
}

func TestFillTwoPlaceholdersInOneRun(t *testing.T) {
	doc := buildDocx(t, `<w:p><w:r><w:t>{{house}} / {{road}} / {{block}}</w:t></w:r></w:p>`)
	out, err := Fill(doc, map[string]string{"house": "2393", "road": "3468", "block": "1034"})
	if err != nil {
		t.Fatal(err)
	}
	x := documentXML(t, out)
	for _, want := range []string{"2393", "3468", "1034"} {
		if !strings.Contains(x, want) {
			t.Fatalf("missing %s in %s", want, x)
		}
	}
	if !strings.Contains(x, "2393 / 3468 / 1034") {
		t.Fatalf("separators lost: %s", x)
	}
}

func TestFillPreservesFormattingRuns(t *testing.T) {
	doc := buildDocx(t, `<w:p>`+
		`<w:r><w:rPr><w:b/></w:rPr><w:t>الاسم:</w:t></w:r>`+
		`<w:r><w:t>{{name}}</w:t></w:r>`+
		`</w:p>`)
	out, err := Fill(doc, map[string]string{"name": "زينب"})
	if err != nil {
		t.Fatal(err)
	}
	x := documentXML(t, out)
	if !strings.Contains(x, "<w:b/>") {
		t.Fatal("bold run lost")
	}
	if !strings.Contains(x, "<w:t>الاسم:</w:t>") {
		t.Fatalf("label run altered: %s", x)
	}
}

func TestUnknownPlaceholderLeftVisible(t *testing.T) {
	doc := buildDocx(t, `<w:p><w:r><w:t>{{name}} {{typoo}}</w:t></w:r></w:p>`)
	out, err := Fill(doc, map[string]string{"name": "علي"})
	if err != nil {
		t.Fatal(err)
	}
	x := documentXML(t, out)
	if !strings.Contains(x, "{{typoo}}") {
		t.Fatal("unknown placeholder should stay visible so the operator can spot it")
	}
	if !strings.Contains(x, "علي") {
		t.Fatal("known placeholder not filled")
	}
}

func TestValueWithXMLSpecialCharsIsEscaped(t *testing.T) {
	doc := buildDocx(t, `<w:p><w:r><w:t>{{name}}</w:t></w:r></w:p>`)
	out, err := Fill(doc, map[string]string{"name": `a & b <script> "x"`})
	if err != nil {
		t.Fatal(err)
	}
	x := documentXML(t, out)
	if strings.Contains(x, "<script>") {
		t.Fatalf("raw markup injected into the document: %s", x)
	}
	if !strings.Contains(x, "&amp;") || !strings.Contains(x, "&lt;script&gt;") {
		t.Fatalf("value not escaped: %s", x)
	}
	// The result must still be a readable archive.
	if _, err := Placeholders(out); err != nil {
		t.Fatalf("output archive corrupted: %v", err)
	}
}

func TestEscapedSourceTextSurvives(t *testing.T) {
	doc := buildDocx(t, `<w:p><w:r><w:t>&lt;الاسم&gt; &amp; {{name}}</w:t></w:r></w:p>`)
	out, err := Fill(doc, map[string]string{"name": "علي"})
	if err != nil {
		t.Fatal(err)
	}
	x := documentXML(t, out)
	if !strings.Contains(x, "&lt;الاسم&gt; &amp; علي") {
		t.Fatalf("pre-escaped source text mangled: %s", x)
	}
}

func TestPlaceholdersDiscovery(t *testing.T) {
	doc := buildDocx(t, `<w:p><w:r><w:t>{{name}}</w:t></w:r></w:p>`+
		`<w:p><w:r><w:t>{{cp</w:t></w:r><w:r><w:t>r}} {{status}}</w:t></w:r></w:p>`)
	got, err := Placeholders(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cpr", "name", "status"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSpacedPlaceholderSyntax(t *testing.T) {
	doc := buildDocx(t, `<w:p><w:r><w:t>{{ name }}</w:t></w:r></w:p>`)
	out, err := Fill(doc, map[string]string{"name": "علي"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(documentXML(t, out), "علي") {
		t.Fatal("spaced placeholder not handled")
	}
}

func TestRejectsNonDocx(t *testing.T) {
	if err := Validate([]byte("this is a pdf, honest")); err == nil {
		t.Fatal("expected rejection")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("hello.txt")
	w.Write([]byte("hi"))
	zw.Close()
	if err := Validate(buf.Bytes()); err == nil {
		t.Fatal("a zip without word/document.xml is not a docx")
	}
}

func TestFillIsIdempotentOnMissingValues(t *testing.T) {
	doc := buildDocx(t, `<w:p><w:r><w:t>{{name}}</w:t></w:r></w:p>`)
	once, err := Fill(doc, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Fill(once, map[string]string{"name": "علي"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(documentXML(t, twice), "علي") {
		t.Fatal("second pass should still find the placeholder")
	}
}
