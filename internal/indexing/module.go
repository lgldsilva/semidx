package indexing

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

const goModFilename = "go.mod"

// ReadModulePath reads the Go module path from projectPath/go.mod.
// Returns empty string if go.mod doesn't exist or can't be read.
func ReadModulePath(projectPath string) string {
	return readModulePathAt(projectPath, goModFilename)
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
		relFile := moduleRel(dir)
		if !filepath.IsLocal(relFile) {
			return "", ""
		}
		mp := readModulePathAt(projectPath, relFile)
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

func moduleRel(relDir string) string {
	relDir = filepath.Clean(relDir)
	if relDir == "." || relDir == "" {
		return goModFilename
	}
	return filepath.Join(relDir, goModFilename)
}

// confinedGoMod joins projectPath/relDir/go.mod and returns the cleaned path
// only when it stays inside the project root. Empty means "skip this hop".
func confinedGoMod(projectPath, relDir string) string {
	relFile := moduleRel(relDir)
	if !filepath.IsLocal(relFile) {
		return ""
	}
	root := filepath.Clean(projectPath)
	full := filepath.Clean(filepath.Join(root, relFile))
	if full != filepath.Join(root, goModFilename) && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return ""
	}
	return full
}

func readModulePathAt(root, relFile string) string {
	relFile = filepath.Clean(relFile)
	if !filepath.IsLocal(relFile) {
		return ""
	}
	full := confinedGoMod(root, filepath.Dir(relFile))
	if full == "" {
		return ""
	}
	// #nosec G304 -- relFile passed filepath.IsLocal; full is confined to root
	f, err := os.Open(full)
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
