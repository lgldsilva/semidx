package extract

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
)

const maxEmailSize = 1 << 20 // 1 MiB

func init() {
	Register(".eml", extractEML)
}

// extractEML parses an .eml (RFC 5322) message and returns a plain-text
// representation with headers followed by the body text. The format is:
//
//	From: sender@example.com
//	To: recipient@example.com
//	Subject: Hello
//	Date: Mon, 1 Jan 2024 00:00:00 +0000
//
//	<body text>
//
// Headers are included so that sender/subject/date are searchable alongside
// the body. Multipart messages are walked for text/plain and text/html parts;
// binary attachments are skipped. The message is capped at 1 MiB.
func extractEML(data []byte) (string, error) {
	if len(data) > maxEmailSize {
		data = data[:maxEmailSize]
	}

	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("extract: eml: parse message: %w", err)
	}

	var hdr strings.Builder
	if from := msg.Header.Get("From"); from != "" {
		hdr.WriteString("From: ")
		hdr.WriteString(decodeHeader(from))
		hdr.WriteByte('\n')
	}
	if to := msg.Header.Get("To"); to != "" {
		hdr.WriteString("To: ")
		hdr.WriteString(decodeHeader(to))
		hdr.WriteByte('\n')
	}
	if subject := msg.Header.Get("Subject"); subject != "" {
		hdr.WriteString("Subject: ")
		hdr.WriteString(decodeHeader(subject))
		hdr.WriteByte('\n')
	}
	if date := msg.Header.Get("Date"); date != "" {
		hdr.WriteString("Date: ")
		hdr.WriteString(date)
		hdr.WriteByte('\n')
	}

	body, err := extractEmailBody(msg.Header, msg.Body)
	if err != nil {
		return "", err
	}

	text := hdr.String()
	if body != "" {
		text += "\n" + body
	}
	return normalizeText(text), nil
}

// decodeHeader decodes an RFC 2047 encoded-word header value (e.g. "=?UTF-8?Q?=C3=A1?=").
func decodeHeader(s string) string {
	dec := &mime.WordDecoder{}
	decoded, err := dec.DecodeHeader(s)
	if err != nil {
		return s // fall back to raw on unparseable encoding
	}
	return decoded
}

// extractEmailBody walks the MIME structure of an email to extract readable text.
// It prefers text/plain over text/html and handles multipart/alternative,
// multipart/mixed, and simple (non-multipart) messages.
func extractEmailBody(hdr mail.Header, body io.Reader) (string, error) {
	contentType := hdr.Get("Content-Type")
	if contentType == "" {
		raw, err := io.ReadAll(io.LimitReader(body, maxEmailSize))
		if err != nil {
			return "", fmt.Errorf("extract: eml: read body: %w", err)
		}
		return string(raw), nil
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		raw, rerr := io.ReadAll(io.LimitReader(body, maxEmailSize))
		if rerr != nil {
			return "", fmt.Errorf("extract: eml: read body: %w", rerr)
		}
		return string(raw), nil
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		return extractEmailMultipartBody(body, params["boundary"])
	}

	// Simple (non-multipart) body.
	cte := hdr.Get("Content-Transfer-Encoding")
	return decodeEmailPart(body, cte, mediaType)
}

// extractEmailMultipartBody walks the multipart structure of an email. It
// recursively searches all parts for the best text representation:
// text/plain is preferred over text/html.
func extractEmailMultipartBody(body io.Reader, boundary string) (string, error) {
	if boundary == "" {
		return "", nil
	}

	mr := multipart.NewReader(body, boundary)
	var plainText, htmlText string

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // skip malformed parts
		}

		partCT := part.Header.Get("Content-Type")
		partMedia, partParams, perr := mime.ParseMediaType(partCT)
		if perr != nil {
			continue
		}

		if strings.HasPrefix(partMedia, "multipart/") {
			inner, ierr := extractEmailMultipartBody(part, partParams["boundary"])
			if ierr == nil {
				if plainText == "" {
					plainText = inner
				}
			}
			continue
		}

		cte := part.Header.Get("Content-Transfer-Encoding")

		switch partMedia {
		case "text/plain":
			if plainText == "" {
				txt, derr := decodeEmailPart(part, cte, partMedia)
				if derr == nil {
					plainText = txt
				}
			}
		case "text/html":
			if htmlText == "" {
				txt, derr := decodeEmailPart(part, cte, partMedia)
				if derr == nil {
					htmlText = txt
				}
			}
		default:
			// Skip binary attachments and unknown types.
		}
	}

	if plainText != "" {
		return plainText, nil
	}
	if htmlText != "" {
		return htmlText, nil
	}
	return "", nil
}

// decodeEmailPart reads a MIME part body, decodes Content-Transfer-Encoding,
// and for text/html strips HTML tags.
func decodeEmailPart(r io.Reader, cte, mediaType string) (string, error) {
	var decoded io.Reader
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "quoted-printable":
		decoded = quotedprintable.NewReader(r)
	case "base64":
		decoded = base64.NewDecoder(base64.StdEncoding, &newlineStripper{src: r})
	case "7bit", "8bit", "binary", "":
		decoded = r
	default:
		decoded = r
	}

	raw, err := io.ReadAll(io.LimitReader(decoded, maxEmailSize))
	if err != nil {
		return "", fmt.Errorf("extract: eml: decode part: %w", err)
	}
	if len(raw) == 0 {
		return "", nil
	}

	text := string(raw)
	if strings.HasPrefix(mediaType, "text/html") {
		stripped, hErr := extractHTML([]byte(text))
		if hErr != nil {
			text = stripHTMLTags(text)
		} else {
			text = stripped
		}
	}
	return text, nil
}

// newlineStripper removes \r and \n from the stream so base64 decoder gets a
// contiguous payload (email base64 often wraps at 76 columns).
type newlineStripper struct {
	src io.Reader
}

func (n *newlineStripper) Read(p []byte) (int, error) {
	raw := make([]byte, len(p))
	total, err := n.src.Read(raw)
	if total == 0 {
		return 0, err
	}
	j := 0
	for i := 0; i < total; i++ {
		if raw[i] != '\r' && raw[i] != '\n' {
			p[j] = raw[i]
			j++
		}
	}
	return j, err
}

// stripHTMLTags is a simple fallback that removes HTML tags when the full
// HTML parser fails.
func stripHTMLTags(s string) string {
	var b strings.Builder
	inTag := false
	inEntity := false
	var entityBuf strings.Builder
	for _, r := range s {
		switch {
		case inTag:
			if r == '>' {
				inTag = false
			}
		case inEntity:
			if r == ';' {
				inEntity = false
				entity := entityBuf.String()
				entityBuf.Reset()
				if decoded, ok := htmlEntities[entity]; ok {
					b.WriteRune(decoded)
				}
				continue
			}
			entityBuf.WriteRune(r)
		case r == '<':
			inTag = true
		case r == '&':
			inEntity = true
			entityBuf.Reset()
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// htmlEntities maps common HTML entities to their runes.
var htmlEntities = map[string]rune{
	"amp":  '&',
	"lt":   '<',
	"gt":   '>',
	"quot": '"',
	"apos": '\'',
	"nbsp": '\u00A0',
	"copy": '\u00A9',
	"reg":  '\u00AE',
}
