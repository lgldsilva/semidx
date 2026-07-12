package agent

import "testing"

func TestCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		flags    Capability
		wantStr  string
		hasTrue  []Capability
		hasFalse []Capability
	}{
		{
			name:     "local-only (standalone CLI, no keys)",
			flags:    CapLocalGit | CapIndexLocal,
			wantStr:  "Capabilities{[local_git index_local]}",
			hasTrue:  []Capability{CapLocalGit, CapIndexLocal},
			hasFalse: []Capability{CapRemoteIndex, CapChatLLM, CapToolCalling},
		},
		{
			name:     "local with chat keys",
			flags:    CapLocalGit | CapIndexLocal | CapChatLLM | CapToolCalling,
			wantStr:  "Capabilities{[local_git index_local chat_llm tool_calling]}",
			hasTrue:  []Capability{CapLocalGit, CapIndexLocal, CapChatLLM, CapToolCalling},
			hasFalse: []Capability{CapRemoteIndex},
		},
		{
			name:     "remote logged-in, no local",
			flags:    CapRemoteIndex,
			wantStr:  "Capabilities{[remote_index]}",
			hasTrue:  []Capability{CapRemoteIndex},
			hasFalse: []Capability{CapLocalGit, CapIndexLocal, CapChatLLM, CapToolCalling},
		},
		{
			name:     "remote + local",
			flags:    CapRemoteIndex | CapLocalGit | CapIndexLocal,
			wantStr:  "Capabilities{[local_git index_local remote_index]}",
			hasTrue:  []Capability{CapRemoteIndex, CapLocalGit, CapIndexLocal},
			hasFalse: []Capability{CapChatLLM, CapToolCalling},
		},
		{
			name:     "server (container) only",
			flags:    CapRemoteIndex | CapChatLLM,
			wantStr:  "Capabilities{[remote_index chat_llm]}",
			hasTrue:  []Capability{CapRemoteIndex, CapChatLLM},
			hasFalse: []Capability{CapLocalGit, CapIndexLocal, CapToolCalling},
		},
		{
			name:     "MCP standalone",
			flags:    CapLocalGit | CapIndexLocal,
			wantStr:  "Capabilities{[local_git index_local]}",
			hasTrue:  []Capability{CapLocalGit, CapIndexLocal},
			hasFalse: []Capability{CapRemoteIndex, CapChatLLM, CapToolCalling},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Capabilities{Flags: tt.flags}

			// Check Has() returns true for expected capabilities
			for _, f := range tt.hasTrue {
				if !c.Has(f) {
					t.Errorf("Capabilities{%b}.Has(%d) = false, want true", tt.flags, f)
				}
			}

			// Check Has() returns false for expected missing capabilities
			for _, f := range tt.hasFalse {
				if c.Has(f) {
					t.Errorf("Capabilities{%b}.Has(%d) = true, want false", tt.flags, f)
				}
			}

			// Check String()
			if got := c.String(); got != tt.wantStr {
				t.Errorf("Capabilities{%b}.String() = %q, want %q", tt.flags, got, tt.wantStr)
			}
		})
	}
}
