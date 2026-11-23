package p2p

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/config"
	"github.com/Lunar-Park/lp-ferry/internal/ledger"
)

// ErrUnpaired is a TLS handshake refusal for an unknown key.
var ErrUnpaired = errors.New("peer key is not paired")

// Node is one machine's P2P endpoint.
type Node struct {
	Identity *Identity
	Name     string
	DB       *ledger.DB
	// Roots returns the allowed roots (read live, so config edits apply).
	Roots func() []config.Root
	// Endpoints optionally overrides the advertised endpoints (tests, or a
	// machine whose listener sits behind a known address).
	Endpoints func() []Endpoint
	// ProbeTimeout bounds each Path Radar probe (default 500ms).
	ProbeTimeout time.Duration
	OnEvent      func(kind string, data any)

	mu      sync.Mutex
	pairing *pairWindow
	srv     *http.Server
	lns     []net.Listener
	clients map[string]*Client
}

type pairWindow struct {
	secret  []byte
	expires time.Time
	used    bool
}

type ctxKey struct{}

// PeerOf returns the authenticated peer ID of a request.
func PeerOf(r *http.Request) string {
	s, _ := r.Context().Value(ctxKey{}).(string)
	return s
}

func (n *Node) emit(kind string, data any) {
	if n.OnEvent != nil {
		n.OnEvent(kind, data)
	}
}

// pinned reports whether pub is a pinned, unrevoked peer key.
func (n *Node) pinned(id string, pub ed25519.PublicKey) bool {
	p, err := ledger.GetPeer(context.Background(), n.DB.Read(), id)
	if err != nil || p.Revoked {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(p.PubKeyB64)
	return err == nil && bytes.Equal(raw, pub)
}

func (n *Node) pairingOpen() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.pairing != nil && !n.pairing.used && time.Now().Before(n.pairing.expires)
}

func (n *Node) serverTLS() *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{n.Identity.Cert},
		ClientAuth:   tls.RequireAnyClientCert,
		NextProtos:   []string{"h2", "http/1.1"},
		// No resumption: every connection presents and re-proves its key.
		SessionTicketsDisabled: true,
		// VerifyConnection runs on every handshake, resumed or not.
		VerifyConnection: func(cs tls.ConnectionState) error {
			pub, err := pubFromCerts(cs.PeerCertificates)
			if err != nil {
				return err
			}
			if n.pinned(PeerIDOf(pub), pub) || n.pairingOpen() {
				return nil
			}
			return fmt.Errorf("%w: %s", ErrUnpaired, PeerIDOf(pub))
		},
	}
}

// Listen starts the mTLS HTTP/2 server on each address ("127.0.0.1:0"
// picks a port). Every listener serves the same node.
func (n *Node) Listen(addrs ...string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.srv == nil {
		n.srv = &http.Server{Handler: n.routes(), TLSConfig: n.serverTLS(), ReadHeaderTimeout: 10 * time.Second}
		n.srv.Protocols = new(http.Protocols)
		n.srv.Protocols.SetHTTP1(true)
		n.srv.Protocols.SetHTTP2(true)
	}
	for _, a := range addrs {
		ln, err := net.Listen("tcp", a)
		if err != nil {
			return err
		}
		n.lns = append(n.lns, ln)
		go func() { _ = n.srv.ServeTLS(ln, "", "") }()
	}
	return nil
}

// Addrs lists the listening addresses.
func (n *Node) Addrs() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, 0, len(n.lns))
	for _, l := range n.lns {
		out = append(out, l.Addr().String())
	}
	return out
}

// Close stops the server and every client.
func (n *Node) Close() error {
	n.mu.Lock()
	srv := n.srv
	clients := n.clients
	n.clients = nil
	n.mu.Unlock()
	for _, c := range clients {
		c.close()
	}
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			return srv.Close()
		}
	}
	return nil
}

// AdvertisedEndpoints is what this node tells peers to dial.
func (n *Node) AdvertisedEndpoints() []Endpoint {
	if n.Endpoints != nil {
		return n.Endpoints()
	}
	var out []Endpoint
	for _, a := range n.Addrs() {
		host, port, _ := net.SplitHostPort(a)
		ip := net.ParseIP(host)
		if ip != nil && ip.IsUnspecified() {
			out = append(out, LocalEndpoints(port)...)
			continue
		}
		out = append(out, Endpoint{Addr: a, Tier: TierFor(ip, ""), Badge: badgeFor(TierFor(ip, ""))})
	}
	return out
}

// ---------------------------------------------------------------------
// Pairing
// ---------------------------------------------------------------------

const pairExporter = "EXPORTER-ferry-pair-v1"

