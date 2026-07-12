// Package gitenv builds environments for invoking git as a subprocess.
//
// git decides which repository to operate on from a set of environment
// variables (GIT_DIR, GIT_WORK_TREE, …) that take precedence over the -C
// directory and normal upward discovery. When semidx runs in a context where
// git has already exported those — inside a git hook, or a bare repository's
// linked worktree — they leak into git child processes and make commands act on
// the caller's repository instead of the target directory. Clean strips them so
// git resolves the repository from its -C directory / working directory.
package gitenv

import "strings"

// locationVars are the environment variables through which git overrides
// repository discovery. Inheriting them makes `git -C dir …` ignore dir and act
// on the repository identified by the variables instead.
var locationVars = map[string]struct{}{
	"GIT_DIR":                          {},
	"GIT_WORK_TREE":                    {},
	"GIT_INDEX_FILE":                   {},
	"GIT_COMMON_DIR":                   {},
	"GIT_OBJECT_DIRECTORY":             {},
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
	"GIT_NAMESPACE":                    {},
	"GIT_PREFIX":                       {},
}

// Clean returns a copy of base with git's repository-location variables removed,
// so a git subprocess discovers its repository from its -C directory / working
// directory. All other variables (config paths, identity, credentials, PATH)
// are preserved. base is not modified.
func Clean(base []string) []string {
	out := make([]string, 0, len(base))
	for _, kv := range base {
		if k, _, ok := strings.Cut(kv, "="); ok {
			if _, drop := locationVars[k]; drop {
				continue
			}
		}
		out = append(out, kv)
	}
	return out
}
