package main

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// runDockerCmd executes the command and returns everything it printed.
func runDockerCmd(t *testing.T) string {
	t.Helper()
	cmd := newDockerCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("docker: %v", err)
	}
	return out.String()
}

// TestDockerCmdEmitsParseableCompose keeps the printed block usable: an agent
// or a human pipes it straight into `docker compose`, so a typo here is a
// broken quickstart rather than a failing test elsewhere.
func TestDockerCmdEmitsParseableCompose(t *testing.T) {
	var doc struct {
		Services map[string]struct {
			Image       string            `yaml:"image"`
			Environment map[string]string `yaml:"environment"`
			Ports       []string          `yaml:"ports"`
			Healthcheck struct {
				Test []string `yaml:"test"`
			} `yaml:"healthcheck"`
		} `yaml:"services"`
		Volumes map[string]any `yaml:"volumes"`
	}
	if err := yaml.Unmarshal([]byte(runDockerCmd(t)), &doc); err != nil {
		t.Fatalf("printed compose is not valid YAML: %v", err)
	}

	db, ok := doc.Services["db"]
	if !ok {
		t.Fatalf("no db service in %v", doc.Services)
	}
	// A plain postgres image has no pgvector, which is the single most common
	// way to get a database semidx cannot use.
	if !strings.HasPrefix(db.Image, "pgvector/pgvector:") {
		t.Errorf("image = %q, want a pgvector image", db.Image)
	}
	if db.Environment["POSTGRES_DB"] == "" || db.Environment["POSTGRES_USER"] == "" {
		t.Errorf("environment is missing user/db: %v", db.Environment)
	}
	if len(db.Healthcheck.Test) == 0 {
		t.Error("no healthcheck: dependants cannot wait for the database")
	}
	if len(doc.Volumes) == 0 {
		t.Error("no named volume: the index would not survive a recreate")
	}
}

// TestDockerCmdPublishesOnLoopbackOnly pins the security posture and keeps the
// port consistent with the DSN printed right below it.
func TestDockerCmdPublishesOnLoopbackOnly(t *testing.T) {
	out := runDockerCmd(t)
	if !strings.Contains(out, `"127.0.0.1:5432:5432"`) {
		t.Error("the published port must be bound to loopback")
	}
	if strings.Contains(out, `"5432:5432"`) {
		t.Error("port published on every interface")
	}
	if !strings.Contains(out, "@localhost:5432/semidx") {
		t.Error("the suggested DSN must match the published port")
	}
}

// TestDockerCmdStatesTheHalfvecRequirement guards the requirement that is
// invisible until a >2000-dimension model is used: pgvector below 0.7 has no
// halfvec type, so EnsureChunksTable fails only then.
func TestDockerCmdStatesTheHalfvecRequirement(t *testing.T) {
	long := newDockerCmd().Long
	for _, want := range []string{"pgvector 0.7", "halfvec", "pg_trgm", "CREATE EXTENSION"} {
		if !strings.Contains(long, want) {
			t.Errorf("help text does not mention %q", want)
		}
	}
}
