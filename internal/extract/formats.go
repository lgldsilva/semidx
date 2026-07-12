package extract

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

// This file adds pure-Go extractors for more document families, registered in
// byExt (extract.go): PowerPoint (.pptx), OpenDocument (.odt/.ods/.odp), EPUB,
// Jupyter notebooks (.ipynb) and RTF. Plain-text families (.csv/.tsv/.log/…) use
// passthrough and are registered there too.

// extractPPTX joins the text of every slide in a .pptx. Slides live at
// ppt/slides/slideN.xml and their runs are <a:t> inside <a:p> — the same local
// element names WordprocessingML uses, so parseWordText (which matches by local
// name, ignoring the namespace prefix) decodes a slide unchanged.
func extractPPTX(data []byte) (string, error) {
	if isOLECompound(data) {
		return "", ErrEncrypted // password-protected OOXML is an OLE container
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("extract: open pptx: %w", err)
	}
	var slides []*zip.File
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			slides = append(slides, f)
		}
	}
	sort.Slice(slides, func(i, j int) bool { return slideNumber(slides[i].Name) < slideNumber(slides[j].Name) })

	var b strings.Builder
	for _, f := range slides {
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("extract: pptx: open %s: %w", f.Name, err)
		}
		text, perr := parseWordText(rc)
		_ = rc.Close()
		if perr != nil {
			return "", perr
		}
		if text != "" {
			b.WriteString(text)
			b.WriteByte('\n')
		}
	}
	return normalizeText(b.String()), nil
}

// slideNumber extracts the integer N from "ppt/slides/slideN.xml" for ordering
// (10 must sort after 2); unparseable names sort last.
func slideNumber(name string) int {
	base := strings.TrimSuffix(path.Base(name), ".xml")
	base = strings.TrimPrefix(base, "slide")
	n, err := strconv.Atoi(base)
	if err != nil {
		return 1 << 30
	}
	return n
}

// extractOpenDocument reads content.xml from an ODF container (.odt/.ods/.odp)
// and collects its visible text, breaking a line at paragraph/heading/table-row
// boundaries. All three share the same content.xml layout.
func extractOpenDocument(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("extract: open opendocument: %w", err)
	}
	// An encrypted ODF marks entries in META-INF/manifest.xml with a
	// manifest:encryption-data element; content.xml is then ciphertext.
	content := zipEntry(zr, "content.xml")
	if content == nil {
		return "", fmt.Errorf("extract: opendocument: missing content.xml")
	}
	rc, err := content.Open()
	if err != nil {
		return "", fmt.Errorf("extract: opendocument: open content.xml: %w", err)
	}
	defer func() { _ = rc.Close() }()

	return parseODFText(rc)
}

// parseODFText streams an ODF content.xml and collects its visible text,
// breaking a line at paragraph/heading/table-row/list-item boundaries and
// emitting a tab for <text:tab>.
func parseODFText(rc io.Reader) (string, error) {
	dec := xml.NewDecoder(rc)
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("extract: opendocument: parse content.xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			if t.Name.Local == "tab" {
				b.WriteByte('\t')
			}
		case xml.EndElement:
			if isODFLineBreak(t.Name.Local) {
				b.WriteByte('\n')
			}
		}
	}
	return normalizeText(b.String()), nil
}

// isODFLineBreak reports whether an ODF end element ends a line.
func isODFLineBreak(name string) bool {
	switch name {
	case "p", "h", "table-row", "list-item":
		return true
	default:
		return false
	}
}

// extractEPUB flattens an .epub (a zip of XHTML documents) to text by stripping
// the tags of every (x)html entry, in filename order. The container's spine
// ordering is skipped for simplicity — filename order is close enough for search.
func extractEPUB(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("extract: open epub: %w", err)
	}
	var docs []*zip.File
	for _, f := range zr.File {
		switch strings.ToLower(path.Ext(f.Name)) {
		case ".xhtml", ".html", ".htm":
			docs = append(docs, f)
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })

	var b strings.Builder
	for _, f := range docs {
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("extract: epub: open %s: %w", f.Name, err)
		}
		raw, rerr := io.ReadAll(rc)
		_ = rc.Close()
		if rerr != nil {
			return "", fmt.Errorf("extract: epub: read %s: %w", f.Name, rerr)
		}
		text, herr := extractHTML(raw)
		if herr != nil {
			continue // skip a malformed chapter rather than failing the book
		}
		if text != "" {
			b.WriteString(text)
			b.WriteByte('\n')
		}
	}
	return normalizeText(b.String()), nil
}

