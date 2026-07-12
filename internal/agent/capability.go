package agent

import "fmt"

// Capability is a bitmask flag for what the current backend can do.
type Capability uint64

const (
	CapLocalGit    Capability = 1 << iota // can run git on local FS (worktrees/branches)
	CapIndexLocal                         // can index a local path/worktree
	CapRemoteIndex                        // can trigger index jobs on remote server
	CapChatLLM                            // a chat LLM is configured (Gemini/OpenRouter key)
	CapToolCalling                        // the chat LLM supports tool calling
)

// Capabilities describes what the current runtime backend offers.
type Capabilities struct {
	Flags Capability
}

func (c Capabilities) Has(f Capability) bool { return c.Flags&f != 0 }

func (c Capabilities) String() string {
	var labels []string
	for _, kv := range []struct {
		f Capability
		l string
	}{
		{CapLocalGit, "local_git"},
		{CapIndexLocal, "index_local"},
		{CapRemoteIndex, "remote_index"},
		{CapChatLLM, "chat_llm"},
		{CapToolCalling, "tool_calling"},
	} {
		if c.Has(kv.f) {
			labels = append(labels, kv.l)
		}
	}
	return fmt.Sprintf("Capabilities{%v}", labels)
}
