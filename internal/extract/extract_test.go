package extract

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/xuri/excelize/v2"
)

func TestSupported(t *testing.T) {
	cases := map[string]bool{
		"notes.txt":       true,
		"README.md":       true,
		"doc.markdown":    true,
		"page.HTML":       true, // case-insensitive
		"page.htm":        true,
		"report.pdf":      true,
		"letter.docx":     true,
		"sheet.xlsx":      true,
		"message.eml":     true,
		"archive.zip":     true, // generic archive
		"archive.tar":     true,
		"archive.tar.gz":  true,
		"archive.tar.bz2": true,
		"image.png":       false,
		"main.go":         false,
		"no_extension":    false,
		"trailing.dotdoc": false,
	}
	for name, want := range cases {
		if got := Supported(name); got != want {
			t.Errorf("Supported(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestExtractUnsupported(t *testing.T) {
	for _, name := range []string{"image.png", "binary.bin", "noext"} {
		_, err := Extract(name, []byte("whatever"))
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("Extract(%q) error = %v, want ErrUnsupported", name, err)
		}
	}
}

func TestExtractPassthrough(t *testing.T) {
	cases := []struct {
		name string
		file string
		data string
	}{
		{"txt", "notes.txt", "plain text line one\nline two"},
		{"md", "README.md", "# Title\n\nsome *markdown* body"},
		{"markdown_unicode", "doc.markdown", "content with unicode: café résumé 日本語 🚀"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Extract(tc.file, []byte(tc.data))
			if err != nil {
				t.Fatalf("Extract(%q) unexpected error: %v", tc.file, err)
			}
			if got != tc.data {
				t.Errorf("Extract(%q) = %q, want %q (verbatim passthrough)", tc.file, got, tc.data)
			}
		})
	}
}

func TestExtractPassthroughRejectsBinary(t *testing.T) {
	// A .txt file holding invalid UTF-8 must be rejected as not-text rather than
	// silently indexing binary garbage.
	binary := []byte{0xff, 0xfe, 0x00, 0x01, 0x80, 0x81}
	_, err := Extract("mislabelled.txt", binary)
	if !errors.Is(err, ErrNotText) {
		t.Fatalf("Extract of binary .txt: error = %v, want ErrNotText", err)
	}
}

func TestExtractHTML(t *testing.T) {
	// Nested tags, HTML entities, and non-visible <head>/<style>/<script> subtrees.
	const doc = `<!DOCTYPE html>
<html>
<head>
  <title>Ignored Title</title>
  <style>.x { color: red; }</style>
  <script>var secret = "should not appear";</script>
</head>
<body>
  <h1>Main &amp; Heading</h1>
  <p>First paragraph with <strong>nested <em>emphasis</em></strong> here.</p>
  <ul><li>Item one &lt;tagged&gt;</li><li>Item two</li></ul>
  <p>Café r&eacute;sum&eacute; entity test.</p>
</body>
</html>`

	got, err := Extract("page.html", []byte(doc))
	if err != nil {
		t.Fatalf("Extract html: unexpected error: %v", err)
	}

	wantContains := []string{
		"Main & Heading",     // &amp; decoded
		"nested", "emphasis", // nested inline tags flattened
		"Item one <tagged>", // &lt;/&gt; decoded
		"Item two",
		"Café résumé entity test.", // &eacute; decoded, UTF-8 preserved
	}
	for _, want := range wantContains {
		if !strings.Contains(got, want) {
			t.Errorf("html text missing %q; got:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"should not appear", "color: red", "Ignored Title"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("html text should not contain %q; got:\n%s", unwanted, got)
		}
	}
}

func TestExtractDOCX(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		wantContains []string
		// wantOrdered, when set, must appear in this exact left-to-right order.
		wantOrdered []string
	}{
		{
			name: "paragraphs",
			body: para("First paragraph of the letter.") +
				para("Second paragraph follows here."),
			wantContains: []string{"First paragraph of the letter.", "Second paragraph follows here."},
			wantOrdered:  []string{"First paragraph", "Second paragraph"},
		},
		{
			name: "heading_list_table",
			body: heading("Quarterly Report") +
				para("First bullet") + para("Second bullet") +
				table([][]string{{"Region", "Revenue"}, {"North", "1000"}, {"South", "2000"}}),
			wantContains: []string{
				"Quarterly Report",              // heading paragraph
				"First bullet", "Second bullet", // list items are paragraphs
				"Region", "Revenue", "North", "1000", "South", "2000", // table cells
			},
			wantOrdered: []string{"Quarterly Report", "First bullet", "Region", "North", "South"},
		},
		{
			name:         "line_breaks_and_tabs",
			body:         `<w:p><w:r><w:t>left</w:t><w:tab/><w:t>right</w:t><w:br/><w:t>next line</w:t></w:r></w:p>`,
			wantContains: []string{"left\tright", "next line"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Extract("doc.docx", buildDOCX(t, tc.body))
			if err != nil {
				t.Fatalf("Extract docx: unexpected error: %v", err)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("docx text missing %q; got:\n%s", want, got)
				}
			}
			assertOrder(t, got, tc.wantOrdered)
		})
	}
}

