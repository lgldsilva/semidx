package privacy

import "testing"

func TestIsSensitive(t *testing.T) {
	cases := map[string]bool{
		".env":                   true,
		"config/.env.production": true,
		"secrets/api_key.txt":    true,
		"deploy/private.pem":     true,
		"src/auth/login.go":      true, // "auth" segment
		"internal/config/cfg.go": true, // "config" segment
		"db/migrations/001.sql":  true, // "db" segment
		"certs/server.key":       true,
		"app/nginx.conf":         true, // .conf ext
		"src/main.go":            false,
		"docs/readme.md":         false,
		"pkg/handler.go":         false,
		"src/donkey.go":          false,
		"pkg/hotkey.py":          false,
		"internal/keyboard.go":   false,
	}
	for path, want := range cases {
		if got := IsSensitive(path); got != want {
			t.Errorf("IsSensitive(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestIsSensitiveCaseInsensitive(t *testing.T) {
	for _, p := range []string{"SECRETS/TOKEN.TXT", "Config/App.CONF", "AUTH/x.go"} {
		if !IsSensitive(p) {
			t.Errorf("IsSensitive(%q) = false, want true (case-insensitive)", p)
		}
	}
}