func pairProof(secret, ekm []byte, role, clientID, serverID string) []byte {
	m := hmac.New(sha256.New, secret)
	fmt.Fprintf(m, "ferry-pair-v1|%s|%s|%s|", role, clientID, serverID)
	m.Write(ekm)
	return m.Sum(nil)
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// StartPairing opens a single-use pairing window and returns the code to
// give the other machine: ferry://pair/<peer_id>?code=<secret>&ep=<endpoint>...
func (n *Node) StartPairing(ttl time.Duration) (string, error) {
	secret := make([]byte, 16) // 128 bits
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	n.mu.Lock()
	n.pairing = &pairWindow{secret: secret, expires: time.Now().Add(ttl)}
	n.mu.Unlock()
	q := url.Values{}
	q.Set("code", b32.EncodeToString(secret))
	for _, e := range n.AdvertisedEndpoints() {
		q.Add("ep", e.String())
	}
	return "ferry://pair/" + n.Identity.ID + "?" + q.Encode(), nil
}

type pairRequest struct {
	PeerID    string   `json:"peer_id"`
	Name      string   `json:"name"`
	Endpoints []string `json:"endpoints"`
	Proof     []byte   `json:"proof"`
}

func (n *Node) handlePair(w http.ResponseWriter, r *http.Request) {
	clientID, pub, err := PeerFromState(r.TLS)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var req pairRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.PeerID != clientID {
		http.Error(w, "bad pairing request", http.StatusBadRequest)
		return
	}
	ekm, err := r.TLS.ExportKeyingMaterial(pairExporter, nil, 32)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	n.mu.Lock()
	pw := n.pairing
	ok := pw != nil && !pw.used && time.Now().Before(pw.expires) &&
		hmac.Equal(req.Proof, pairProof(pw.secret, ekm, "client", clientID, n.Identity.ID))
	if ok {
		pw.used = true // single use, even if what follows fails
	}
	n.mu.Unlock()
	if !ok {
		http.Error(w, "pairing refused", http.StatusForbidden)
		return
	}
	if err := n.pin(clientID, req.Name, pub, req.Endpoints); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	eps := []string{}
	for _, e := range n.AdvertisedEndpoints() {
		eps = append(eps, e.String())
	}
	writeJSON(w, pairRequest{PeerID: n.Identity.ID, Name: n.Name, Endpoints: eps,
		Proof: pairProof(pw.secret, ekm, "server", clientID, n.Identity.ID)})
	n.emit("peer.changed", map[string]any{"peer_id": clientID, "state": "paired"})
}

func (n *Node) pin(id, name string, pub ed25519.PublicKey, endpoints []string) error {
	return n.DB.WriteTx(context.Background(), func(tx *sql.Tx) error {
		return ledger.UpsertPeer(tx, &ledger.Peer{ID: id, Name: name, PubKeyB64: base64.StdEncoding.EncodeToString(pub), Addresses: endpoints})
	})
}

// Pair completes pairing from a code produced by StartPairing on the other
// machine. The other side's key must match the ID in the code, and both
// sides prove the secret over the TLS channel binding, so a
// man-in-the-middle cannot complete it.
func (n *Node) Pair(ctx context.Context, code string) (*ledger.Peer, error) {
	u, err := url.Parse(code)
	if err != nil || u.Scheme != "ferry" || u.Host != "pair" {
		return nil, errors.New("not a ferry pairing code")
	}
	serverID := strings.Trim(u.Path, "/")
	secret, err := b32.DecodeString(u.Query().Get("code"))
	if err != nil || len(secret) != 16 {
		return nil, errors.New("pairing code secret is malformed")
	}
	var lastErr error
	for _, raw := range u.Query()["ep"] {
		ep, err := ParseEndpoint(raw)
		if err != nil {
			continue
		}
		p, err := n.pairWith(ctx, ep, serverID, secret, u.Query()["ep"])
		if err == nil {
			return p, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("pairing code lists no endpoints")
	}
	return nil, lastErr
}

func (n *Node) pairWith(ctx context.Context, ep Endpoint, serverID string, secret []byte, endpoints []string) (*ledger.Peer, error) {
	var serverPub ed25519.PublicKey
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{n.Identity.Cert},
		InsecureSkipVerify: true, //nolint:gosec // the server key is verified against the peer ID below
		NextProtos:         []string{"http/1.1"},
		ServerName:         "ferry.peer",
		VerifyConnection: func(cs tls.ConnectionState) error {
			pub, err := pubFromCerts(cs.PeerCertificates)
			if err != nil {
				return err
			}
			if PeerIDOf(pub) != serverID {
				return fmt.Errorf("server key is %s, the pairing code names %s", PeerIDOf(pub), serverID)
			}
			serverPub = pub
			return nil
		},
	}
	d := &net.Dialer{Timeout: 3 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", ep.Addr)
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, cfg)
	defer func() { _ = conn.Close() }()
	if err := conn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	cs := conn.ConnectionState()
	ekm, err := cs.ExportKeyingMaterial(pairExporter, nil, 32)
	if err != nil {
		return nil, err
	}
	mine := []string{}
	for _, e := range n.AdvertisedEndpoints() {
		mine = append(mine, e.String())
	}
	body, _ := json.Marshal(pairRequest{PeerID: n.Identity.ID, Name: n.Name, Endpoints: mine,
		Proof: pairProof(secret, ekm, "client", n.Identity.ID, serverID)})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://ferry.peer/v1/pair", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if err := req.Write(conn); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pairing refused by %s: %s", serverID, resp.Status)
	}
	var out pairRequest
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !hmac.Equal(out.Proof, pairProof(secret, ekm, "server", n.Identity.ID, serverID)) {
		return nil, errors.New("the other machine could not prove the pairing secret")
	}
	eps := endpoints
	if len(out.Endpoints) > 0 {
		eps = out.Endpoints
	}
	if err := n.pin(serverID, out.Name, serverPub, eps); err != nil {
		return nil, err
	}
	n.emit("peer.changed", map[string]any{"peer_id": serverID, "state": "paired"})
	return ledger.GetPeer(ctx, n.DB.Read(), serverID)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
