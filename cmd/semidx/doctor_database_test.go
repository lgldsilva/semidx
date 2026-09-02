package main

import (
	"context"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/clientconfig"
	"github.com/lgldsilva/semidx/internal/config"
)

// TestReportDatabaseOnlyWhenPostgresIsActive keeps doctor fast and side-effect
// free: probing a database that this invocation will not use would add a
// connection timeout to every `semidx doctor` run on a laptop.
func TestReportDatabaseOnlyWhenPostgresIsActive(t *testing.T) {
	dsn := "postgres://u:p@127.0.0.1:1/db"
	cases := []struct {
		name string
		d    *deps
		want bool
	}{
		{"local SQLite", &deps{cfg: &config.Config{DatabaseURL: dsn}, localIndexPath: "/tmp/i.db"}, false},
		{"remote server", &deps{cfg: &config.Config{DatabaseURL: dsn}, useRemote: true, client: &clientconfig.Config{ServerURL: "http://s"}}, false},
		{"no DSN", &deps{cfg: &config.Config{}}, false},
		{"postgres active", &deps{cfg: &config.Config{DatabaseURL: dsn}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b strings.Builder
			reportDatabase(context.Background(), &b, c.d)
			if got := strings.Contains(b.String(), "## Database"); got != c.want {
				t.Errorf("database section emitted = %v, want %v (output %q)", got, c.want, b.String())
			}
		})
	}
}

// TestReportDatabaseUnreachableIsReportedNotFatal: a wrong DSN must produce a
// finding pointing at `semidx docker`, never abort the rest of the report.
func TestReportDatabaseUnreachableIsReportedNotFatal(t *testing.T) {
	var b strings.Builder
	d := &deps{cfg: &config.Config{DatabaseURL: "postgres://u:p@127.0.0.1:1/db?sslmode=disable"}}
	reportDatabase(context.Background(), &b, d)

	out := b.String()
	if !strings.Contains(out, "unreachable") {
		t.Errorf("output does not report the failure: %q", out)
	}
	if !strings.Contains(out, "semidx docker") {
		t.Errorf("output does not point at the fix: %q", out)
	}
}
