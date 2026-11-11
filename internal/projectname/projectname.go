// Package projectname holds the single project-name rule shared by the HTTP
// API and the web admin. Names reach the filesystem (server-side clones), so
// the rule is deliberately strict: no separators, no leading dot or dash.
package projectname

import (
	"errors"
	"regexp"
)

// MaxLen is the longest accepted project name.
const MaxLen = 255

// valid allows letters, digits, hyphens, underscores, and dots, and must start
// with an alphanumeric character.
var valid = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,254}$`)

// Validate reports why name is not an acceptable project name, or nil.
func Validate(name string) error {
	if name == "" {
		return errors.New("project name is required")
	}
	if len(name) > MaxLen {
		return errors.New("project name must be at most 255 characters")
	}
	if !valid.MatchString(name) {
		return errors.New("project name must start with alphanumeric and contain only letters, digits, hyphens, underscores, and dots")
	}
	return nil
}
