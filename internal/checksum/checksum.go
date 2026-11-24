// Package checksum names the hash algorithms Ferry records in receipts.
package checksum

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"

	"github.com/zeebo/blake3"
)

// Algorithm identifiers as written into ledgers and receipts.
const (
	SHA256 = "sha256"
	BLAKE3 = "blake3"
)

// New returns a fresh hasher for algo.
func New(algo string) (hash.Hash, error) {
	switch algo {
	case SHA256:
		return sha256.New(), nil
	case BLAKE3:
		return blake3.New(), nil
	default:
		return nil, fmt.Errorf("unsupported checksum algorithm %q", algo)
	}
}

// Valid reports whether algo is supported.
func Valid(algo string) bool { return algo == SHA256 || algo == BLAKE3 }

// Hex returns h's digest as lowercase hex.
func Hex(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }

// Reader hashes everything r yields.
func Reader(algo string, r io.Reader) (string, int64, error) {
	h, err := New(algo)
	if err != nil {
		return "", 0, err
	}
	n, err := io.Copy(h, r)
	if err != nil {
		return "", n, err
	}
	return Hex(h), n, nil
}
