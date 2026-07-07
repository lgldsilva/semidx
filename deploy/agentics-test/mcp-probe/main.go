// Command mcp-probe drives a real MCP stdio handshake against a semidx MCP
// server, to verify from the outside that the server an agent would launch
// actually speaks the protocol and exposes semidx's tools. It spawns the command
// given after the flags (e.g. `semidx --local mcp`), performs initialize +
// tools/list, then optionally calls semantic_projects and semantic_search.
//
// It is the ground-truth check used by the agentics test harness: agents load
// the same command via their MCP config, so if the probe passes, the wiring the
// harness installed is functional. Exit code 0 = PASS, non-zero = failure.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	var project, query, expect string
	var timeout time.Duration
	flag.StringVar(&project, "project", "", "project to search via semantic_search (optional)")
	flag.StringVar(&query, "query", "", "search query (requires -project)")
	flag.StringVar(&expect, "expect", "", "substring that must appear in the search result")
	flag.DurationVar(&timeout, "timeout", 60*time.Second, "overall timeout")
	flag.Parse()

	cmdArgs := flag.Args()
	if len(cmdArgs) == 0 {
		die("usage: mcp-probe [flags] -- <command that runs `semidx mcp`>")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// The spawned command is the harness's own semidx invocation, passed on the
	// argv of this test tool. Verify the executable resolves to a real binary
	// and use the resolved path to construct the command.
	exePath, err := exec.LookPath(cmdArgs[0])
	if err != nil {
		die("command not found: %s: %v", cmdArgs[0], err)
	}
	cmd := exec.CommandContext(ctx, "sh")
	cmd.Path = exePath
	cmd.Args = append([]string{exePath}, cmdArgs[1:]...)
	cmd.Stderr = os.Stderr // surface the server's own logs alongside the probe's

	cli := mcp.NewClient(&mcp.Implementation{Name: "mcp-probe", Version: "1"}, nil)
	sess, err := cli.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		die("connect (initialize) failed: %v", err)
	}
	defer func() { _ = sess.Close() }()
	fmt.Println("OK   initialize handshake completed")

	// tools/list must expose the three semidx tools (and never semantic_index).
	lt, err := sess.ListTools(ctx, nil)
	if err != nil {
		die("tools/list failed: %v", err)
	}
	want := map[string]bool{"semantic_search": false, "semantic_projects": false, "semantic_reindex": false}
	for _, tool := range lt.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
		if tool.Name == "semantic_index" {
			die("tool semantic_index must not exist (arbitrary-path indexing was removed)")
		}
	}
	for name, found := range want {
		if !found {
			die("tools/list is missing %q (have %d tools)", name, len(lt.Tools))
		}
	}
	fmt.Println("OK   tools/list exposes semantic_search, semantic_projects, semantic_reindex")

	// semantic_projects should succeed (even if the index is empty).
	if txt, isErr := call(ctx, sess, "semantic_projects", nil); isErr {
		die("semantic_projects returned an error: %s", txt)
	} else {
		fmt.Printf("OK   semantic_projects:\n%s\n", indent(txt))
	}

	// Optional live search.
	if project != "" && query != "" {
		txt, isErr := call(ctx, sess, "semantic_search", map[string]any{"project": project, "query": query})
		if isErr {
			die("semantic_search errored: %s", txt)
		}
		if expect != "" && !strings.Contains(txt, expect) {
			die("semantic_search result missing %q:\n%s", expect, txt)
		}
		fmt.Printf("OK   semantic_search %q / %q:\n%s\n", project, query, indent(txt))
	}

	fmt.Println("PASS MCP server is working")
}

func call(ctx context.Context, sess *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		die("CallTool %s failed: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func indent(s string) string {
	return "     " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n     ")
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL "+format+"\n", args...)
	os.Exit(1)
}
