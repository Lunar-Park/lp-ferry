// Package p2p is Ferry's machine-to-machine transport: Ed25519 identities,
// channel-bound pairing, mutual TLS 1.3 with pinned keys (no CA, no cloud,
// no relay), per-transfer signed capability grants, Path Radar endpoint
// selection with failover, and HTTP/2 streaming with Range resume.
package p2p

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Identity is this machine's long-lived key and the TLS certificate that
// carries it.
type Identity struct {
	Priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
	ID   string
	Cert tls.Certificate
}

// PeerIDOf derives a readable peer ID from a public key.
func PeerIDOf(pub ed25519.PublicKey) string {
	s := sha256.Sum256(pub)
	return "fry_" + hex.EncodeToString(s[:8])
}

// NewIdentity creates a fresh identity in memory.
func NewIdentity() (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return fromKey(priv, pub)
}

func fromKey(priv ed25519.PrivateKey, pub ed25519.PublicKey) (*Identity, error) {
	id := PeerIDOf(pub)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: id},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(20 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"ferry.peer"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return nil, err
	}
	return &Identity{Priv: priv, Pub: pub, ID: id, Cert: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}}, nil
}

// LoadOrCreate reads the identity key at path (PKCS#8 PEM, 0600) or
// creates one. A key file readable by others is refused.
func LoadOrCreate(path string) (*Identity, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		id, err := NewIdentity()
		if err != nil {
			return nil, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(id.Priv)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
			_ = f.Close()
			return nil, err
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return nil, err
		}
		return id, f.Close()
	}
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s is accessible by other users (%v); refusing to use it", path, fi.Mode().Perm())
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%s: not a PEM key", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an Ed25519 key", path)
	}
	return fromKey(priv, priv.Public().(ed25519.PublicKey))
}

// pubFromCerts checks the presented leaf like pubFromRaw.
func pubFromCerts(certs []*x509.Certificate) (ed25519.PublicKey, error) {
	if len(certs) == 0 {
		return nil, errors.New("peer presented no certificate")
	}
	return pubFromRaw([][]byte{certs[0].Raw})
}

// pubFromRaw extracts the Ed25519 key from a raw peer certificate.
func pubFromRaw(raw [][]byte) (ed25519.PublicKey, error) {
	if len(raw) == 0 {
		return nil, errors.New("peer presented no certificate")
	}
	cert, err := x509.ParseCertificate(raw[0])
	if err != nil {
		return nil, err
	}
	pub, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("peer certificate key is not Ed25519")
	}
	// Self-signed leaf: verify the signature with the certificate's own key
	// (no CA semantics; the key itself is what gets pinned).
	if err := cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
		return nil, fmt.Errorf("peer certificate is not self-signed by its key: %w", err)
	}
	return pub, nil
}

// PeerFromState returns the authenticated peer of a TLS connection.
func PeerFromState(cs *tls.ConnectionState) (string, ed25519.PublicKey, error) {
	if cs == nil || len(cs.PeerCertificates) == 0 {
		return "", nil, errors.New("no peer certificate")
	}
	pub, ok := cs.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
	if !ok {
		return "", nil, errors.New("peer key is not Ed25519")
	}
	return PeerIDOf(pub), pub, nil
}
