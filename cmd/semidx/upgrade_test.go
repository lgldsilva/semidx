package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTarGz builds a minimal release tar.gz containing a single `name` entry.
func makeTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// makeZip builds a minimal release zip containing a single `name` entry.
func makeZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// releaseServer serves the Gitea release layout: /releases/latest (API) and
// /releases/download/<tag>/{archive,checksums.txt}.
func releaseServer(t *testing.T, tag string, archives map[string][]byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"tag_name":%q}`, tag)
	})
	var sums strings.Builder
	for name, data := range archives {
		fmt.Fprintf(&sums, "%s  %s\n", sha256hex(data), name)
	}
	mux.HandleFunc("/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sums.String()))
	})
	for name, data := range archives {
		data := data
		mux.HandleFunc("/releases/download/"+tag+"/"+name, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(data)
		})
	}
	return httptest.NewServer(mux)
}

func TestFetchLatestTag(t *testing.T) {
	srv := releaseServer(t, "v1.4.0", nil)
	defer srv.Close()

	got, err := fetchLatestTag(context.Background(), srv.Client(), srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "v1.4.0" {
		t.Fatalf("tag = %q, want v1.4.0", got)
	}
}

// A private release host rejects unauthenticated reads; the token must be sent.
func TestFetchLatestTag_WithToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token SEKRET" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprint(w, `{"tag_name":"v9.9.9"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := fetchLatestTag(context.Background(), srv.Client(), srv.URL, ""); err == nil {
		t.Fatal("expected 404 without a token")
	}
	got, err := fetchLatestTag(context.Background(), srv.Client(), srv.URL, "SEKRET")
	if err != nil {
		t.Fatal(err)
	}
	if got != "v9.9.9" {
		t.Fatalf("tag = %q, want v9.9.9", got)
	}
}

func TestDownloadReleaseBinary_TarGz(t *testing.T) {
	tag := "v2.0.0"
	want := []byte("#!/bin/sh\necho semidx\n")
	archive := "semidx_2.0.0_linux_amd64.tar.gz"
	srv := releaseServer(t, tag, map[string][]byte{archive: makeTarGz(t, "semidx", want)})
	defer srv.Close()

	got, err := downloadReleaseBinary(context.Background(), srv.Client(), srv.URL+"/releases/download", tag, "linux", "amd64", "")
	if err != nil {
		t.Fatalf("downloadReleaseBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("binary = %q, want %q", got, want)
	}
}

func TestDownloadReleaseBinary_Zip(t *testing.T) {
	tag := "v2.0.0"
	want := []byte("MZ...windows binary...")
	archive := "semidx_2.0.0_windows_amd64.zip"
	srv := releaseServer(t, tag, map[string][]byte{archive: makeZip(t, "semidx.exe", want)})
	defer srv.Close()

	got, err := downloadReleaseBinary(context.Background(), srv.Client(), srv.URL+"/releases/download", tag, "windows", "amd64", "")
	if err != nil {
		t.Fatalf("downloadReleaseBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("binary = %q, want %q", got, want)
	}
}

// A tampered archive whose bytes don't match checksums.txt must be rejected.
func TestDownloadReleaseBinary_ChecksumMismatch(t *testing.T) {
	tag := "v3.0.0"
	archive := "semidx_3.0.0_linux_arm64.tar.gz"
	good := makeTarGz(t, "semidx", []byte("real"))
	srv := releaseServer(t, tag, map[string][]byte{archive: good})
	defer srv.Close()

	// Serve a *different* archive body than the one checksums.txt was built from.
	tampered := makeTarGz(t, "semidx", []byte("evil"))
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n", sha256hex(good), archive)
	})
	mux.HandleFunc("/releases/download/"+tag+"/"+archive, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(tampered)
	})
	srv2 := httptest.NewServer(mux)
	defer srv2.Close()

	_, err := downloadReleaseBinary(context.Background(), srv2.Client(), srv2.URL+"/releases/download", tag, "linux", "arm64", "")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "aaa  semidx_1.0.0_linux_amd64.tar.gz\nbbb  semidx_1.0.0_darwin_arm64.tar.gz\n"
	if got := checksumFor(sums, "semidx_1.0.0_darwin_arm64.tar.gz"); got != "bbb" {
		t.Fatalf("checksumFor = %q, want bbb", got)
	}
	if got := checksumFor(sums, "missing.tar.gz"); got != "" {
		t.Fatalf("checksumFor(missing) = %q, want empty", got)
	}
}

func TestExtractSemidxBinary_NotFound(t *testing.T) {
	ar := makeTarGz(t, "README.md", []byte("no binary here"))
	if _, err := extractSemidxBinary(ar, "tar.gz", "linux"); err == nil {
		t.Fatal("expected error when the binary is absent from the archive")
	}
}

func TestReplaceBinaryAt(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "semidx")
	if err := os.WriteFile(exe, []byte("OLD BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinaryAt(exe, []byte("NEW BINARY")); err != nil {
		t.Fatalf("replaceBinaryAt: %v", err)
	}
	got, err := os.ReadFile(exe) // #nosec G304 -- test-controlled temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW BINARY" {
		t.Fatalf("binary content = %q, want NEW BINARY", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".semidx.upgrade.tmp")); !os.IsNotExist(err) {
		t.Fatalf("temp file not cleaned up: %v", err)
	}
}

func TestReplaceBinaryAt_UnwritableDir(t *testing.T) {
	if err := replaceBinaryAt(filepath.Join(t.TempDir(), "no-such-subdir", "semidx"), []byte("x")); err == nil {
		t.Fatal("expected an error writing into a non-existent directory")
	}
}

func TestUpdateHTTPClient_InsecureForHomelab(t *testing.T) {
	c := updateHTTPClient("https://gitea.raspberrypi.lan/api/v1/repos/x/y")
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("expected an insecure transport for a *.raspberrypi.lan host")
	}
}

func TestValueOr(t *testing.T) {
	if got := valueOr("SEMIDX_DEFINITELY_UNSET_KEY_XYZ", "fallback"); got != "fallback" {
		t.Fatalf("valueOr(unset) = %q, want fallback", got)
	}
}

func TestSameVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v1.2.3", "1.2.3", true},
		{"1.2.3", "v1.2.3", true},
		{"v1.2.3", "v1.2.4", false},
		{"dev", "v1.2.3", false}, // a dev build is never "current"
		{"", "v1.2.3", false},
	}
	for _, c := range cases {
		if got := sameVersion(c.a, c.b); got != c.want {
			t.Errorf("sameVersion(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
