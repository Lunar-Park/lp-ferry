package p2p

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GrantItem is one file a grant lets the grantee read.
type GrantItem struct {
	Root    string `json:"root"`
	Rel     string `json:"rel"`
	Size    int64  `json:"size"`
	MtimeNS int64  `json:"mtime_ns"`
}

// Grant is a per-transfer capability: issued and signed by the source
// peer, bound to one grantee peer, one transfer, an explicit item list and
// an expiry. A paired peer still cannot fetch anything it was not granted.
type Grant struct {
	GrantID    string               `json:"grant_id"`
	TransferID string               `json:"transfer_id"`
	Issuer     string               `json:"issuer_peer"`
	Grantee    string               `json:"grantee_peer"`
	ExpiresAt  time.Time            `json:"expires_at"`
	Items      map[string]GrantItem `json:"items"`
}

// Grant errors map to 403.
var (
	ErrGrantInvalid = errors.New("grant invalid")
	ErrGrantExpired = errors.New("grant expired")
	ErrWrongPeer    = errors.New("grant belongs to another peer")
	ErrWrongItem    = errors.New("item not in grant")
)

// SignGrant serializes and signs g with the issuer's key:
// base64url(json) "." base64url(ed25519 signature over the json).
func SignGrant(id *Identity, g Grant) (string, error) {
	if g.GrantID == "" {
		var b [8]byte
		_, _ = rand.Read(b[:])
		g.GrantID = "grt_" + hex.EncodeToString(b[:])
	}
	g.Issuer = id.ID
	body, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(id.Priv, body)
	enc := base64.RawURLEncoding
	return enc.EncodeToString(body) + "." + enc.EncodeToString(sig), nil
}

// VerifyGrant checks a token was issued by this node, is unexpired, and
// was presented by its grantee. It does not check the item; Lookup does.
func VerifyGrant(local *Identity, token, presenter string, now time.Time) (*Grant, error) {
	enc := base64.RawURLEncoding
	b64body, b64sig, ok := strings.Cut(token, ".")
	if !ok {
		return nil, fmt.Errorf("%w: malformed", ErrGrantInvalid)
	}
	body, err1 := enc.DecodeString(b64body)
	sig, err2 := enc.DecodeString(b64sig)
	if err1 != nil || err2 != nil {
		return nil, fmt.Errorf("%w: encoding", ErrGrantInvalid)
	}
	if !ed25519.Verify(local.Pub, body, sig) {
		return nil, fmt.Errorf("%w: signature is not this node's", ErrGrantInvalid)
	}
	var g Grant
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrGrantInvalid, err)
	}
	if g.Issuer != local.ID {
		return nil, fmt.Errorf("%w: issued by %s", ErrGrantInvalid, g.Issuer)
	}
	if !now.Before(g.ExpiresAt) {
		return nil, fmt.Errorf("%w at %s", ErrGrantExpired, g.ExpiresAt.Format(time.RFC3339))
	}
	if g.Grantee != presenter {
		return nil, fmt.Errorf("%w (%s, presented by %s)", ErrWrongPeer, g.Grantee, presenter)
	}
	return &g, nil
}

// Lookup returns the granted item or ErrWrongItem.
func (g *Grant) Lookup(itemID string) (GrantItem, error) {
	it, ok := g.Items[itemID]
	if !ok {
		return GrantItem{}, fmt.Errorf("%w: %s", ErrWrongItem, itemID)
	}
	return it, nil
}
