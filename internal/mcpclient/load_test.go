package mcpclient

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_Empty(t *testing.T) {
	cfgs, err := LoadConfig("")
	if err != nil || cfgs != nil {
		t.Fatalf("empty path = %v, %v; want nil, nil", cfgs, err)
	}
}

func TestLoadConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	body := `{"servers":[
		{"name":"github","transport":"stdio","command":"gh-mcp","args":["--foo"],"enabled":true},
		{"name":"docs","transport":"http","url":"https://mcp.example/","enabled":false}
	]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgs, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfgs) != 2 {
		t.Fatalf("got %d servers, want 2", len(cfgs))
	}
	if cfgs[0].Name != "github" || cfgs[0].Transport != TransportStdio || cfgs[0].Command != "gh-mcp" || !cfgs[0].Enabled {
		t.Errorf("server[0] = %+v", cfgs[0])
	}
	if len(cfgs[0].Args) != 1 || cfgs[0].Args[0] != "--foo" {
		t.Errorf("server[0].Args = %v", cfgs[0].Args)
	}
	if cfgs[1].URL != "https://mcp.example/" || cfgs[1].Enabled {
		t.Errorf("server[1] = %+v", cfgs[1])
	}
}

func TestLoadConfig_Missing(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadConfig_BadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for bad JSON")
	}
}
