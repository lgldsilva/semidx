package secretbox

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// testMasterKey is a fixed 32-byte key (hex) for deterministic tests.
const testMasterKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func mustBox(t *testing.T, masterKey string) *Box {
	t.Helper()
	b, err := New(masterKey)
	if err != nil {
		t.Fatalf("New(%q): %v", masterKey, err)
	}
	if !b.Enabled() {
		t.Fatalf("New(%q): box not enabled", masterKey)
	}
	return b
}

func TestNewEmptyKeyDisablesBox(t *testing.T) {
	b, err := New("")
	if err != nil {
		t.Fatalf("New(\"\") error = %v, want nil", err)
	}
	if b != nil {
		t.Fatalf("New(\"\") = %v, want nil box", b)
	}
	if b.Enabled() {
		t.Error("nil box Enabled() = true, want false")
	}
}

func TestNewAcceptsAllEncodings(t *testing.T) {
	raw, err := hex.DecodeString(testMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	encodings := map[string]string{
		"hex":            testMasterKey,
		"hex upper":      strings.ToUpper(testMasterKey),
		"base64 std":     base64.StdEncoding.EncodeToString(raw),
		"base64 std raw": base64.RawStdEncoding.EncodeToString(raw),
		"base64 url":     base64.URLEncoding.EncodeToString(raw),
		"base64 url raw": base64.RawURLEncoding.EncodeToString(raw),
		"whitespace":     "  " + testMasterKey + "\n",
	}
	sealed, err := mustBox(t, testMasterKey).Seal([]byte("cross-encoding"))
	if err != nil {
		t.Fatal(err)
	}
	for name, enc := range encodings {
		t.Run(name, func(t *testing.T) {
			b := mustBox(t, enc)
			// Same raw key regardless of encoding: blobs interoperate.
			plain, err := b.Open(sealed)
			if err != nil {
				t.Fatalf("Open across encodings: %v", err)
			}
			if string(plain) != "cross-encoding" {
				t.Errorf("plain = %q, want %q", plain, "cross-encoding")
			}
			if v := b.KeyVersion(); v != 1 {
				t.Errorf("KeyVersion() = %d, want 1", v)
			}
		})
	}
}

func TestNewRejectsBadKeys(t *testing.T) {
	cases := map[string]string{
		"hex 16 bytes":       "000102030405060708090a0b0c0d0e0f",
		"hex 33 bytes":       testMasterKey + "ff",
		"base64 64 bytes":    base64.StdEncoding.EncodeToString(make([]byte, 64)),
		"base64 16 bytes":    base64.StdEncoding.EncodeToString(make([]byte, 16)),
		"garbage":            "not-a-key!!! definitely not hex ~~~",
		"short word":         "hunter2",
		"almost hex":         strings.Replace(testMasterKey, "0", "g", 1),
		"valid b64 but tiny": "YWJj", // "abc", 3 bytes
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			b, err := New(key)
			if err == nil {
				t.Fatalf("New(%q) succeeded, want error", key)
			}
			if b != nil {
				t.Errorf("New(%q) returned non-nil box with error", key)
			}
			if !strings.Contains(err.Error(), "SEMIDX_SECRET_KEY") {
				t.Errorf("error %q does not mention SEMIDX_SECRET_KEY", err)
			}
		})
	}
}

func TestSealOpenRoundTripProperty(t *testing.T) {
	b := mustBox(t, testMasterKey)
	rapid.Check(t, func(t *rapid.T) {
		plain := rapid.SliceOfN(rapid.Byte(), 0, 4096).Draw(t, "plain")
		blob, err := b.Seal(plain)
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		got, err := b.Open(blob)
		if err != nil {
			t.Fatalf("Open(Seal(x)): %v", err)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("Open(Seal(x)) = %x, want %x", got, plain)
		}
	})
}

