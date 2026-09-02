package pgcheck

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeDB answers the probe's queries from a script keyed by a distinctive
// fragment of each statement, so tests describe a database state declaratively.
type fakeDB struct {
	version    string
	versionErr error
	installed  map[string]string // extension -> extversion
	available  map[string]bool
	halfvec    bool
	superuser  bool
}

type fakeRow struct {
	values []any
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("scan arity mismatch")
	}
	for i, d := range dest {
		switch t := d.(type) {
		case *string:
			*t = r.values[i].(string)
		case *bool:
			*t = r.values[i].(bool)
		case **string:
			if r.values[i] == nil {
				*t = nil
				continue
			}
			v := r.values[i].(string)
			*t = &v
		default:
			return errors.New("unsupported scan target")
		}
	}
	return nil
}

func (f *fakeDB) QueryRow(_ context.Context, sql string, args ...any) Row {
	switch {
	case strings.Contains(sql, "server_version"):
		return fakeRow{values: []any{f.version}, err: f.versionErr}
	case strings.Contains(sql, "FROM pg_extension"):
		name := args[0].(string)
		if v, ok := f.installed[name]; ok {
			return fakeRow{values: []any{v}}
		}
		return fakeRow{values: []any{nil}}
	case strings.Contains(sql, "pg_available_extensions"):
		return fakeRow{values: []any{f.available[args[0].(string)]}}
	case strings.Contains(sql, "halfvec_cosine_ops"):
		return fakeRow{values: []any{f.halfvec}}
	case strings.Contains(sql, "rolsuper"):
		return fakeRow{values: []any{f.superuser}}
	}
	return fakeRow{err: errors.New("unexpected query: " + sql)}
}

func findingsFor(t *testing.T, db *fakeDB) []string {
	t.Helper()
	r, err := Inspect(context.Background(), db)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	return r.Findings()
}

func TestInspectHealthyDatabase(t *testing.T) {
	db := &fakeDB{
		version:   "16.4",
		installed: map[string]string{"vector": "0.8.0", "pg_trgm": "1.6"},
		halfvec:   true,
		superuser: true,
	}
	r, err := Inspect(context.Background(), db)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if r.ServerVersion != "16.4" || r.Vector.Version != "0.8.0" || !r.Trgm.Installed {
		t.Fatalf("report = %+v", r)
	}
	if got := r.Findings(); len(got) != 0 {
		t.Errorf("healthy database reported findings: %v", got)
	}
}

func TestInspectMissingExtensionIsAFindingNotAnError(t *testing.T) {
	db := &fakeDB{
		version:   "16.4",
		installed: map[string]string{"pg_trgm": "1.6"},
		available: map[string]bool{"vector": true},
		superuser: true,
	}
	got := findingsFor(t, db)
	if len(got) != 1 || !strings.Contains(got[0], "CREATE EXTENSION vector") {
		t.Errorf("findings = %v, want one CREATE EXTENSION hint", got)
	}
}

func TestInspectUnprivilegedRoleIsToldToAskAnAdmin(t *testing.T) {
	db := &fakeDB{
		version:   "16.4",
		installed: map[string]string{"pg_trgm": "1.6"},
		available: map[string]bool{"vector": true},
	}
	got := findingsFor(t, db)
	if len(got) != 1 || !strings.Contains(got[0], "may not create it") {
		t.Errorf("findings = %v, want the admin hint", got)
	}
}

func TestInspectServerWithoutPgvectorIsUnusable(t *testing.T) {
	db := &fakeDB{
		version:   "16.4",
		installed: map[string]string{"pg_trgm": "1.6"},
		superuser: true,
	}
	got := findingsFor(t, db)
	if len(got) != 1 || !strings.Contains(got[0], "NOT available") {
		t.Errorf("findings = %v, want the unusable-server finding", got)
	}
}

// TestInspectOldPgvectorFlagsHalfvec is the finding that motivated this probe:
// pgvector below 0.7 indexes fine with bge-m3 and only fails later, when a
// model above 2000 dimensions needs the halfvec operator class.
func TestInspectOldPgvectorFlagsHalfvec(t *testing.T) {
	db := &fakeDB{
		version:   "16.4",
		installed: map[string]string{"vector": "0.6.2", "pg_trgm": "1.6"},
		halfvec:   false,
		superuser: true,
	}
	got := findingsFor(t, db)
	if len(got) != 1 || !strings.Contains(got[0], "halfvec_cosine_ops") || !strings.Contains(got[0], "0.6.2") {
		t.Errorf("findings = %v, want the halfvec upgrade hint naming the version", got)
	}
}

func TestInspectUnreachableDatabaseErrors(t *testing.T) {
	db := &fakeDB{versionErr: errors.New("connection refused")}
	if _, err := Inspect(context.Background(), db); err == nil {
		t.Fatal("Inspect() on an unreachable database should error")
	}
}
