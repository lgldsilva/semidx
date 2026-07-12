package extract

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestCanBeEncrypted(t *testing.T) {
	for _, n := range []string{"a.pdf", "b.docx", "c.pptx", "d.xlsx"} {
		if !CanBeEncrypted(n) {
			t.Errorf("CanBeEncrypted(%q) = false", n)
		}
	}
	for _, n := range []string{"e.txt", "f.md", "g.csv", "h.jar"} {
		if CanBeEncrypted(n) {
			t.Errorf("CanBeEncrypted(%q) = true", n)
		}
	}
}

// TestExtractXLSXWithPassword round-trips a real encrypted workbook: excelize
// writes it password-protected, and ExtractAllWithPassword must decrypt+read it
// with the right password and reject the wrong one.
func TestExtractXLSXWithPassword(t *testing.T) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if err := f.SetCellValue("Sheet1", "A1", "quarterly revenue report"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := f.Write(&buf, excelize.Options{Password: "s3cret"}); err != nil {
		t.Fatalf("write encrypted xlsx: %v", err)
	}
	enc := buf.Bytes()
	if !isOLECompound(enc) {
		t.Fatal("encrypted xlsx should be an OLE compound file")
	}

	docs, err := ExtractAllWithPassword("book.xlsx", enc, "s3cret")
	if err != nil {
		t.Fatalf("correct password: %v", err)
	}
	if len(docs) != 1 || !strings.Contains(docs[0].Text, "quarterly revenue report") {
		t.Errorf("decrypted text missing: %+v", docs)
	}

	if _, err := ExtractAllWithPassword("book.xlsx", enc, "wrong"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong password = %v, want ErrWrongPassword", err)
	}
}

// Note on docx/pptx: they use the IDENTICAL excelize.Decrypt OOXML path that
// TestExtractXLSXWithPassword exercises (the switch groups .docx/.pptx/.xlsx),
// differing only in the post-decrypt parser (byExt[ext]), which the plain-file
// docx/pptx tests already cover. A synthetic encrypted-docx fixture is not used
// because excelize.Encrypt round-trips its own xlsx cleanly but not an arbitrary
// hand-built OOXML zip (an mscfb/minisector quirk of its encryptor, unrelated to
// the decrypt path used in production on real Word/PowerPoint files).

// TestExtractAllWithPasswordUnencrypted: a file that is not actually encrypted
// (or a type that can't be) just flows through the normal parser.
func TestExtractAllWithPasswordUnencrypted(t *testing.T) {
	docs, err := ExtractAllWithPassword("notes.txt", []byte("plain text notes"), "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || !strings.Contains(docs[0].Text, "plain text notes") {
		t.Errorf("passthrough failed: %+v", docs)
	}
	// A plain (unencrypted) .xlsx still opens even though a password was supplied.
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	_ = f.SetCellValue("Sheet1", "A1", "open workbook")
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	docs, err = ExtractAllWithPassword("plain.xlsx", buf.Bytes(), "unused")
	if err != nil || len(docs) != 1 || !strings.Contains(docs[0].Text, "open workbook") {
		t.Errorf("unencrypted xlsx with password arg: docs=%+v err=%v", docs, err)
	}
}
