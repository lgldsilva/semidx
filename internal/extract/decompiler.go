package extract

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// decompiler runs an external Java decompiler over a .class file to produce
// pseudo-source for richer semantic search. It is entirely optional: when
// SEMIDX_JAVA_DECOMPILER is unset the JAR extractor uses only the pure-Go
// constant-pool API surface, so semidx still "runs anywhere". The env var holds
// a command whose first token is the executable and remaining tokens are fixed
// args; the path of a temp .class file is appended, and stdout is the source.
// Example: SEMIDX_JAVA_DECOMPILER="java -jar /opt/cfr.jar"
type decompiler struct {
	argv []string
}

// newDecompiler returns a decompiler if one is configured, else nil.
func newDecompiler() *decompiler {
	cmd := strings.Fields(os.Getenv("SEMIDX_JAVA_DECOMPILER"))
	if len(cmd) == 0 {
		return nil
	}
	return &decompiler{argv: cmd}
}

// decompile writes the class bytes to a temp file, runs the configured tool and
// returns its stdout. Any failure (missing tool, timeout, non-zero exit) degrades
// gracefully to ("", false) so indexing continues with the constant-pool surface.
func (d *decompiler) decompile(class []byte) (string, bool) {
	f, err := os.CreateTemp("", "semidx-*.class")
	if err != nil {
		return "", false
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(class); err != nil {
		_ = f.Close()
		return "", false
	}
	_ = f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := append(append([]string{}, d.argv[1:]...), f.Name())
	// #nosec G204 -- the command comes from operator config (SEMIDX_JAVA_DECOMPILER), not user input.
	out, err := exec.CommandContext(ctx, d.argv[0], args...).Output()
	if err != nil || len(out) == 0 {
		return "", false
	}
	return string(out), true
}
