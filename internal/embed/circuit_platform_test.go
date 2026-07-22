package embed

import (
	"errors"
	"testing"
	"time"
)

func TestRetryableErrorContract(t *testing.T) {
	cause := errors.New("provider unavailable")
	err := &RetryableError{Err: cause, After: 3 * time.Second}
	if err.Error() != cause.Error() {
		t.Errorf("Error() = %q, want %q", err.Error(), cause.Error())
	}
	if !errors.Is(err, cause) {
		t.Error("RetryableError should unwrap its cause")
	}
	if err.RetryAfter() != 3*time.Second {
		t.Errorf("RetryAfter() = %s", err.RetryAfter())
	}
}