func TestExtractDOCXMissingDocument(t *testing.T) {
	// A zip that is not a Word document (no word/document.xml) must error gracefully.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("other.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("<x/>")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Extract("bogus.docx", buf.Bytes()); err == nil {
		t.Error("Extract of docx without word/document.xml: want error, got nil")
	}
}

func TestExtractDOCXEncrypted(t *testing.T) {
	// Password-protected OOXML files are not zips: Office wraps them in an OLE2 /
	// CFBF compound file. Any .docx that begins with that signature must be
	// reported as encrypted rather than as a corrupt zip.
	ole := append(append([]byte{}, oleMagic...), make([]byte, 512)...)
	if _, err := Extract("locked.docx", ole); !errors.Is(err, ErrEncrypted) {
		t.Errorf("Extract of OLE-wrapped docx: error = %v, want ErrEncrypted", err)
	}
}

func TestExtractXLSX(t *testing.T) {
	// Two sheets, an intentionally empty cell, and a formula cell.
	f := excelize.NewFile()
	t.Cleanup(func() { _ = f.Close() })

	if err := f.SetSheetName("Sheet1", "Inventory"); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	set(t, f, "Inventory", map[string]any{
		"A1": "Item", "B1": "Qty",
		"A2": "Widget", "B2": 42,
		// A3 deliberately left empty
		"B3": 8,
	})
	if err := f.SetCellFormula("Inventory", "B4", "SUM(B2:B3)"); err != nil {
		t.Fatalf("set formula: %v", err)
	}
	if _, err := f.NewSheet("Notes"); err != nil {
		t.Fatalf("new sheet: %v", err)
	}
	set(t, f, "Notes", map[string]any{"A1": "Remark", "A2": "café menu"})

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}

	got, err := Extract("book.xlsx", buf.Bytes())
	if err != nil {
		t.Fatalf("Extract xlsx: unexpected error: %v", err)
	}
	for _, want := range []string{
		"# Inventory", "# Notes", // one heading per sheet
		"Widget", "42", "café menu", // literal cells incl. UTF-8
		"Widget\t42", // cells within a row are tab-separated
		"\t8",        // empty leading cell (A3) preserved as a tab
	} {
		if !strings.Contains(got, want) {
			t.Errorf("xlsx text missing %q; got:\n%s", want, got)
		}
	}
	// The formula cell has no cached result, so excelize yields no value for it;
	// the point is that a sheet containing a formula extracts without error.
	assertOrder(t, got, []string{"# Inventory", "# Notes"})
}

func TestExtractXLSXEncrypted(t *testing.T) {
	// excelize writes a genuine password-protected (OLE2 compound) workbook, which
	// Extract must report as encrypted rather than crash on.
	f := excelize.NewFile()
	set(t, f, "Sheet1", map[string]any{"A1": "top secret"})
	var buf bytes.Buffer
	if err := f.Write(&buf, excelize.Options{Password: "hunter2"}); err != nil {
		t.Fatalf("write encrypted xlsx: %v", err)
	}
	_ = f.Close()

	if !isOLECompound(buf.Bytes()) {
		t.Fatalf("expected encrypted xlsx to be an OLE compound file")
	}
	if _, err := Extract("locked.xlsx", buf.Bytes()); !errors.Is(err, ErrEncrypted) {
		t.Errorf("Extract of encrypted xlsx: error = %v, want ErrEncrypted", err)
	}
}