func TestTamperAnyBitFailsProperty(t *testing.T) {
	b := mustBox(t, testMasterKey)
	rapid.Check(t, func(t *rapid.T) {
		plain := rapid.SliceOfN(rapid.Byte(), 0, 256).Draw(t, "plain")
		blob, err := b.Seal(plain)
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		pos := rapid.IntRange(0, len(blob)-1).Draw(t, "pos")
		bit := rapid.IntRange(0, 7).Draw(t, "bit")
		blob[pos] ^= 1 << bit
		if _, err := b.Open(blob); err == nil {
			t.Fatalf("Open succeeded on blob with bit %d of byte %d flipped", bit, pos)
		}
	})
}

func TestOpenWithWrongKeyFails(t *testing.T) {
	blob, err := mustBox(t, testMasterKey).Seal([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	otherKey := strings.Repeat("ab", 32)
	if _, err := mustBox(t, otherKey).Open(blob); err == nil {
		t.Fatal("Open with a different master key succeeded, want error")
	}
}

func TestDisabledBoxErrors(t *testing.T) {
	boxes := map[string]*Box{
		"nil":        nil,
		"zero value": {},
	}
	for name, b := range boxes {
		t.Run(name, func(t *testing.T) {
			if b.Enabled() {
				t.Error("Enabled() = true, want false")
			}
			if v := b.KeyVersion(); v != 0 {
				t.Errorf("KeyVersion() = %d, want 0", v)
			}
			if _, err := b.Seal([]byte("x")); !errors.Is(err, ErrDisabled) {
				t.Errorf("Seal error = %v, want ErrDisabled", err)
			}
			if _, err := b.Open([]byte("x")); !errors.Is(err, ErrDisabled) {
				t.Errorf("Open error = %v, want ErrDisabled", err)
			}
		})
	}
}

func TestOpenShortBlobFails(t *testing.T) {
	b := mustBox(t, testMasterKey)
	for _, n := range []int{0, 1, nonceSize - 1, nonceSize, nonceSize + b.aead.Overhead() - 1} {
		if _, err := b.Open(make([]byte, n)); err == nil {
			t.Errorf("Open(%d-byte blob) succeeded, want error", n)
		}
	}
}

func TestSealIsNonDeterministic(t *testing.T) {
	b := mustBox(t, testMasterKey)
	plain := []byte("same plaintext")
	first, err := b.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two Seals of the same plaintext produced identical blobs (nonce reuse?)")
	}
	// Both must still open to the original.
	for _, blob := range [][]byte{first, second} {
		got, err := b.Open(blob)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("Open = %q, want %q", got, plain)
		}
	}
}

func TestBlobLayout(t *testing.T) {
	b := mustBox(t, testMasterKey)
	plain := []byte("layout")
	blob, err := b.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if want := nonceSize + len(plain) + b.aead.Overhead(); len(blob) != want {
		t.Errorf("blob length = %d, want nonce(%d) + plaintext(%d) + tag(%d) = %d",
			len(blob), nonceSize, len(plain), b.aead.Overhead(), want)
	}
}

func TestHKDFInfoIsVersioned(t *testing.T) {
	if got := string(hkdfInfo(1)); got != "semidx/git-credentials/v1" {
		t.Errorf("hkdfInfo(1) = %q, want %q", got, "semidx/git-credentials/v1")
	}
	if got := string(hkdfInfo(2)); got != "semidx/git-credentials/v2" {
		t.Errorf("hkdfInfo(2) = %q, want %q", got, "semidx/git-credentials/v2")
	}
	// Different versions must derive different keys: a blob sealed under v1
	// cannot open under a v2-derived AEAD.
	raw, err := hex.DecodeString(testMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := deriveAEAD(raw, 2)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := mustBox(t, testMasterKey).Seal([]byte("v1 secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v2.Open(nil, blob[:nonceSize], blob[nonceSize:], nil); err == nil {
		t.Error("v2-derived key opened a v1 blob — derivation is not version-separated")
	}
}
