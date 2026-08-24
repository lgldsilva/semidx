package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/usage"
)

func usageServer(t *testing.T, fs *fakeStore) *Server {
	t.Helper()
	fs.token = &store.Token{Scopes: []string{"read"}}
	return New(fs, fakeEmbedder{}, nil)
}

func TestSearchUsageReport(t *testing.T) {
	fs := &fakeStore{usageAgg: usage.Aggregate{
		Total:              3,
		ByProject:          []usage.Count{{Key: "repo", Count: 3}},
		BySource:           []usage.Count{{Key: "mcp", Count: 3}},
		ByOutcome:          []usage.Count{{Key: "hit", Count: 2}, {Key: "empty", Count: 1}},
		ProjectsWithEvents: map[string]struct{}{"repo": {}},
	}}
	rec := do(t, usageServer(t, fs), "GET", "/api/v1/search-usage", "tok", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("search-usage = %d, body %s", rec.Code, rec.Body.String())
	}
	var report map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if len(report) == 0 {
		t.Error("report is empty")
	}
}

func TestSearchUsageAcceptsWindowAndProject(t *testing.T) {
	fs := &fakeStore{}
	if rec := do(t, usageServer(t, fs), "GET", "/api/v1/search-usage?days=7&project=repo", "tok", ""); rec.Code != http.StatusOK {
		t.Errorf("days=7 = %d, want 200", rec.Code)
	}
}

func TestSearchUsageRejectsBadWindow(t *testing.T) {
	srv := usageServer(t, &fakeStore{})
	for _, q := range []string{"days=0", "days=366", "days=abc", "days=-1"} {
		if rec := do(t, srv, "GET", "/api/v1/search-usage?"+q, "tok", ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", q, rec.Code)
		}
	}
}

func TestSearchUsageStoreFailure(t *testing.T) {
	fs := &fakeStore{usageErr: errors.New("db down")}
	if rec := do(t, usageServer(t, fs), "GET", "/api/v1/search-usage", "tok", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("aggregate failure = %d, want 500", rec.Code)
	}
}

func TestTenantUsageRequiresQuotaStore(t *testing.T) {
	// The base fake has no QuotaStore surface, so billing counters answer 501.
	if rec := do(t, usageServer(t, &fakeStore{}), "GET", "/api/v1/usage", "tok", ""); rec.Code != http.StatusNotImplemented {
		t.Errorf("store without quotas = %d, want 501", rec.Code)
	}
}