func TestExtractPDF(t *testing.T) {
	cases := []struct {
		name         string
		pages        []string
		wantContains []string
		wantEmpty    bool
	}{
		{
			name:         "single_page",
			pages:        []string{text(72, 700, "Hello PDF Extract")},
			wantContains: []string{"Hello PDF Extract"},
		},
		{
			name: "multi_page",
			pages: []string{
				text(72, 700, "Page One Alpha"),
				text(72, 700, "Page Two Bravo"),
				text(72, 700, "Page Three Charlie"),
			},
			wantContains: []string{"Page One Alpha", "Page Two Bravo", "Page Three Charlie"},
		},
		{
			name: "table_columns",
			pages: []string{
				text(72, 700, "Name") + text(300, 700, "Score") +
					text(72, 680, "Alice") + text(300, 680, "95") +
					text(72, 660, "Bob") + text(300, 660, "87"),
			},
			wantContains: []string{"Name", "Score", "Alice", "95", "Bob", "87"},
		},
		{
			name:         "non_ascii",
			pages:        []string{text(72, 700, `caf\351 r\351sum\351 na\357ve`)}, // WinAnsi octal escapes
			wantContains: []string{"café résumé naïve"},
		},
		{
			name:      "text_less", // an image-only / scanned-like page has no text layer
			pages:     []string{""},
			wantEmpty: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Extract("doc.pdf", buildPDF(tc.pages))
			if err != nil {
				t.Fatalf("Extract pdf: unexpected error: %v", err)
			}
			if tc.wantEmpty {
				if strings.TrimSpace(got) != "" {
					t.Errorf("text-less pdf: want empty, got %q", got)
				}
				return
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("pdf text missing %q; got:\n%s", want, got)
				}
			}
		})
	}
}

func TestExtractPDFEncrypted(t *testing.T) {
	// Build a plain PDF then encrypt it with pdfcpu (AES). The reader cannot open
	// it without the password, which Extract must surface as ErrEncrypted.
	plain := buildPDF([]string{text(72, 700, "Confidential")})

	conf := model.NewDefaultConfiguration()
	conf.Cmd = model.ENCRYPT
	conf.UserPW = "secret"
	conf.OwnerPW = "secret"

	var enc bytes.Buffer
	if err := api.Encrypt(bytes.NewReader(plain), &enc, conf); err != nil {
		t.Fatalf("encrypt pdf: %v", err)
	}

	if _, err := Extract("locked.pdf", enc.Bytes()); !errors.Is(err, ErrEncrypted) {
		t.Errorf("Extract of encrypted pdf: error = %v, want ErrEncrypted", err)
	}
}

// TestExtractCorruptAndEdge exercises malformed, empty and wrong-extension inputs
// across every binary format: each must return an error (never panic).
func TestExtractCorruptAndEdge(t *testing.T) {
	pngMagic := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 1, 2, 3}

	cases := []struct {
		name string
		file string
		data []byte
	}{
		{"pdf_garbage", "broken.pdf", []byte("%PDF-1.4 this is not a real pdf")},
		{"pdf_empty", "empty.pdf", nil},
		{"pdf_png_bytes", "image.pdf", pngMagic},
		{"docx_garbage", "broken.docx", []byte("not a zip at all")},
		{"docx_empty", "empty.docx", nil},
		{"docx_png_bytes", "image.docx", pngMagic},
		{"xlsx_garbage", "broken.xlsx", []byte("not a zip at all")},
		{"xlsx_empty", "empty.xlsx", nil},
		{"xlsx_png_bytes", "image.xlsx", pngMagic},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The call must not panic; recover here would only mask a regression
			// since Extract is documented to convert panics into errors itself.
			if _, err := Extract(tc.file, tc.data); err == nil {
				t.Errorf("Extract(%q, %d bytes): want error, got nil", tc.file, len(tc.data))
			}
		})
	}
}

// TestExtractTextEdge covers text formats on empty input (valid, empty result).
func TestExtractTextEdge(t *testing.T) {
	for _, name := range []string{"empty.txt", "empty.md", "empty.html"} {
		got, err := Extract(name, nil)
		if err != nil {
			t.Errorf("Extract(%q, nil): unexpected error: %v", name, err)
		}
		if strings.TrimSpace(got) != "" {
			t.Errorf("Extract(%q, nil) = %q, want empty", name, got)
		}
	}
}

// --- ordering assertion ------------------------------------------------------

// assertOrder checks that each string in want appears in got, in the given order.
func assertOrder(t *testing.T, got string, want []string) {
	t.Helper()
	from := 0
	for _, w := range want {
		i := strings.Index(got[from:], w)
		if i < 0 {
			t.Errorf("expected %q at/after offset %d (out of order or missing); got:\n%s", w, from, got)
			return
		}
		from += i + len(w)
	}
}

// --- docx fixtures -----------------------------------------------------------

// para wraps text in a WordprocessingML paragraph run.
func para(s string) string {
	return "<w:p><w:r><w:t>" + s + "</w:t></w:r></w:p>"
}

// heading is a paragraph carrying a Heading1 style (text still lives in <w:t>).
func heading(s string) string {
	return `<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>` + s + `</w:t></w:r></w:p>`
}

