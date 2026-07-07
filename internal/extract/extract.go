// Package extract turns documents into plain text / lightweight markdown so the
// indexing pipeline can chunk and embed them. It dispatches on the file
// extension of a name and knows only about bytes in and text out, so it has no
// dependency on the store, embedder or chunker and is cheap to unit-test in
// isolation. All decoders are pure Go (no CGO).
package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html"
)

// ErrUnsupported is returned by Extract for a file whose extension it does not
// know how to decode (unknown or binary types).
var ErrUnsupported = errors.New("extract: unsupported file type")

// ErrNotText is returned when a format that is expected to be UTF-8 text (.txt,
// .md, .markdown) holds bytes that are not valid UTF-8, so it is almost
// certainly binary content misnamed with a text extension.
var ErrNotText = errors.New("extract: content is not valid UTF-8 text")

// ErrEncrypted is returned when a document is encrypted / password-protected and
// therefore cannot be decoded without a password. Callers can special-case it
// to surface a "needs password" hint instead of a generic parse failure.
var ErrEncrypted = errors.New("extract: file is encrypted/password-protected")

// oleMagic is the 8-byte signature of an OLE2 Compound File (CFBF), the
// container Microsoft Office uses to wrap password-protected OOXML documents
// (.docx/.xlsx). A plain OOXML file is a zip; an encrypted one is CFBF, so this
// signature cleanly distinguishes the two.
var oleMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// isOLECompound reports whether data begins with the OLE2 compound-file magic,
// which for an .docx/.xlsx means the payload is encrypted rather than a zip.
func isOLECompound(data []byte) bool {
	return len(data) >= len(oleMagic) && bytes.Equal(data[:len(oleMagic)], oleMagic)
}

// extractor decodes one family of file types into text.
type extractor func(data []byte) (string, error)

// byExt maps a lowercase extension (including the leading dot) to its decoder.
// It is the single source of truth for both Extract and Supported.
var byExt = map[string]extractor{
	".txt":      passthrough,
	".md":       passthrough,
	".markdown": passthrough,
	// Plain-text data/config families: index them as-is.
	".csv":  passthrough,
	".tsv":  passthrough,
	".log":  passthrough,
	".ini":  passthrough,
	".cfg":  passthrough,
	".conf": passthrough,
	".html": extractHTML,
	".htm":  extractHTML,
	".pdf":  extractPDF,
	".docx": extractDOCX,
	".xlsx": extractXLSX,
	// Added in Fase 1 (formats.go): more document families, all pure-Go.
	".pptx":  extractPPTX,
	".odt":   extractOpenDocument,
	".ods":   extractOpenDocument,
	".odp":   extractOpenDocument,
	".epub":  extractEPUB,
	".ipynb": extractIPYNB,
	".rtf":   extractRTF,
}

// Register adds a custom extractor for the given extension (with leading dot,
// e.g. ".proto"). Panics if ext is already registered (to catch duplicate
// registrations at init time).
func Register(ext string, fn extractor) {
	if _, ok := byExt[ext]; ok {
		panic("extract: duplicate registration for " + ext)
	}
	byExt[ext] = fn
}

// Extract returns the plain-text / lightweight-markdown content of a document,
// dispatched by the file extension of name. Unknown/binary types return
// ErrUnsupported, encrypted documents return ErrEncrypted, and any panic from a
// third-party decoder on malformed input is recovered into an error so a bad
// document can never crash the indexer.
func Extract(name string, data []byte) (out string, err error) {
	ext := strings.ToLower(filepath.Ext(name))
	decode, ok := byExt[ext]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnsupported, ext)
	}

	// A single recover here guards every decoder: decode runs on this goroutine,
	// so a panic deep inside a parser unwinds into this deferred func.
	defer func() {
		if r := recover(); r != nil {
			out, err = "", fmt.Errorf("extract: %s decoder panicked: %v", ext, r)
		}
	}()

	return decode(data)
}

// Supported reports whether ExtractAll can handle a file by its extension —
// either a single document or an archive that fans out to many Docs.
func Supported(name string) bool {
	if archiveType(name) != "" {
		return true
	}
	ext := strings.ToLower(filepath.Ext(name))
	_, ok := byExt[ext]
	return ok || archiveExts[ext]
}

// passthrough returns already-textual formats (.txt, .md, .markdown) as-is,
// rejecting content that is not valid UTF-8 so binary blobs mislabelled with a
// text extension do not pollute the index.
func passthrough(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}
	return string(data), nil
}

// extractHTML strips tags to readable text, dropping <script>/<style> bodies and
// collapsing block-level elements into newline-separated lines.
func extractHTML(data []byte) (string, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("extract: parse html: %w", err)
	}

	var b strings.Builder
	walkHTMLText(doc, &b)

	return normalizeText(b.String()), nil
}

