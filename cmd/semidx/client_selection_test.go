package main

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/clientconfig"
)

// TestSelectionCommandsDoNotPersistEnvOverrides: `tenant use` / `workspace
// use` must change only their field on disk. The in-memory client config has
// SEMIDX_SERVER_URL/SEMIDX_TOKEN env overrides merged in; saving it used to
// overwrite the stored login with the per-shell values.
func TestSelectionCommandsDoNotPersistEnvOverrides(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(d *deps) error
		get  func(*clientconfig.Config) string
	}{
		{"tenant", func(d *deps) error { return runCmd(newTenantUseCmd(d), "acme") },
			func(c *clientconfig.Config) string { return c.Tenant }},
		{"workspace", func(d *deps) error { return runCmd(newWorkspaceUseCmd(d), "team") },
			func(c *clientconfig.Config) string { return c.Workspace }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			for _, k := range []string{"SEMIDX_TENANT", "SEMIDX_WORKSPACE", "SEMIDX_DEFAULT_PROJECT"} {
				t.Setenv(k, "")
			}
			saved := &clientconfig.Config{ServerURL: "https://saved.example", Token: "saved-token"}
			if err := clientconfig.Save(saved); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SEMIDX_SERVER_URL", "https://env.example")
			t.Setenv("SEMIDX_TOKEN", "env-token")
			loaded, err := clientconfig.Load()
			if err != nil {
				t.Fatal(err)
			}
			d := &deps{client: loaded}
			if err := tc.run(d); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SEMIDX_SERVER_URL", "")
			t.Setenv("SEMIDX_TOKEN", "")
			onDisk, err := clientconfig.Load()
			if err != nil {
				t.Fatal(err)
			}
			if onDisk.ServerURL != "https://saved.example" || onDisk.Token != "saved-token" {
				t.Errorf("env overrides persisted: server=%q token=%q", onDisk.ServerURL, onDisk.Token)
			}
			if tc.get(onDisk) == "" {
				t.Errorf("selection not saved: %+v", onDisk)
			}
			if tc.get(d.client) == "" {
				t.Errorf("in-memory selection not updated: %+v", d.client)
			}
		})
	}
}

func runCmd(c interface {
	SetArgs([]string)
	Execute() error
}, args ...string) error {
	c.SetArgs(args)
	return c.Execute()
}
