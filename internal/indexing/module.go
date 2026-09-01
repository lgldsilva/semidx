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
	fileRel = filepath.Clean(fileRel)
	if fileRel != "." && !filepath.IsLocal(fileRel) {
		return "", ""
	}

	dir = filepath.Dir(fileRel)
	for {
		modPath := confinedGoMod(projectPath, dir)
		if modPath == "" {
			return "", ""
		}
		mp := readModulePathAt(modPath)
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

// confinedGoMod joins projectPath/relDir/go.mod and returns the cleaned path
// only when it stays inside the project root. Empty means "skip this hop".
func confinedGoMod(projectPath, relDir string) string {
	relDir = filepath.Clean(relDir)
	if relDir != "." && !filepath.IsLocal(relDir) {
		return ""
	}
	root := filepath.Clean(projectPath)
	full := filepath.Clean(filepath.Join(root, relDir, "go.mod"))
	if full != filepath.Join(root, "go.mod") && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return ""
	}
	return full
}

func readModulePathAt(path string) string {
	if path == "" {
		return ""
	}
	// #nosec G304 -- path is confined to the project root by confinedGoMod
	f, err := os.Open(path)
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
