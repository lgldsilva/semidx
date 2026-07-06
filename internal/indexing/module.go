package indexing

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// ReadModulePath reads the Go module path from projectPath/go.mod.
// Returns empty string if go.mod doesn't exist or can't be read.
func ReadModulePath(projectPath string) string {
	// #nosec G304 -- projectPath is resolved from CLI/config, not user input.
	f, err := os.Open(filepath.Join(projectPath, "go.mod"))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}
