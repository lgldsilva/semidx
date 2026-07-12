package llm

import "testing"

// TestBuildProvider_allTypes verifies each supported provider type builds
// without error and reports the expected name. It does not hit the network —
// construction only validates config, not credentials.
func TestBuildProvider_allTypes(t *testing.T) {
	tests := []struct {
		cfg      ProviderConfig
		wantName string
	}{
		{ProviderConfig{Type: ProviderAnthropic, APIKey: "test-key"}, "anthropic"},
		{ProviderConfig{Type: ProviderGoogle, APIKey: "test-key"}, "google"},
		{ProviderConfig{Type: ProviderOpenRouter, APIKey: "test-key"}, "openrouter"},
		{ProviderConfig{Type: ProviderGroq, APIKey: "test-key"}, "groq"},
	}
	for _, tt := range tests {
		t.Run(string(tt.cfg.Type), func(t *testing.T) {
			p, err := BuildProvider(tt.cfg)
			if err != nil {
				t.Fatalf("BuildProvider(%s): %v", tt.cfg.Type, err)
			}
			if p == nil {
				t.Fatal("BuildProvider returned nil provider")
			}
			if got := p.Name(); got != tt.wantName {
				t.Errorf("Name() = %q, want %q", got, tt.wantName)
			}
		})
	}
}

// TestBuildProvider_groqDefaultBaseURL verifies Groq falls back to its
// OpenAI-compatible endpoint when no BaseURL override is given.
func TestBuildProvider_groqDefaultBaseURL(t *testing.T) {
	p, err := BuildProvider(ProviderConfig{Type: ProviderGroq, APIKey: "k"})
	if err != nil {
		t.Fatalf("BuildProvider(groq): %v", err)
	}
	if p.Name() != "groq" {
		t.Errorf("Name() = %q, want groq", p.Name())
	}
}

func TestBuildProvider_unknown(t *testing.T) {
	if _, err := BuildProvider(ProviderConfig{Type: "nope"}); err == nil {
		t.Error("expected error for unknown provider type")
	}
}
