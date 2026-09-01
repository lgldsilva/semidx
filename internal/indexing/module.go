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
	f, err := os.Open(filepath.Clean(filepath.Join(projectPath, "go.mod")))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	return readModulePathFromFile(f)
}

// FindModulePath walks up from the directory containing fileRel until it finds
// a go.mod inside projectPath. It returns the module path declared there, or an
// empty string if no go.mod is found. This supports monorepos with multiple
// Go modules in subdirectories.
func FindModulePath(projectPath, fileRel string) string {
	_, mp := findModuleInfo(projectPath, fileRel)
	return mp
}

// FindModuleDir walks up from the directory containing fileRel until it finds
// the nearest go.mod inside projectPath. It returns the directory containing
// that go.mod relative to projectPath ("." for the root), or an empty string
// if no go.mod is found.
func FindModuleDir(projectPath, fileRel string) string {
	dir, _ := findModuleInfo(projectPath, fileRel)
	return dir
}

func findModuleInfo(projectPath, fileRel string) (dir, modulePath string) {
	if projectPath == "" || fileRel == "" {
		return "", ""
	}

	dir = filepath.Dir(fileRel)
	for {
		path := filepath.Join(projectPath, dir, "go.mod")
		mp := readModulePathAt(path)
		if mp != "" {
			return dir, mp
		}
		if dir == "." || dir == string(filepath.Separator) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", ""
}

func readModulePathAt(path string) string {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	return readModulePathFromFile(f)
}

func readModulePathFromFile(f *os.File) string {
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}