// extractIPYNB reads a Jupyter notebook (JSON) cell-aware: it concatenates the
// source of markdown and code cells (skipping outputs), so the searchable text
// is the notebook's authored content, not execution noise.
func extractIPYNB(data []byte) (string, error) {
	var nb struct {
		Cells []struct {
			CellType string          `json:"cell_type"`
			Source   json.RawMessage `json:"source"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(data, &nb); err != nil {
		return "", fmt.Errorf("extract: ipynb: parse json: %w", err)
	}
	var b strings.Builder
	for _, c := range nb.Cells {
		if c.CellType != "code" && c.CellType != "markdown" {
			continue
		}
		src, err := decodeNotebookSource(c.Source)
		if err != nil {
			return "", fmt.Errorf("extract: ipynb: cell source: %w", err)
		}
		if strings.TrimSpace(src) == "" {
			continue
		}
		b.WriteString(src)
		b.WriteString("\n\n")
	}
	return normalizeText(b.String()), nil
}

// decodeNotebookSource handles both shapes nbformat allows for a cell's source:
// an array of line strings, or a single string.
func decodeNotebookSource(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var lines []string
	if err := json.Unmarshal(raw, &lines); err == nil {
		return strings.Join(lines, ""), nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	return "", fmt.Errorf("unexpected source shape")
}

// extractRTF strips an RTF document to plain text. It skips ignorable
// destinations ({\*...}) and the non-content tables (font/color/stylesheet/
// info/pict), decodes \'hh hex escapes, and maps \par/\line/\tab to whitespace.
// It is a pragmatic reader for common editor output (Word/WordPad), not a full
// RTF parser.
func extractRTF(data []byte) (string, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte(`{\rtf`)) {
		return "", fmt.Errorf("extract: not an rtf document")
	}
	p := &rtfParser{s: data, skipTo: -1}
	return p.parse(), nil
}

// rtfParser holds the state of the RTF scan: the current byte cursor (i), the
// brace-group depth, and skipTo — the group depth to stop skipping at, or -1
// when not inside a non-content destination.
type rtfParser struct {
	s      []byte
	b      strings.Builder
	skipTo int
	depth  int
	i      int
}

// parse scans the whole document and returns the normalized plain text.
func (p *rtfParser) parse() string {
	for p.i < len(p.s) {
		switch c := p.s[p.i]; c {
		case '{':
			p.depth++
			p.i++
		case '}':
			p.closeGroup()
		case '\\':
			p.handleEscape()
		case '\r', '\n':
			p.i++
		default:
			if p.skipTo < 0 {
				p.b.WriteByte(c)
			}
			p.i++
		}
	}
	return normalizeText(p.b.String())
}

// closeGroup ends a brace group, leaving skip mode if this is the group we were
// skipping to.
func (p *rtfParser) closeGroup() {
	if p.skipTo >= 0 && p.depth == p.skipTo {
		p.skipTo = -1
	}
	p.depth--
	p.i++
}

// handleEscape dispatches a backslash escape: a literal brace/backslash, an
// ignorable destination, a \'hh hex byte, or a control word.
func (p *rtfParser) handleEscape() {
	if p.tryLiteralEscape() || p.tryIgnorableDest() || p.tryHexEscape() {
		return
	}
	word, next := rtfControlWord(p.s, p.i+1)
	p.i = next
	if p.skipTo < 0 {
		p.applyControlWord(word)
	}
}

// tryLiteralEscape handles \\, \{ and \} (an escaped backslash or brace).
func (p *rtfParser) tryLiteralEscape() bool {
	if p.i+1 >= len(p.s) {
		return false
	}
	switch p.s[p.i+1] {
	case '\\', '{', '}':
		if p.skipTo < 0 {
			p.b.WriteByte(p.s[p.i+1])
		}
		p.i += 2
		return true
	default:
		return false
	}
}

// tryIgnorableDest handles \* — an ignorable destination whose whole enclosing
// group is skipped.
func (p *rtfParser) tryIgnorableDest() bool {
	if p.i+1 < len(p.s) && p.s[p.i+1] == '*' {
		if p.skipTo < 0 {
			p.skipTo = p.depth
		}
		p.i += 2
		return true
	}
	return false
}

// tryHexEscape handles \'hh, a byte in the document codepage; it is decoded as
// Latin-1 (rune == byte) into UTF-8, which covers the common range.
func (p *rtfParser) tryHexEscape() bool {
	if p.i+3 < len(p.s) && p.s[p.i+1] == '\'' {
		if v, err := strconv.ParseInt(string(p.s[p.i+2:p.i+4]), 16, 32); err == nil && p.skipTo < 0 {
			p.b.WriteRune(rune(v))
		}
		p.i += 4
		return true
	}
	return false
}

// applyControlWord maps a control word to whitespace, or enters skip mode for a
// destination that holds no body text.
func (p *rtfParser) applyControlWord(word string) {
	switch word {
	case "par", "line", "sect", "page", "row":
		p.b.WriteByte('\n')
	case "tab", "cell":
		p.b.WriteByte('\t')
	case "fonttbl", "colortbl", "stylesheet", "info", "pict", "header", "footer":
		p.skipTo = p.depth // these destinations hold no body text
	}
}

// rtfControlWord reads a control word starting at i (just past the backslash):
// letters, then an optional numeric parameter, then an optional delimiter space
// (which is consumed). Returns the word and the index just past it.
func rtfControlWord(s []byte, i int) (string, int) {
	start := i
	for i < len(s) && isASCIILetter(s[i]) {
		i++
	}
	word := string(s[start:i])
	for i < len(s) && (s[i] == '-' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i < len(s) && s[i] == ' ' {
		i++ // the single trailing space is the control-word delimiter, not text
	}
	return word, i
}

func isASCIILetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
