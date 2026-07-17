package extract

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

var xmlTagRx = regexp.MustCompile(`<[^>]*>`)

func init() {
	_ = Register(".xml", extractXML)
}

// extractXML strips XML tags and returns the text content between them. It first
// tries the Go encoding/xml decoder for a well-formed parse; if that fails it
// falls back to a simple regex-based tag-stripper so non-well-formed XML is still
// searchable.
func extractXML(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}

	text, err := stripXMLViaDecoder(data)
	if err == nil {
		return normalizeText(text), nil
	}
	// Non-well-formed XML: strip tags via regex (accept <...>), keep everything else.
	text = xmlTagRx.ReplaceAllString(string(data), "")
	text = strings.TrimSpace(text)
	return normalizeText(text), nil
}

// stripXMLViaDecoder uses the xml decoder to walk tokens and collect CharData,
// skipping comments, processing instructions, and CDATA boundaries.
func stripXMLViaDecoder(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false  // be lenient with namespace quirks
	dec.AutoClose = nil // don't auto-close anything
	dec.Entity = xml.HTMLEntity

	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("extract: xml decode: %w", err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			text := bytes.TrimSpace(t)
			if len(text) > 0 {
				if b.Len() > 0 {
					b.WriteByte(' ')
				}
				b.Write(text)
			}
		case xml.EndElement:
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}
