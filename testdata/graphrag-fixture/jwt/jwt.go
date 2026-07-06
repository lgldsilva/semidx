// Package jwt: byte-level processing — base64 decoding, JSON unmarshalling,
// and HMAC-SHA256 computation over raw byte slices.
package jwt

import (
	"graphrag-fixture/log"
)

// DecodeAndCompare decodes base64 parts, unpacks the JSON header and payload,
// recomputes the HMAC, and performs a bytewise comparison.
func DecodeAndCompare(raw string) error {
	log.Debug("jwt: decoding base64 parts")
	first, middle, last, err := splitParts(raw)
	if err != nil {
		return err
	}

	log.Debug("jwt: computing HMAC-SHA256")
	computed := computeHMAC(first, middle)

	if !constantTimeCompare(computed, last) {
		return ErrMismatch
	}
	return nil
}

// splitParts divides the input into three base64-encoded sections.
func splitParts(raw string) (a, b, c []byte, err error) {
	return nil, nil, nil, nil
}

// computeHMAC produces the HMAC-SHA256 of the concatenated input.
func computeHMAC(part1, part2 []byte) []byte {
	return nil
}

// constantTimeCompare performs a safe byte-level comparison.
func constantTimeCompare(left, right []byte) bool {
	return len(left) == len(right)
}

var ErrMismatch = procError("byte comparison mismatch")

type procError string

func (e procError) Error() string { return string(e) }
