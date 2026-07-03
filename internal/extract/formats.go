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
	var content *zip.File
	for _, f := range zr.File {
		if f.Name == "content.xml" {
			content = f
			break
		}
	}
	if content == nil {
		return "", fmt.Errorf("extract: opendocument: missing content.xml")
	}
	rc, err := content.Open()
	if err != nil {
		return "", fmt.Errorf("extract: opendocument: open content.xml: %w", err)
	}
	defer func() { _ = rc.Close() }()

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
			switch t.Name.Local {
			case "p", "h", "table-row", "list-item":
				b.WriteByte('\n')
			}
		}
	}
	return normalizeText(b.String()), nil
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
	s := data
	if !bytes.HasPrefix(bytes.TrimSpace(s), []byte(`{\rtf`)) {
		return "", fmt.Errorf("extract: not an rtf document")
	}
	// skipDepth > 0 means we are inside a destination whose text is not content.
	var b strings.Builder
	skipTo := -1 // group depth to stop skipping at; -1 = not skipping
	depth := 0
	i := 0
	for i < len(s) {
		switch c := s[i]; c {
		case '{':
			depth++
			i++
		case '}':
			if skipTo >= 0 && depth == skipTo {
				skipTo = -1
			}
			depth--
			i++
		case '\\':
			if i+1 < len(s) && (s[i+1] == '\\' || s[i+1] == '{' || s[i+1] == '}') {
				if skipTo < 0 {
					b.WriteByte(s[i+1])
				}
				i += 2
				continue
			}
			if i+1 < len(s) && s[i+1] == '*' {
				// ignorable destination: skip the whole enclosing group
				if skipTo < 0 {
					skipTo = depth
				}
				i += 2
				continue
			}
			if i+3 < len(s) && s[i+1] == '\'' {
				if v, err := strconv.ParseInt(string(s[i+2:i+4]), 16, 32); err == nil && skipTo < 0 {
					// \'hh is a byte in the document codepage; decode as Latin-1
					// (rune == byte) into UTF-8, which covers the common range.
					b.WriteRune(rune(v))
				}
				i += 4
				continue
			}
			word, next := rtfControlWord(s, i+1)
			i = next
			if skipTo >= 0 {
				continue
			}
			switch word {
			case "par", "line", "sect", "page", "row":
				b.WriteByte('\n')
			case "tab", "cell":
				b.WriteByte('\t')
			case "fonttbl", "colortbl", "stylesheet", "info", "pict", "header", "footer":
				skipTo = depth // these destinations hold no body text
			}
		case '\r', '\n':
			i++
		default:
			if skipTo < 0 {
				b.WriteByte(c)
			}
			i++
		}
	}
	return normalizeText(b.String()), nil
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
