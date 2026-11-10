package config

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync/atomic"
)

// projectEnvFile is the working-directory .env layer. It belongs to whatever
// repository the user happens to be in — possibly a freshly cloned, untrusted
// one — so it is read with a denylist: it may tune semidx (workers, limits,
// keyword mode, local index…) but never decide where code or credentials go.
const projectEnvFile = ".env"

// projectEnvDenied lists settings that, set by an untrusted project .env,
// would weaken privacy/security or run/download code. Credentials and
// endpoints are caught by projectEnvKeyDenied's generic rules.
var projectEnvDenied = map[string]bool{
	"EMBED_PRIVACY":                 true, // could switch privacy routing off
	"EMBED_PROVIDER":                true, // prepends a provider to the chain
	"SEMIDX_CHAT_PROVIDER":          true, // picks who receives code for RAG/agent
	"SEMIDX_SECRET_SCAN":            true,
	"SEMIDX_SECRET_BLOCK_EMBEDDING": true,
	"SEMIDX_INSECURE":               true, // TLS off for `semidx upgrade`
	"SEMIDX_TLS_CA_CERT":            true, // custom CA for `semidx upgrade`
	"SEMIDX_JAVA_DECOMPILER":        true, // external command
	"SEMIDX_MCP_CLIENT_CONFIG":      true, // launches external MCP servers
	"SEMIDX_AGENT_ACTIONS":          true, // agent may execute actions
	"SEMIDX_GIT_ALLOW_FILE":         true,
	"SEMIDX_GIT_SSL_NO_VERIFY":      true,
	"SEMIDX_COOKIE_SECURE":          true,
}

// projectEnvTightening lists denied keys a project .env may still set to the
// one value that only makes semidx stricter (e.g. EMBED_PRIVACY=true).
var projectEnvTightening = map[string]string{
	"EMBED_PRIVACY":                 "true",
	"SEMIDX_SECRET_SCAN":            "true",
	"SEMIDX_SECRET_BLOCK_EMBEDDING": "true",
}

// projectEnvAllowed reports whether a project .env may set key to value.
func projectEnvAllowed(key, value string) bool {
	if want, ok := projectEnvTightening[strings.ToUpper(key)]; ok && value == want {
		return true
	}
	return !projectEnvKeyDenied(key)
}

// projectEnvKeyDenied reports whether a project .env may not set key:
// credentials and DSNs (anything IsSecret masks), any endpoint/base URL or
// release API (where keys and code would be sent), and the explicit
// security-sensitive settings above.
func projectEnvKeyDenied(key string) bool {
	up := strings.ToUpper(key)
	if projectEnvDenied[up] || IsSecret(key) {
		return true
	}
	for _, suffix := range []string{"_URL", "_URLS", "_ENDPOINT", "_API"} {
		if strings.HasSuffix(up, suffix) {
			return true
		}
	}
	return false
}

// semidxKey reports whether semidx reads key, so the ignore warning names
// only settings that matter (not an app's own STRIPE_SECRET_KEY etc.).
func semidxKey(key string) bool {
	if strings.HasPrefix(key, "SEMIDX_") || key == "OLLAMA_URL" {
		return true
	}
	for _, k := range KnownKeys {
		if k.Name == key {
			return true
		}
	}
	return false
}

var (
	// projectEnvWarnOut receives the one-time "ignored keys" notice (stderr;
	// MCP stdio keeps stdout for the protocol). Tests swap it.
	projectEnvWarnOut io.Writer = os.Stderr
	projectEnvWarned  atomic.Bool
)

// loadProjectEnv parses the working-directory .env and drops every key
// projectEnvAllowed rejects, warning once per process about the ignored
// semidx settings.
func loadProjectEnv(path string) map[string]string {
	vars := parseEnvFile(path)
	var ignored []string
	for k, v := range vars {
		if projectEnvAllowed(k, v) {
			continue
		}
		delete(vars, k)
		if semidxKey(k) {
			ignored = append(ignored, k)
		}
	}
	if len(ignored) > 0 && projectEnvWarned.CompareAndSwap(false, true) {
		sort.Strings(ignored)
		_, _ = fmt.Fprintf(projectEnvWarnOut,
			"[warn] ignoring %s from %s in the working directory: credentials, endpoints and "+
				"security settings are read only from the environment or the user config "+
				"(`semidx config set`)\n", strings.Join(ignored, ", "), path)
	}
	return vars
}

// layeredResolver builds the standard precedence chain: real env (lookup) >
// filtered project .env > user config file > default.
func layeredResolver(lookup func(string) (string, bool)) *resolver {
	r := &resolver{lookup: lookup}
	r.layers = append(r.layers, loadProjectEnv(projectEnvFile))
	if p, err := UserEnvPath(); err == nil {
		r.layers = append(r.layers, parseEnvFile(p))
	}
	return r
}
