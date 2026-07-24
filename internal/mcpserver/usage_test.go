package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/internal/usage"
	"github.com/lgldsilva/semidx/pkg/client"
)

type memUsageStore struct {
	agg usage.Aggregate
	err error
}

func (s memUsageStore) RecordUsageEvent(context.Context, usage.Event) error { return nil }
func (s memUsageStore) UsageAggregate(context.Context, time.Time, string, int) (usage.Aggregate, error) {
	return s.agg, s.err
}

func TestLocalUsageAndHandler(t *testing.T) {
	t.Parallel()
	us := memUsageStore{agg: usage.Aggregate{Total: 2, ByProject: []usage.Count{{Key: "p", Count: 2}}}}
	b := WithUsage(&stubBackend{}, us)
	ub, ok := b.(UsageBackend)
	if !ok {
		t.Fatal("expected UsageBackend")
	}
	report, err := ub.Usage(context.Background(), 7, "p")
	if err != nil || report.Total != 2 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if WithUsage(&stubBackend{}, nil) == nil {
		t.Fatal("nil store should return original backend")
	}

	h := usageHandler(ub)
	res, _, err := h(context.Background(), &mcp.CallToolRequest{}, usageArgs{Days: 0})
	if err != nil || res == nil {
		t.Fatalf("handler: %v %#v", err, res)
	}
	res, _, err = h(context.Background(), &mcp.CallToolRequest{}, usageArgs{Days: 999})
	if err != nil || res == nil {
		t.Fatal("expected days validation error result")
	}

	failing := usageHandler(localUsage{store: memUsageStore{err: errors.New("boom")}})
	res, _, err = failing(context.Background(), &mcp.CallToolRequest{}, usageArgs{Days: 3})
	if err != nil || res == nil {
		t.Fatal("expected error result")
	}
}

func TestRemoteUsageAndWithRemote(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/search-usage", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total":   1,
			"summary": "ok",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := client.New(srv.URL, "tok")
	b := WithRemoteUsage(&stubBackend{}, c)
	ub, ok := b.(UsageBackend)
	if !ok {
		t.Fatal("expected UsageBackend")
	}
	report, err := ub.Usage(context.Background(), 30, "")
	if err != nil || report.Total != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if WithRemoteUsage(&stubBackend{}, nil) == nil {
		t.Fatal("nil client")
	}
	ua, ok := b.(*usageAwareBackend)
	if !ok || ua.Unwrap() == nil {
		t.Fatal("unwrap")
	}
}
