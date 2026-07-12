package passwd

import (
	"errors"
	"flag"
	"os"
	"testing"

	"pgregory.net/rapid"
)

// TestMain caps rapid's iteration count for this package. argon2id at the
// production parameters (64 MiB / 3 passes) costs ~250 ms per call under -race,
// so the default 100 checks would make the property test take minutes; a smaller
// sample still exercises the invariant across varied inputs quickly.
func TestMain(m *testing.M) {
	if f := flag.Lookup("rapid.checks"); f != nil {
		_ = f.Value.Set("12")
	}
	os.Exit(m.Run())
}

// TestHashVerifyProperties is a property-based check of the core invariants:
// for ANY password, (1) Hash then Verify with the same password is true, (2)
// Verify with a different password is false, and (3) two hashes of the same
// password differ (the salt is applied).
func TestHashVerifyProperties(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		pw := rapid.StringN(0, 64, -1).Draw(rt, "password")

		h, err := Hash(pw)
		if err != nil {
			rt.Fatalf("Hash(%q) error: %v", pw, err)
		}
		ok, err := Verify(pw, h)
		if err != nil || !ok {
			rt.Fatalf("Verify(correct) = %v, %v; want true, nil", ok, err)
		}
		// A different password must not verify.
		if ok, err := Verify(pw+"x", h); err != nil || ok {
			rt.Fatalf("Verify(wrong) = %v, %v; want false, nil", ok, err)
		}
		// Salted: a second hash of the same password is a distinct encoding.
		h2, err := Hash(pw)
		if err != nil {
			rt.Fatalf("second Hash error: %v", err)
		}
		if h == h2 {
			rt.Fatal("two hashes of the same password are identical — salt not applied")
		}
	})
}

// TestVerifyMalformedVariants covers each distinct parse-failure branch of Verify
// using otherwise well-formed six-part encodings that fail at one specific stage.
func TestVerifyMalformedVariants(t *testing.T) {
	cases := map[string]string{
		"version mismatch":   "$argon2id$v=18$m=1,t=1,p=1$c2FsdA$aGFzaA",
		"version unparsable": "$argon2id$vx$m=1,t=1,p=1$c2FsdA$aGFzaA",
		"params unparsable":  "$argon2id$v=19$bogus$c2FsdA$aGFzaA",
		"bad salt base64":    "$argon2id$v=19$m=1,t=1,p=1$!!!$aGFzaA",
		"bad hash base64":    "$argon2id$v=19$m=1,t=1,p=1$c2FsdA$!!!",
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			ok, err := Verify("x", encoded)
			if ok {
				t.Errorf("Verify(%q) = true; want false", encoded)
			}
			if !errors.Is(err, ErrMalformedHash) {
				t.Errorf("Verify(%q) err = %v; want ErrMalformedHash", encoded, err)
			}
		})
	}
}

// TestVerifyWellFormedDifferentParams exercises the successful parse path with
// non-default parameters and confirms a non-matching password returns false.
func TestVerifyWellFormedDifferentParams(t *testing.T) {
	// A valid encoding (parameters differ from the defaults) with a bogus hash:
	// parsing succeeds, the constant-time compare fails → (false, nil).
	encoded := "$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHQ$" +
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	ok, err := Verify("whatever", encoded)
	if err != nil {
		t.Fatalf("Verify returned err = %v; want nil (parse succeeded)", err)
	}
	if ok {
		t.Error("Verify matched a bogus hash")
	}
}
