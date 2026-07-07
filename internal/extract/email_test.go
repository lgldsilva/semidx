package extract

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestExtractEMLSimple(t *testing.T) {
	eml := "From: alice@example.com\r\n" +
		"To: bob@example.com\r\n" +
		"Subject: Hello\r\n" +
		"Date: Mon, 1 Jan 2024 10:00:00 +0000\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"This is the email body.\r\n"

	got, err := Extract("msg.eml", []byte(eml))
	if err != nil {
		t.Fatalf("Extract eml: unexpected error: %v", err)
	}
	for _, want := range []string{"From: alice@example.com", "To: bob@example.com", "Subject: Hello", "This is the email body"} {
		if !strings.Contains(got, want) {
			t.Errorf("eml missing %q in:\n%s", want, got)
		}
	}
}

func TestExtractEMLMultipartAlternative(t *testing.T) {
	boundary := "==boundary123=="
	eml := "From: sender@test.com\r\n" +
		"To: recipient@test.com\r\n" +
		"Subject: Multipart Test\r\n" +
		"Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n" +
		"\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"Plain text version\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/html\r\n" +
		"\r\n" +
		"<html><body><h1>HTML version</h1><p>with formatting</p></body></html>\r\n" +
		"--" + boundary + "--\r\n"

	got, err := Extract("msg.eml", []byte(eml))
	if err != nil {
		t.Fatalf("Extract eml: unexpected error: %v", err)
	}
	// text/plain is preferred over text/html
	if !strings.Contains(got, "Plain text version") {
		t.Errorf("eml missing plain text, got:\n%s", got)
	}
	if strings.Contains(got, "HTML version") {
		t.Errorf("eml should prefer plain text over HTML, got:\n%s", got)
	}
}

func TestExtractEMLMultipartMixed(t *testing.T) {
	boundary := "==next=="
	eml := "From: dev@example.com\r\n" +
		"Subject: With attachment\r\n" +
		"Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n" +
		"\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"Body text here\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"data.bin\"\r\n" +
		"\r\n" +
		"binary-data-should-be-skipped\r\n" +
		"--" + boundary + "--\r\n"

	got, err := Extract("msg.eml", []byte(eml))
	if err != nil {
		t.Fatalf("Extract eml: unexpected error: %v", err)
	}
	if !strings.Contains(got, "Body text here") {
		t.Errorf("eml missing body text: %q", got)
	}
}

func TestExtractEMLBase64(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte("Decoded base64 content"))
	eml := "From: test@test.com\r\n" +
		"Subject: Base64 Test\r\n" +
		"Content-Type: text/plain\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		body + "\r\n"

	got, err := Extract("msg.eml", []byte(eml))
	if err != nil {
		t.Fatalf("Extract eml: unexpected error: %v", err)
	}
	if !strings.Contains(got, "Decoded base64 content") {
		t.Errorf("eml base64 decoding failed, got:\n%s", got)
	}
}

func TestExtractEMLQuotedPrintable(t *testing.T) {
	eml := "From: test@test.com\r\n" +
		"Subject: QP Test\r\n" +
		"Content-Type: text/plain\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"\r\n" +
		"J'ai d=C3=A9couvert le caf=C3=A9!\r\n"

	got, err := Extract("msg.eml", []byte(eml))
	if err != nil {
		t.Fatalf("Extract eml: unexpected error: %v", err)
	}
	if !strings.Contains(got, "J'ai découvert le café!") {
		t.Errorf("eml quoted-printable decoding failed, got:\n%s", got)
	}
}

func TestExtractEMLEncodedSubject(t *testing.T) {
	eml := "From: sender@example.com\r\n" +
		"Subject: =?UTF-8?Q?Caf=C3=A9_R=C3=A9sum=C3=A9?=\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"Body\r\n"

	got, err := Extract("msg.eml", []byte(eml))
	if err != nil {
		t.Fatalf("Extract eml: unexpected error: %v", err)
	}
	if !strings.Contains(got, "Café Résumé") {
		t.Errorf("eml encoded subject failed, got:\n%s", got)
	}
}

func TestExtractEMLNoHeaders(t *testing.T) {
	eml := "Content-Type: text/plain\r\n" +
		"\r\n" +
		"Just body, no headers\r\n"

	got, err := Extract("msg.eml", []byte(eml))
	if err != nil {
		t.Fatalf("Extract eml: unexpected error: %v", err)
	}
	if !strings.Contains(got, "Just body") {
		t.Errorf("eml body missing: %q", got)
	}
}

func TestExtractEMLHTMLOnly(t *testing.T) {
	eml := "Subject: HTML Only\r\n" +
		"Content-Type: text/html\r\n" +
		"\r\n" +
		"<html><body><p>Hello <b>world</b>!</p></body></html>\r\n"

	got, err := Extract("msg.eml", []byte(eml))
	if err != nil {
		t.Fatalf("Extract eml: unexpected error: %v", err)
	}
	if !strings.Contains(got, "Hello world") {
		t.Errorf("eml HTML stripping failed, got:\n%s", got)
	}
}

func TestExtractEMLInvalid(t *testing.T) {
	// Totally invalid input must not panic.
	_, err := Extract("msg.eml", []byte("not an email at all\r\n"))
	if err == nil {
		t.Error("invalid eml should error")
	}
}

func TestExtractEMLCorruptAndEdge(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"garbage", []byte{0xff, 0xfe, 0x00}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Must not panic.
			if _, err := Extract("msg.eml", tc.data); err == nil {
				t.Errorf("corrupt eml should error")
			}
		})
	}
}

func TestExtractEMLPreservesSubjectLine(t *testing.T) {
	// Regression: subject line must appear with "Subject:" prefix so it is
	// searchable as metadata.
	eml := fmt.Sprintf("From: a@b.com\r\nSubject: %s\r\n\r\nbody\r\n", strings.Repeat("x", 200))
	got, err := Extract("msg.eml", []byte(eml))
	if err != nil {
		t.Fatalf("Extract eml: unexpected error: %v", err)
	}
	if !strings.Contains(got, "Subject:") {
		t.Errorf("eml missing Subject: header: %q", got)
	}
}
