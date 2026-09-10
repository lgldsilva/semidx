package client

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestFilesBatchAsyncSurfacesServerErrorMessage(t *testing.T) {
	c, closeFn := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"payload too large"}`))
	})
	defer closeFn()

	_, err := c.FilesBatchAsyncWithInventory(context.Background(), "proj",
		[]BatchFile{{Path: "a.go", Content: "x"}}, nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", apiErr.Status)
	}
	if apiErr.Message != "payload too large" {
		t.Errorf("Message = %q, want %q", apiErr.Message, "payload too large")
	}
}

func TestServerErrorMessage(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"json error", `{"error":"payload too large"}`, "payload too large"},
		{"proxy html", "<html>413 Request Entity Too Large</html>", "<html>413 Request Entity Too Large</html>"},
		{"malformed json", `{"error":`, `{"error":`},
		{"empty body", "", ""},
	}
	for _, tc := range cases {
		if got := serverErrorMessage(strings.NewReader(tc.body)); got != tc.want {
			t.Errorf("%s: serverErrorMessage = %q, want %q", tc.name, got, tc.want)
		}
	}
}
