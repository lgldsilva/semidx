package xdg

import (
	"os"
	"path/filepath"
	"testing"
)

// resetProfile restores the active profile after a test so tests are isolated.
// The caller must defer this immediately after SetProfile.
func saveProfile() string { return activeProfile }

func restoreProfile(p string) { activeProfile = p }

func TestDefaultPaths(t *testing.T) {
	p := saveProfile()
	defer restoreProfile(p)
	activeProfile = ""
	t.Setenv(configDirEnv, "")

	env, err := UserEnvPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ClientConfigPath()
	if err != nil {
		t.Fatal(err)
	}

	if filepath.Base(env) != "semidx.env" {
		t.Errorf("default env file = %s, want semidx.env", filepath.Base(env))
	}
	if filepath.Base(cfg) != "config.yaml" {
		t.Errorf("default config file = %s, want config.yaml", filepath.Base(cfg))
	}
}

func TestProfilePaths(t *testing.T) {
	p := saveProfile()
	defer restoreProfile(p)

	if err := SetProfile("test"); err != nil {
		t.Fatal(err)
	}

	env, err := UserEnvPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ClientConfigPath()
	if err != nil {
		t.Fatal(err)
	}

	if filepath.Base(env) != "semidx-test.env" {
		t.Errorf("profile env file = %s, want semidx-test.env", filepath.Base(env))
	}
	if filepath.Base(cfg) != "config-test.yaml" {
		t.Errorf("profile config file = %s, want config-test.yaml", filepath.Base(cfg))
	}
}

func TestProfilePathsWithNumericAndDots(t *testing.T) {
	p := saveProfile()
	defer restoreProfile(p)

	if err := SetProfile("ci.v2"); err != nil {
		t.Fatal(err)
	}

	env, err := UserEnvPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(env) != "semidx-ci.v2.env" {
		t.Errorf("got %s, want semidx-ci.v2.env", filepath.Base(env))
	}
}

func TestConfigDirEnv(t *testing.T) {
	p := saveProfile()
	defer restoreProfile(p)
	activeProfile = ""

	tmp := t.TempDir()
	t.Setenv(configDirEnv, tmp)

	env, err := UserEnvPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ClientConfigPath()
	if err != nil {
		t.Fatal(err)
	}

	wantEnv := filepath.Join(tmp, "semidx.env")
	wantCfg := filepath.Join(tmp, "config.yaml")
	if env != wantEnv {
		t.Errorf("EnvPath = %q, want %q", env, wantEnv)
	}
	if cfg != wantCfg {
		t.Errorf("ClientConfigPath = %q, want %q", cfg, wantCfg)
	}
}

func TestConfigDirEnvWithProfile(t *testing.T) {
	p := saveProfile()
	defer restoreProfile(p)

	tmp := t.TempDir()
	t.Setenv(configDirEnv, tmp)

	if err := SetProfile("ci"); err != nil {
		t.Fatal(err)
	}

	env, _ := UserEnvPath()
	cfg, _ := ClientConfigPath()

	wantEnv := filepath.Join(tmp, "semidx-ci.env")
	wantCfg := filepath.Join(tmp, "config-ci.yaml")
	if env != wantEnv {
		t.Errorf("EnvPath = %q, want %q", env, wantEnv)
	}
	if cfg != wantCfg {
		t.Errorf("ClientConfigPath = %q, want %q", cfg, wantCfg)
	}
}

func TestSetProfileValidation(t *testing.T) {
	p := saveProfile()
	defer restoreProfile(p)

	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    string
	}{
		{"empty", "", false, ""},
		{"default", "default", false, ""},
		{"valid", "test", false, "test"},
		{"with dash", "my-profile", false, "my-profile"},
		{"with dot", "v0.1", false, "v0.1"},
		{"with spaces trimmed", "  ci  ", false, "ci"},
		{"slash", "a/b", true, ""},
		{"backslash", "a\\b", true, ""},
		{"space inside", "a b", true, ""},
		{"non-alphanumeric", "héllo", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			activeProfile = ""
			err := SetProfile(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetProfile(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && activeProfile != tt.want {
				t.Errorf("activeProfile = %q, want %q", activeProfile, tt.want)
			}
		})
	}
}

func TestProfileGetter(t *testing.T) {
	p := saveProfile()
	defer restoreProfile(p)

	if Profile() != "" {
		t.Error("Profile should be empty by default")
	}

	if err := SetProfile("test"); err != nil {
		t.Fatal(err)
	}
	if Profile() != "test" {
		t.Errorf("Profile = %q, want test", Profile())
	}

	if err := SetProfile(""); err != nil {
		t.Fatal(err)
	}
	if Profile() != "" {
		t.Error("Profile should be empty after clearing")
	}
}

func TestXDGConfigHomeIsolation(t *testing.T) {
	p := saveProfile()
	defer restoreProfile(p)
	activeProfile = ""
	os.Unsetenv(configDirEnv)

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	env, err := UserEnvPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ClientConfigPath()
	if err != nil {
		t.Fatal(err)
	}

	wantEnv := filepath.Join(dir, semidxDir, "semidx.env")
	wantCfg := filepath.Join(dir, semidxDir, "config.yaml")
	if env != wantEnv {
		t.Errorf("EnvPath = %q, want %q", env, wantEnv)
	}
	if cfg != wantCfg {
		t.Errorf("ClientConfigPath = %q, want %q", cfg, wantCfg)
	}
}
