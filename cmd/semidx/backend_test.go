package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/clientconfig"
)

func TestResolveUseRemote(t *testing.T) {
	cc := &clientconfig.Config{ServerURL: "http://s"}
	empty := &clientconfig.Config{}

	tests := []struct {
		name       string
		cc         *clientconfig.Config
		forceLocal bool
		backend    string
		env        string
		want       bool
		wantErr    bool
	}{
		{"auto with server", cc, false, "", "", true, false},
		{"auto without server", empty, false, "", "", false, false},
		{"force local ignores server", cc, true, "", "", false, false},
		{"backend local ignores server", cc, false, "local", "", false, false},
		{"backend remote with server", cc, false, "remote", "", true, false},
		{"backend remote without server", empty, false, "remote", "", false, true},
		{"env local", cc, false, "", "local", false, false},
		{"flag wins over env", cc, false, "local", "remote", false, false},
		{"invalid backend", cc, false, "cloud", "", false, true},
		{"force local wins over backend remote", cc, true, "remote", "", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SEMIDX_BACKEND", tc.env)
			got, err := resolveUseRemote(tc.cc, tc.forceLocal, tc.backend)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Errorf("useRemote = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestErrIndexInRemoteMode(t *testing.T) {
	err := errIndexInRemoteMode("http://semidx.example", ".")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"push", "--to-server", "--local", "logout", "http://semidx.example"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}
}

func TestValidateIndexToServer(t *testing.T) {
	if err := validateIndexToServer(false, false, "", false); err != nil {
		t.Fatalf("valid combo: %v", err)
	}
	if err := validateIndexToServer(true, false, "", false); err == nil {
		t.Error("watch should fail")
	}
	if err := validateIndexToServer(false, true, "", false); err == nil {
		t.Error("git should fail")
	}
	if err := validateIndexToServer(false, false, "main", false); err == nil {
		t.Error("branch should fail")
	}
	if err := validateIndexToServer(false, false, "", true); err == nil {
		t.Error("keyword should fail")
	}
}

func isolateCLIConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SEMIDX_SERVER_URL", "")
	t.Setenv("SEMIDX_TOKEN", "")
	t.Setenv("SEMIDX_DEFAULT_PROJECT", "")
	t.Setenv("SEMIDX_BACKEND", "")
	t.Chdir(t.TempDir())
}

func TestLogoutRemovesClientConfig(t *testing.T) {
	isolateCLIConfig(t)

	cfg := &clientconfig.Config{ServerURL: "http://example", Token: "tok"}
	if err := clientconfig.Save(cfg); err != nil {
		t.Fatal(err)
	}
	path, err := clientconfig.Path()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config should exist: %v", err)
	}
	if err := runCLI(t, "logout"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("config should be removed, stat err = %v", err)
	}
	// Idempotent.
	if err := runCLI(t, "logout"); err != nil {
		t.Fatalf("second logout: %v", err)
	}
}

func TestIndexRefusesRemoteWithoutToServer(t *testing.T) {
	isolateCLIConfig(t)
	// Pretend login without needing a live server: write client config.
	if err := clientconfig.Save(&clientconfig.Config{ServerURL: "http://127.0.0.1:9", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	err := runCLI(t, "index", "--project", ".")
	if err == nil {
		t.Fatal("index with remote login should error")
	}
	if !strings.Contains(err.Error(), "push") && !strings.Contains(err.Error(), "--to-server") {
		t.Errorf("error should guide user, got: %v", err)
	}
}

func TestLocalFlagOverridesLogin(t *testing.T) {
	isolateCLIConfig(t)
	if err := clientconfig.Save(&clientconfig.Config{ServerURL: "http://server:8080", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	// --local must be observed at run time (regression: flags used to be
	// snapshotted as zero values when wiring PersistentPreRunE).
	root := newRootCmd()
	root.SetArgs([]string{"--local", "config", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config list --local: %v", err)
	}

	d := &deps{}
	if err := d.setup(root, true, false, ""); err != nil {
		t.Fatal(err)
	}
	if d.remote() {
		t.Error("--local must clear remote mode")
	}
	if !d.hasServerConfig() {
		t.Error("login should still be loaded from disk")
	}
	label := activeBackend(d)
	if !strings.Contains(label, "ignored") {
		t.Errorf("active backend should note ignored login, got %q", label)
	}
}

func TestBackendRemoteRequiresLogin(t *testing.T) {
	isolateCLIConfig(t)
	err := runCLI(t, "--backend", "remote", "config", "list")
	if err == nil {
		t.Fatal("expected error when --backend remote without login")
	}
	if !strings.Contains(err.Error(), "login") {
		t.Errorf("got %v", err)
	}
}

func TestLogoutCommandWired(t *testing.T) {
	root := newRootCmd()
	cmd, _, err := root.Find([]string{"logout"})
	if err != nil || cmd == nil || cmd.Name() != "logout" {
		t.Fatalf("logout not wired: err=%v cmd=%v", err, cmd)
	}
}
