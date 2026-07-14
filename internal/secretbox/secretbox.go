// Package secretbox is an AES-256-GCM vault for RECOVERABLE secrets —
// values the server must read back in cleartext, such as per-project git
// credentials used to authenticate clone/pull against a remote host. This is
// deliberately different from api_tokens, which are one-way hashes: a hash
// can only be verified, never recovered, so it cannot authenticate semidx
// against a third party.
//
// Key model: the operator supplies a single master key via SEMIDX_SECRET_KEY
// (exactly 32 bytes, encoded as hex or base64). The AES key is NOT the master
// key itself — it is derived with HKDF-SHA256 using a versioned info string
// ("semidx/git-credentials/v1"), so a future key-rotation scheme can derive
// new keys from the same master by bumping the version. When
// SEMIDX_SECRET_KEY is unset the box is disabled: New returns (nil, nil) and
// every Seal/Open on the nil box fails with a clear error.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

const (
	// masterKeySize is the required decoded length of SEMIDX_SECRET_KEY.
	masterKeySize = 32
	// nonceSize is the standard AES-GCM nonce length prepended to each blob.
	nonceSize = 12
	// currentKeyVersion tags blobs-at-rest with the derivation in use; bump it
	// (with a matching hkdfInfo) to rotate the derived key under the same
	// master key.
	currentKeyVersion = 1
)

// ErrDisabled is returned by Seal/Open when no master key is configured
// (nil or zero-value Box).
var ErrDisabled = errors.New("secretbox disabled: SEMIDX_SECRET_KEY not set")

// Box seals and opens secrets with AES-256-GCM under a key derived from the
// master key. The zero value and the nil pointer are both safe: Enabled
// reports false and Seal/Open return ErrDisabled.
type Box struct {
	aead    cipher.AEAD
	version int
}

// New builds a Box from the master key as configured in SEMIDX_SECRET_KEY.
// The key may be hex- or base64-encoded (std or url alphabet, padded or not)
// and must decode to exactly 32 bytes. An empty masterKey disables the box:
// New returns (nil, nil) and the nil Box rejects every Seal/Open.
func New(masterKey string) (*Box, error) {
	if masterKey == "" {
		return nil, nil
	}
	raw, err := decodeMasterKey(masterKey)
	if err != nil {
		return nil, err
	}
	aead, err := deriveAEAD(raw, currentKeyVersion)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead, version: currentKeyVersion}, nil
}

// Enabled reports whether the box can seal and open secrets. It is nil-safe:
// a nil *Box (the "no master key" state returned by New) reports false.
func (b *Box) Enabled() bool {
	return b != nil && b.aead != nil
}

// KeyVersion identifies the key-derivation version blobs are sealed under
// (1 for now). A disabled box reports 0.
func (b *Box) KeyVersion() int {
	if !b.Enabled() {
		return 0
	}
	return b.version
}

// Seal encrypts plain with AES-256-GCM under a fresh random nonce and returns
// nonce(12) || ciphertext+tag. Sealing the same plaintext twice yields
// different blobs. A disabled box returns ErrDisabled.
func (b *Box) Seal(plain []byte) ([]byte, error) {
	if !b.Enabled() {
		return nil, ErrDisabled
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secretbox: generating nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, plain, nil), nil
}

// Open decrypts a blob produced by Seal. It returns an error (never panics)
// when the blob is too short, was sealed under a different key, or has been
// tampered with in any way (GCM authentication failure).
func (b *Box) Open(blob []byte) ([]byte, error) {
	if !b.Enabled() {
		return nil, ErrDisabled
	}
	if len(blob) < nonceSize+b.aead.Overhead() {
		return nil, fmt.Errorf("secretbox: blob too short: %d bytes", len(blob))
	}
	plain, err := b.aead.Open(nil, blob[:nonceSize], blob[nonceSize:], nil)
	if err != nil {
		return nil, fmt.Errorf("secretbox: open: %w", err)
	}
	return plain, nil
}

// hkdfInfo is the HKDF-SHA256 info string for a derivation version. Keeping
// the version a parameter (instead of a constant string) is what makes future
// rotation possible: version N of a blob can always be re-derived and opened
// while new blobs are sealed under version N+1.
func hkdfInfo(version int) []byte {
	return fmt.Appendf(nil, "semidx/git-credentials/v%d", version)
}

// deriveAEAD expands the master key into a version-specific AES-256 key via
// HKDF-SHA256 and wraps it in a GCM AEAD.
func deriveAEAD(master []byte, version int) (cipher.AEAD, error) {
	key := make([]byte, masterKeySize)
	if _, err := io.ReadFull(hkdf.New(sha256.New, master, nil, hkdfInfo(version)), key); err != nil {
		return nil, fmt.Errorf("secretbox: deriving key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: gcm: %w", err)
	}
	return aead, nil
}

// decodeMasterKey accepts the master key as hex or base64 (std/url alphabet,
// with or without padding), requiring exactly masterKeySize decoded bytes.
// Hex is tried first so a 64-char hex key is never misread as base64.
func decodeMasterKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	decoders := []func(string) ([]byte, error){
		hex.DecodeString,
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
	}
	var wrongSizes []int
	for _, dec := range decoders {
		raw, err := dec(s)
		if err != nil {
			continue
		}
		if len(raw) == masterKeySize {
			return raw, nil
		}
		wrongSizes = append(wrongSizes, len(raw))
	}
	if len(wrongSizes) > 0 {
		return nil, fmt.Errorf(
			"secretbox: SEMIDX_SECRET_KEY must decode to exactly %d bytes (got %d); generate one with: openssl rand -hex %d",
			masterKeySize, wrongSizes[0], masterKeySize)
	}
	return nil, fmt.Errorf(
		"secretbox: SEMIDX_SECRET_KEY is neither valid hex nor base64; generate one with: openssl rand -hex %d",
		masterKeySize)
}