// walkHTMLText recursively appends the visible text of an HTML node tree to b,
// skipping non-visible subtrees and inserting a newline after block-level
// elements so paragraphs stay separated.
func walkHTMLText(n *html.Node, b *strings.Builder) {
	if n.Type == html.ElementNode && isHiddenElement(n.Data) {
		return // skip non-visible subtrees entirely
	}
	if n.Type == html.TextNode {
		if text := strings.TrimSpace(n.Data); text != "" {
			b.WriteString(text)
			b.WriteByte(' ')
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkHTMLText(c, b)
	}
	// Break lines on block-level boundaries so paragraphs stay separated.
	if n.Type == html.ElementNode && isBlockElement(n.Data) {
		b.WriteByte('\n')
	}
}

// isHiddenElement reports whether an element's subtree carries no visible text
// and should be skipped entirely.
func isHiddenElement(tag string) bool {
	switch tag {
	case "script", "style", "head", "noscript":
		return true
	default:
		return false
	}
}

// isBlockElement reports whether an HTML tag should introduce a line break in
// the flattened text (block-level and line-break elements).
func isBlockElement(tag string) bool {
	switch tag {
	case "p", "div", "br", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6",
		"section", "article", "header", "footer", "ul", "ol", "table",
		"blockquote", "pre", "hr":
		return true
	default:
		return false
	}
}

// extractPDF pulls the text layer out of a PDF. Scanned/image-only PDFs have no
// text layer and yield an empty string, which is acceptable. Encrypted PDFs
// (which the decoder rejects or cannot decrypt without a password) return
// ErrEncrypted. Panics on malformed input are recovered by Extract.
func extractPDF(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		if isPDFEncrypted(err) {
			return "", ErrEncrypted
		}
		return "", fmt.Errorf("extract: open pdf: %w", err)
	}
	return pdfText(r)
}

// pdfText pulls the plain text from an already-opened pdf.Reader (shared by the
// unencrypted path and the password path in crypto.go).
func pdfText(r *pdf.Reader) (string, error) {
	rc, err := r.GetPlainText()
	if err != nil {
		if isPDFEncrypted(err) {
			return "", ErrEncrypted
		}
		return "", fmt.Errorf("extract: read pdf text: %w", err)
	}
	var b bytes.Buffer
	if _, err := io.Copy(&b, rc); err != nil {
		return "", fmt.Errorf("extract: read pdf text: %w", err)
	}
	return normalizeText(b.String()), nil
}

// isPDFEncrypted reports whether a pdf reader error signals that the document is
// encrypted. The library exposes the sentinel pdf.ErrInvalidPassword for a wrong
// (here: empty) password, and for ciphers it cannot handle it returns errors
// whose message mentions encryption ("N-bit encryption key", "encryption filter",
// …); both mean the file is password-protected as far as this package cares.
func isPDFEncrypted(err error) bool {
	if errors.Is(err, pdf.ErrInvalidPassword) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "encrypt")
}

// extractDOCX reads word/document.xml from the .docx zip container and joins its
// text, preserving paragraph and table-row breaks as newlines. It uses only
// stdlib (archive/zip + encoding/xml) to keep the dependency surface small.
func extractDOCX(data []byte) (string, error) {
	if isOLECompound(data) {
		return "", ErrEncrypted // password-protected .docx is an OLE container, not a zip
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("extract: open docx: %w", err)
	}

	docXML := zipEntry(zr, "word/document.xml")
	if docXML == nil {
		return "", fmt.Errorf("extract: docx: missing word/document.xml")
	}

	rc, err := docXML.Open()
	if err != nil {
		return "", fmt.Errorf("extract: docx: open document.xml: %w", err)
	}
	defer func() { _ = rc.Close() }()

	return parseWordText(rc)
}

// parseWordText streams the WordprocessingML tokens and collects visible text.
// Matching by local element name (ignoring the usual "w:" namespace prefix) and
// walking every <w:t> regardless of nesting means text inside tables, lists and
// headings is captured, not just top-level paragraphs. Paragraphs (<w:p>) and
// table rows (<w:tr>) end a line; tabs (<w:tab>) and breaks (<w:br>/<w:cr>)
// map to their whitespace.
func parseWordText(rc io.Reader) (string, error) {
	dec := xml.NewDecoder(rc)
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("extract: docx: parse document.xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := writeWordStart(dec, t, &b); err != nil {
				return "", err
			}
		case xml.EndElement:
			if isWordLineBreak(t.Name.Local) {
				b.WriteByte('\n')
			}
		}
	}
	return normalizeText(b.String()), nil
}

// writeWordStart handles a WordprocessingML start element: <w:t> text runs are
// decoded and appended, <w:tab> and <w:br>/<w:cr> map to their whitespace.
func writeWordStart(dec *xml.Decoder, t xml.StartElement, b *strings.Builder) error {
	switch t.Name.Local {
	case "t":
		var s string
		if err := dec.DecodeElement(&s, &t); err != nil {
			return fmt.Errorf("extract: docx: decode text run: %w", err)
		}
		b.WriteString(s)
	case "tab":
		b.WriteByte('\t')
	case "br", "cr":
		b.WriteByte('\n')
	}
	return nil
}

// isWordLineBreak reports whether a WordprocessingML end element ends a line
// (paragraphs and table rows).
func isWordLineBreak(name string) bool {
	return name == "p" || name == "tr"
}

// extractXLSX renders each worksheet as a markdown-ish heading followed by
// tab-separated rows, so cell values stay searchable with their sheet context.
func extractXLSX(data []byte) (string, error) {
	if isOLECompound(data) {
		return "", ErrEncrypted // password-protected .xlsx is an OLE container, not a zip
	}

	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("extract: open xlsx: %w", err)
	}
	defer func() { _ = f.Close() }()

	var b strings.Builder
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			return "", fmt.Errorf("extract: xlsx: read sheet %q: %w", sheet, err)
		}
		b.WriteString("# ")
		b.WriteString(sheet)
		b.WriteByte('\n')
		for _, row := range rows {
			b.WriteString(strings.Join(row, "\t"))
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	return normalizeText(b.String()), nil
}

// zipEntry returns the archive entry with the exact name, or nil if absent.
func zipEntry(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// normalizeText trims trailing spaces from each line and collapses runs of blank
// lines into a single one, so extracted text stays compact for chunking without
// losing paragraph structure.
func normalizeText(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			blank++
			if blank > 1 {
				continue // keep at most one blank line between blocks
			}
		} else {
			blank = 0
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
