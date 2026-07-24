package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeOllamaRuntimeGPU(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			t.Errorf("path = %q, want /api/ps", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"nomic-embed-text:latest","model":"nomic-embed-text:latest","size":274000000,"size_vram":274000000}]}`))
	}))
	defer srv.Close()

	p := ProbeOllamaRuntime(context.Background(), srv.URL)
	if !p.Reachable {
		t.Fatalf("reachable = false, err=%q", p.Err)
	}
	if len(p.Models) != 1 || p.Models[0].VRAMPercent() != 100 {
		t.Fatalf("models = %+v", p.Models)
	}
	if !strings.Contains(p.Summary(), "GPU:") || !strings.Contains(p.Summary(), "100% VRAM") {
		t.Errorf("Summary = %q", p.Summary())
	}
}

func TestProbeOllamaRuntimeCPU(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"name":"bge-m3","size":1000,"size_vram":0}]}`))
	}))
	defer srv.Close()

	p := ProbeOllamaRuntime(context.Background(), srv.URL)
	if !p.Reachable || !strings.HasPrefix(p.Summary(), "CPU:") {
		t.Fatalf("Summary = %q (reachable=%v)", p.Summary(), p.Reachable)
	}
}

func TestProbeOllamaRuntimeIdle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	p := ProbeOllamaRuntime(context.Background(), srv.URL)
	if !p.Reachable {
		t.Fatalf("err = %q", p.Err)
	}
	if !strings.Contains(p.Summary(), "GPU unknown") {
		t.Errorf("Summary = %q", p.Summary())
	}
}

func TestProbeOllamaRuntimeUnreachable(t *testing.T) {
	p := ProbeOllamaRuntime(context.Background(), "http://127.0.0.1:1")
	if p.Reachable {
		t.Fatal("expected unreachable")
	}
	if !strings.Contains(p.Summary(), "unreachable") {
		t.Errorf("Summary = %q", p.Summary())
	}
}

func TestProbeOllamaRuntimeHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := ProbeOllamaRuntime(context.Background(), srv.URL)
	if p.Reachable {
		t.Fatal("expected unreachable on 500")
	}
	if !strings.Contains(p.Err, "500") {
		t.Errorf("Err = %q", p.Err)
	}
}

func TestProbeOllamaRuntimeEmptyURL(t *testing.T) {
	p := ProbeOllamaRuntime(context.Background(), "  ")
	if p.Reachable || p.Err == "" {
		t.Fatalf("probe = %+v", p)
	}
}

func TestProbeOllamaRuntimesAndURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	urls := OllamaProbeURLs("http://single:11434", []string{"", srv.URL, "  "})
	if len(urls) != 1 || urls[0] != srv.URL {
		t.Fatalf("OllamaProbeURLs with OllamaURLs = %v", urls)
	}
	single := OllamaProbeURLs("http://single:11434", nil)
	if len(single) != 1 || single[0] != "http://single:11434" {
		t.Fatalf("single = %v", single)
	}
	if OllamaProbeURLs("", nil) != nil {
		t.Fatal("empty config should yield nil URLs")
	}

	probes := ProbeOllamaRuntimes(context.Background(), []string{"", srv.URL})
	if len(probes) != 1 || !probes[0].Reachable {
		t.Fatalf("probes = %+v", probes)
	}
}

func TestRunningModelVRAMPercent(t *testing.T) {
	if (RunningModel{Size: 0, SizeVRAM: 10}).VRAMPercent() != 0 {
		t.Error("zero size should yield 0%")
	}
	if (RunningModel{Size: 200, SizeVRAM: 50}).VRAMPercent() != 25 {
		t.Error("want 25%")
	}
}
