package main

import (
	"context"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/config"
)

// A command that needs Postgres but cannot reach it used to leave d.db holding
// a typed-nil *PgStore. teardown then saw a non-nil interface, called Close on
// it, and the process died with a nil-pointer panic instead of printing the
// connection error. Reproduces with a DSN nothing is listening on.
func TestDatabaseFailureLeavesTeardownSafe(t *testing.T) {
	isolateEnv(t)
	// A malformed DSN fails during parsing, so the test exercises the same
	// failure path without waiting out the connection retry backoff.
	d := &deps{cfg: &config.Config{DatabaseURL: "postgres://%zz"}}

	got, err := d.database(context.Background())
	if err == nil {
		t.Fatal("database() must report the connection failure")
	}
	if !strings.Contains(err.Error(), "connect to database") {
		t.Errorf("err = %v, want it wrapped as a connection failure", err)
	}
	if got != nil {
		t.Errorf("store = %#v, want nil so callers cannot use a half-open store", got)
	}
	if d.db != nil {
		t.Error("d.db must stay nil after a failed connection")
	}

	// The real regression: this used to panic.
	d.teardown(nil, nil)
}