// table renders rows of cells as a WordprocessingML table (<w:tbl>).
func table(rows [][]string) string {
	var b strings.Builder
	b.WriteString("<w:tbl>")
	for _, row := range rows {
		b.WriteString("<w:tr>")
		for _, cell := range row {
			b.WriteString("<w:tc>" + para(cell) + "</w:tc>")
		}
		b.WriteString("</w:tr>")
	}
	b.WriteString("</w:tbl>")
	return b.String()
}

// buildDOCX assembles a minimal but valid .docx (a zip with word/document.xml)
// whose <w:body> holds the supplied WordprocessingML fragment.
func buildDOCX(t *testing.T, body string) []byte {
	t.Helper()
	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>` + body + `</w:body></w:document>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("create document.xml: %v", err)
	}
	if _, err := w.Write([]byte(document)); err != nil {
		t.Fatalf("write document.xml: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close docx zip: %v", err)
	}
	return buf.Bytes()
}

// --- xlsx fixtures -----------------------------------------------------------

// set writes a batch of cell values, failing the test on any error.
func set(t *testing.T, f *excelize.File, sheet string, cells map[string]any) {
	t.Helper()
	for ref, v := range cells {
		if err := f.SetCellValue(sheet, ref, v); err != nil {
			t.Fatalf("set cell %s!%s: %v", sheet, ref, err)
		}
	}
}

// --- pdf fixtures ------------------------------------------------------------

// text emits a positioned Helvetica text-showing operator for a content stream.
func text(x, y int, s string) string {
	return fmt.Sprintf("BT /F1 12 Tf %d %d Td (%s) Tj ET\n", x, y, s)
}

// buildPDF hand-assembles a minimal multi-page PDF, one page per content-stream
// body in pages (an empty body yields a text-less page), computing the xref byte
// offsets so github.com/ledongthuc/pdf can read the text layer. WinAnsiEncoding
// lets Latin-1 accented characters written as octal escapes round-trip.
func buildPDF(pages []string) []byte {
	n := len(pages)
	fontObj := 3 + 2*n // 1=catalog, 2=pages, 3..2+n=pages, 3+n..2+2n=streams, 3+2n=font

	var buf bytes.Buffer
	var offsets []int
	obj := func(body string) {
		offsets = append(offsets, buf.Len())
		buf.WriteString(body)
	}

	buf.WriteString("%PDF-1.4\n")
	obj("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	var kids strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&kids, "%d 0 R ", 3+i)
	}
	obj(fmt.Sprintf("2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", kids.String(), n))

	for i := 0; i < n; i++ {
		obj(fmt.Sprintf("%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
			"/Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >> >>\nendobj\n",
			3+i, 3+n+i, fontObj))
	}
	for i := 0; i < n; i++ {
		s := pages[i]
		obj(fmt.Sprintf("%d 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", 3+n+i, len(s), s))
	}
	obj(fmt.Sprintf("%d 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica "+
		"/Encoding /WinAnsiEncoding >>\nendobj\n", fontObj))

	xrefStart := buf.Len()
	total := len(offsets) + 1
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", total)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", total, xrefStart)
	return buf.Bytes()
}

// --- legacy office (conditional) ---------------------------------------------

// hasLibreOffice reports whether libreoffice is available in $PATH.
func hasLibreOffice() bool {
	return libreOfficeAvailable
}

// TestExtractLegacyOfficeRegistration verifies that .doc/.xls/.ppt are
// registered in byExt only when libreoffice is available.
func TestExtractLegacyOfficeRegistration(t *testing.T) {
	for _, ext := range []string{".doc", ".xls", ".ppt"} {
		registryMu.RLock()
		_, inByExt := byExt[ext]
		registryMu.RUnlock()
		if hasLibreOffice() && !inByExt {
			t.Errorf("ext %s should be registered when libreoffice is available", ext)
		}
		if !hasLibreOffice() && inByExt {
			t.Errorf("ext %s should NOT be registered when libreoffice is missing", ext)
		}
	}
}

// TestExtractLegacyOfficeUnsupported verifies that without libreoffice, .doc
// files return ErrUnsupported (or the format error from extractLegacyOffice).
func TestExtractLegacyOfficeUnsupported(t *testing.T) {
	if hasLibreOffice() {
		t.Skip("libreoffice is available — testing registration path instead")
	}
	for _, name := range []string{"doc.doc", "sheet.xls", "deck.ppt"} {
		_, err := Extract(name, []byte("dummy"))
		if err == nil {
			t.Errorf("Extract(%q) should error without libreoffice", name)
		}
	}
}
