package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// In pool mode (2+ Ollama URLs) a custom openai provider used to be a raw
// client: force-local (sensitive files) and global privacy requests were
// round-robined onto it, sending sensitive chunks to a remote API.
func TestPoolNeverSendsForceLocalToRemoteCustom(t *testing.T) {
	ollama := func() *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"embeddings":[[1]]}`))
		}))
		t.Cleanup(s.Close)
		return s
	}
	var remoteHits atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		remoteHits.Add(1)
		_, _ = w.Write([]byte(`{"data":[{"embedding":[9]}]}`))
	}))
	t.Cleanup(remote.Close)

	for _, tc := range []struct {
		name    string
		privacy bool
		force   bool
	}{{"force-local ctx", false, true}, {"global privacy", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			remoteHits.Store(0)
			e := NewChainFromConfig(ChainConfig{
				OllamaURLs: []string{ollama().URL, ollama().URL},
				Provider:   "openai", Endpoint: remote.URL, APIKey: "k",
				Privacy: tc.privacy,
			})
			ctx := WithForceLocal(context.Background(), tc.force)
			for i := 0; i < 6; i++ {
				if _, err := e.Embed(ctx, "m", "secret"); err != nil {
					t.Fatalf("embed %d: %v", i, err)
				}
				if _, err := e.EmbedSingle(ctx, "m", "secret"); err != nil {
					t.Fatalf("embedSingle %d: %v", i, err)
				}
			}
			if n := remoteHits.Load(); n != 0 {
				t.Fatalf("remote provider received %d privacy-restricted requests", n)
			}
		})
	}
}
