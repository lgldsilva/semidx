package extract

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/xuri/excelize/v2"
)

// ErrWrongPassword is returned when a password was supplied for an encrypted
// file but it did not decrypt it — distinct from ErrEncrypted (no/again-needed
// password). The unlock flow uses it to keep trying other candidate passwords.
var ErrWrongPassword = errors.New("extract: wrong password for encrypted file")

// CanBeEncrypted reports whether a file type is one this package can decrypt with
// a password (PDF and the OOXML Office formats). Used to decide whether an
// ErrEncrypted file is worth prompting a password for.
func CanBeEncrypted(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf", ".docx", ".pptx", ".xlsx":
		return true
	}
	return false
}

// ExtractAllWithPassword is ExtractAll for a password-protected file: it
// decrypts with the given password, then reuses the normal parser on the
// plaintext. A wrong password yields ErrWrongPassword; a file that is not
// actually encrypted falls through to ExtractAll. A panic in any decoder is
// recovered into an error so a malformed file never crashes the indexer.
func ExtractAllWithPassword(name string, data []byte, password string) (out []Doc, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("extract: decrypt decoder panicked: %v", r)
		}
	}()

	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".pdf":
		text, e := extractPDFWithPassword(data, password)
		if e != nil {
			return nil, e
		}
		return []Doc{{Path: name, Text: text}}, nil

	case ".docx", ".pptx", ".xlsx":
		if !isOLECompound(data) {
			return ExtractAll(name, data) // not actually encrypted — parse as-is
		}
		// Encrypted OOXML is an OLE compound file wrapping the real zip. excelize's
		// decryptor is document-agnostic (ECMA-376 agile/standard), so it yields the
		// inner OOXML zip for docx/pptx/xlsx alike; the existing parser then runs on
		// that plaintext zip.
		inner, e := excelize.Decrypt(data, &excelize.Options{Password: password})
		if e != nil || len(inner) == 0 {
			return nil, ErrWrongPassword
		}
		registryMu.RLock()
		decodeFn, ok := byExt[ext]
		registryMu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnsupported, ext)
		}
		text, e := decodeFn(inner)
		if e != nil {
			return nil, ErrWrongPassword // decrypt produced non-parseable bytes ⇒ wrong password
		}
		return []Doc{{Path: name, Text: text}}, nil

	default:
		return ExtractAll(name, data) // this type is never encrypted
	}
}

// extractPDFWithPassword opens an encrypted PDF with the user password. It first
// tries ledongthuc/pdf (RC4 + AES-128); for AES-256 (PDF 2.0, V5/R6), which that
// library rejects, it falls back to pdfcpu's decryptor.
func extractPDFWithPassword(data []byte, password string) (string, error) {
	used := false
	r, err := pdf.NewReaderEncrypted(bytes.NewReader(data), int64(len(data)), func() string {
		if used {
			return "" // stop: the single candidate password was already tried
		}
		used = true
		return password
	})
	switch {
	case err == nil:
		return pdfText(r)
	case errors.Is(err, pdf.ErrInvalidPassword):
		return "", ErrWrongPassword
	case isUnsupportedPDFCrypto(err):
		return extractPDFViaPdfcpu(data, password)
	default:
		return "", fmt.Errorf("extract: open encrypted pdf: %w", err)
	}
}

// isUnsupportedPDFCrypto reports whether the ledongthuc error means the cipher is
// beyond its support (AES-256 / PDF 2.0), so pdfcpu should be tried instead.
func isUnsupportedPDFCrypto(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "unsupported") && (strings.Contains(m, "encryption") ||
		strings.Contains(m, "r=6") || strings.Contains(m, "v=5"))
}

// extractPDFViaPdfcpu decrypts an AES-256 PDF with pdfcpu into plaintext bytes,
// then extracts text with the normal reader. A wrong password fails the decrypt.
func extractPDFViaPdfcpu(data []byte, password string) (string, error) {
	conf := model.NewDefaultConfiguration()
	conf.UserPW = password
	conf.OwnerPW = password
	var buf bytes.Buffer
	if err := api.Decrypt(bytes.NewReader(data), &buf, conf); err != nil {
		return "", ErrWrongPassword
	}
	return extractPDF(buf.Bytes())
}
