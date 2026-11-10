package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serveJSON(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A provider returning fewer vectors than inputs used to be passed through, and
// callers indexing embeddings[i] per chunk panicked out of range.
func TestOpenAIEmbedRejectsShortResponse(t *testing.T) {
	srv := serveJSON(t, `{"data":[{"index":0,"embedding":[1,2]}]}`)
	if _, err := NewOpenAIClient(srv.URL, "").Embed(context.Background(), "m", "a", "b"); err == nil {
		t.Fatal("want error for 1 vector / 2 inputs")
	}
}

// OpenAI-compatible APIs may return data out of order; the index field is the
// authority.
func TestOpenAIEmbedOrdersByIndex(t *testing.T) {
	srv := serveJSON(t, `{"data":[{"index":1,"embedding":[2]},{"index":0,"embedding":[1]}]}`)
	got, err := NewOpenAIClient(srv.URL, "").Embed(context.Background(), "m", "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if got[0][0] != 1 || got[1][0] != 2 {
		t.Fatalf("vectors not ordered by index: %v", got)
	}
}

func TestOpenAIEmbedRejectsBadIndex(t *testing.T) {
	srv := serveJSON(t, `{"data":[{"index":0,"embedding":[1]},{"index":0,"embedding":[2]}]}`)
	if _, err := NewOpenAIClient(srv.URL, "").Embed(context.Background(), "m", "a", "b"); err == nil {
		t.Fatal("want error for duplicate index")
	}
}

func TestOllamaEmbedRejectsShortResponse(t *testing.T) {
	srv := serveJSON(t, `{"embeddings":[[1,2]]}`)
	if _, err := NewOllamaClient(srv.URL).Embed(context.Background(), "m", "a", "b"); err == nil {
		t.Fatal("want error for 1 vector / 2 inputs")
	}
}
