package passwd

import (
	"strings"
	"testing"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	h, err := Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Errorf("unexpected encoding: %s", h)
	}
	ok, err := Verify("correct horse battery staple", h)
	if err != nil || !ok {
		t.Errorf("Verify(correct) = %v, %v; want true, nil", ok, err)
	}
}

func TestVerifyWrongPassword(t *testing.T) {
	h, _ := Hash("s3cret")
	ok, err := Verify("wrong", h)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("Verify(wrong) = true; want false")
	}
}

func TestHashIsSalted(t *testing.T) {
	a, _ := Hash("same")
	b, _ := Hash("same")
	if a == b {
		t.Error("two hashes of the same password are identical — salt not applied")
	}
}

func TestVerifyMalformed(t *testing.T) {
	for _, bad := range []string{"", "not-a-hash", "$argon2id$v=19$bad", "$bcrypt$x$y$z$w$v"} {
		if _, err := Verify("x", bad); err == nil {
			t.Errorf("Verify with %q: expected error", bad)
		}
	}
}
