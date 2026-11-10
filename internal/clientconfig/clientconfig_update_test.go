package clientconfig

import "testing"

func TestUpdateIgnoresEnvOverrides(t *testing.T) {
	isolate(t)
	if err := Save(&Config{ServerURL: "https://saved", Token: "saved"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEMIDX_SERVER_URL", "https://env")
	t.Setenv("SEMIDX_TOKEN", "env")
	if c, err := LoadFile(); err != nil || c.Token != "saved" || c.ServerURL != "https://saved" {
		t.Fatalf("LoadFile applied env overrides: %+v, %v", c, err)
	}
	if err := Update(func(c *Config) { c.Tenant = "acme" }); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEMIDX_SERVER_URL", "")
	t.Setenv("SEMIDX_TOKEN", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "saved" || c.ServerURL != "https://saved" || c.Tenant != "acme" {
		t.Errorf("Update persisted the wrong state: %+v", c)
	}
}
