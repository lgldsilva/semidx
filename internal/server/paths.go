package server

import (
	"errors"
	"path/filepath"
	"strings"
)

// validateRelativePath ensures a pushed file path is a safe, relative key with no
// parent-directory traversal.
func validateRelativePath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("path is required")
	}
	if filepath.IsAbs(path) {
		return errors.New("path must be relative")
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || clean == ".." {
		return errors.New("path is required")
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return errors.New("path must not contain parent directory segments")
		}
	}
	return nil
}
