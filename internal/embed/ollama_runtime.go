package embed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// probeTimeout bounds a runtime probe so `config list` / `doctor` stay snappy
// even when Ollama is down (unlike the generous embed HTTP timeout).
const probeTimeout = 2 * time.Second

// RunningModel is one model currently resident in an Ollama process
// (GET /api/ps). SizeVRAM > 0 means at least some weights are on GPU.
type RunningModel struct {
	Name     string
	Size     int64
	SizeVRAM int64
}

// VRAMPercent is the share of the model size reported in VRAM (0–100).
// Returns 0 when Size is unknown.
func (m RunningModel) VRAMPercent() int {
	if m.Size <= 0 {
		return 0
	}
	return int((m.SizeVRAM * 100) / m.Size)
}

// RuntimeProbe is the result of probing one Ollama base URL for resident models
// and inferring GPU vs CPU from size_vram (no nvidia-smi).
type RuntimeProbe struct {
	URL       string
	Reachable bool
	Err       string // set when !Reachable
	Models    []RunningModel
}

// Summary is a one-line human description for CLI output.
func (p RuntimeProbe) Summary() string {
	if !p.Reachable {
		if p.Err == "" {
			return "unreachable"
		}
		return "unreachable (" + p.Err + ")"
	}
	if len(p.Models) == 0 {
		return "reachable; no models loaded (GPU unknown until a model is resident)"
	}
	anyGPU := false
	parts := make([]string, 0, len(p.Models))
	for _, m := range p.Models {
		name := m.Name
		if name == "" {
			name = "(unnamed)"
		}
		pct := m.VRAMPercent()
		if m.SizeVRAM > 0 {
			anyGPU = true
			parts = append(parts, fmt.Sprintf("%s %d%% VRAM", name, pct))
		} else {
			parts = append(parts, fmt.Sprintf("%s CPU", name))
		}
	}
	joined := strings.Join(parts, "; ")
	if anyGPU {
		return "GPU: " + joined
	}
	return "CPU: " + joined
}

type psResponse struct {
	Models []struct {
		Name     string `json:"name"`
		Model    string `json:"model"`
		Size     int64  `json:"size"`
		SizeVRAM int64  `json:"size_vram"`
	} `json:"models"`
}

// ProbeOllamaRuntime calls GET {baseURL}/api/ps with a short timeout and
// classifies resident models by size_vram. Unreachable endpoints return
// Reachable=false with Err set; the error is never returned as a Go error so
// callers can print a soft status line.
func ProbeOllamaRuntime(ctx context.Context, baseURL string) RuntimeProbe {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	out := RuntimeProbe{URL: baseURL}
	if baseURL == "" {
		out.Err = "empty URL"
		return out
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/ps", nil)
	if err != nil {
		out.Err = err.Error()
		return out
	}

	client := &http.Client{Timeout: probeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		out.Err = truncateProbeErr(err)
		return out
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			out.Err = resp.Status
		} else {
			out.Err = resp.Status + ": " + msg
		}
		return out
	}

	var ps psResponse
	if err := json.NewDecoder(resp.Body).Decode(&ps); err != nil {
		out.Err = "invalid /api/ps JSON: " + err.Error()
		return out
	}

	out.Reachable = true
	out.Models = make([]RunningModel, 0, len(ps.Models))
	for _, m := range ps.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		out.Models = append(out.Models, RunningModel{
			Name:     name,
			Size:     m.Size,
			SizeVRAM: m.SizeVRAM,
		})
	}
	return out
}

// ProbeOllamaRuntimes probes each URL in order. Empty entries are skipped.
func ProbeOllamaRuntimes(ctx context.Context, urls []string) []RuntimeProbe {
	out := make([]RuntimeProbe, 0, len(urls))
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		out = append(out, ProbeOllamaRuntime(ctx, u))
	}
	return out
}

// OllamaProbeURLs returns the local Ollama endpoints to probe from config:
// SEMIDX_OLLAMA_URLS when set, otherwise the single SEMIDX_OLLAMA_URL.
func OllamaProbeURLs(ollamaURL string, ollamaURLs []string) []string {
	if len(ollamaURLs) > 0 {
		out := make([]string, 0, len(ollamaURLs))
		for _, u := range ollamaURLs {
			u = strings.TrimSpace(u)
			if u != "" {
				out = append(out, u)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if u := strings.TrimSpace(ollamaURL); u != "" {
		return []string{u}
	}
	return nil
}

func truncateProbeErr(err error) string {
	s := err.Error()
	// Strip long dial wrappers; keep the useful tail.
	if i := strings.LastIndex(s, ": "); i >= 0 && i+2 < len(s) {
		tail := s[i+2:]
		if len(tail) < len(s) && len(tail) > 0 && len(tail) < 120 {
			return tail
		}
	}
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}
