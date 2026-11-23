package p2p

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/ledger"
)

// ErrRedirect is returned for any redirect: a grant never leaves the peer
// it was issued for.
var ErrRedirect = errors.New("peer answered with a redirect; refused")

// Client talks to one pinned peer over its best path.
type Client struct {
	n     *Node
	Peer  string
	pub   ed25519.PublicKey
	Radar *Radar

	tr *http.Transport
	hc *http.Client
}

// Client returns the cached client for a pinned peer.
func (n *Node) Client(ctx context.Context, peerID string) (*Client, error) {
	n.mu.Lock()
	if c := n.clients[peerID]; c != nil {
		n.mu.Unlock()
		return c, nil
	}
	n.mu.Unlock()
	p, err := ledger.GetPeer(ctx, n.DB.Read(), peerID)
	if err != nil {
		return nil, err
	}
	if p.Revoked {
		return nil, fmt.Errorf("peer %s is revoked", peerID)
	}
	raw, err := base64.StdEncoding.DecodeString(p.PubKeyB64)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("peer %s has a malformed pinned key", peerID)
	}
	c := &Client{n: n, Peer: peerID, pub: ed25519.PublicKey(raw)}
	c.Radar = &Radar{Probe: c.probe, Timeout: n.ProbeTimeout}
	var eps []Endpoint
	for _, s := range p.Addresses {
		if e, err := ParseEndpoint(s); err == nil {
			eps = append(eps, e)
		}
	}
	c.Radar.SetCandidates(eps)
	c.tr = c.transport(func() (Endpoint, error) {
		if ep, ok := c.Radar.Active(); ok {
			return ep, nil
		}
		return c.Radar.Select(context.Background())
	})
	c.hc = &http.Client{Transport: c.tr, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrRedirect }}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.clients == nil {
		n.clients = map[string]*Client{}
	}
	if existing := n.clients[peerID]; existing != nil {
		return existing, nil
	}
	n.clients[peerID] = c
	return c, nil
}

// AddCandidates merges discovered endpoints into the radar.
func (c *Client) AddCandidates(eps []Endpoint) {
	c.Radar.mu.Lock()
	cur := append([]Endpoint(nil), c.Radar.cands...)
	c.Radar.mu.Unlock()
	c.Radar.SetCandidates(append(cur, eps...))
}

func (c *Client) close() { c.tr.CloseIdleConnections() }

func (c *Client) tlsConfig() *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{c.n.Identity.Cert},
		InsecureSkipVerify: true, //nolint:gosec // verified against the pinned key below; there is no CA
		ServerName:         "ferry.peer",
		NextProtos:         []string{"h2", "http/1.1"},
		VerifyConnection: func(cs tls.ConnectionState) error {
			pub, err := pubFromCerts(cs.PeerCertificates)
			if err != nil {
				return err
			}
			if !bytes.Equal(pub, c.pub) {
				return fmt.Errorf("%w: expected %s, got %s", ErrUnpaired, c.Peer, PeerIDOf(pub))
			}
			return nil
		},
	}
}

// bigBuffers raises socket buffers for high bandwidth-delay paths.
func bigBuffers(_, _ string, rc syscall.RawConn) error {
	return rc.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, 8<<20)
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 8<<20)
	})
}

func (c *Client) transport(pick func() (Endpoint, error)) *http.Transport {
	cfg := c.tlsConfig()
	return &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   cfg,
		DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			ep, err := pick()
			if err != nil {
				return nil, err
			}
			d := &net.Dialer{Timeout: 3 * time.Second, Control: bigBuffers, KeepAlive: 15 * time.Second}
			raw, err := d.DialContext(ctx, "tcp", ep.Addr)
			if err != nil {
				return nil, err
			}
			conn := tls.Client(raw, cfg.Clone())
			if err := conn.HandshakeContext(ctx); err != nil {
				_ = raw.Close()
				return nil, err
			}
			return conn, nil
		},
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
}

// probe measures one endpoint with a dedicated connection.
func (c *Client) probe(ctx context.Context, ep Endpoint) Measurement {
	m := Measurement{Endpoint: ep}
	tr := c.transport(func() (Endpoint, error) { return ep, nil })
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrRedirect }}
	start := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://ferry.peer/v1/probe?bytes=262144", nil)
	resp, err := hc.Do(req)
	if err != nil {
		m.Err = err.Error()
		return m
	}
	defer func() { _ = resp.Body.Close() }()
	m.RTT = time.Since(start)
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		m.Err = fmt.Sprintf("probe: %v %s", err, resp.Status)
		return m
	}
	if el := time.Since(start); el > 0 {
		m.BPS = float64(n) / el.Seconds()
	}
	return m
}

// RemoteError is a non-2xx answer from a peer.
type RemoteError struct {
	Status int
	Msg    string
}

func (e *RemoteError) Error() string { return fmt.Sprintf("peer answered %d: %s", e.Status, e.Msg) }

func netError(err error) bool {
	if err == nil {
		return false
	}
	var re *RemoteError
	if errors.As(err, &re) || errors.Is(err, ErrRedirect) || errors.Is(err, context.Canceled) || errors.Is(err, ErrUnpaired) {
		return false
	}
	return true
}

// Do sends a request on the active path. Idempotent requests that fail on
// the network fail over to the next path and retry.
func (c *Client) Do(ctx context.Context, method, path string, body []byte, hdr http.Header) (*http.Response, error) {
	attempts := 3
	var last error
	for i := 0; i < attempts; i++ {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		// The host is a fixed placeholder; DialTLSContext connects to the
		// pinned peer's radar-selected endpoint, never to this name.
		req, err := http.NewRequestWithContext(ctx, method, "https://ferry.peer"+path, rd) //nolint:gosec // G704: see above
		if err != nil {
			return nil, err
		}
		for k, v := range hdr {
			req.Header[k] = v
		}
		resp, err := c.hc.Do(req) //nolint:gosec // G704: pinned peer only
		if err == nil {
			if resp.StatusCode >= 300 {
				msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
				return nil, &RemoteError{Status: resp.StatusCode, Msg: string(bytes.TrimSpace(msg))}
			}
			c.touch()
			return resp, nil
		}
		last = err
		if !netError(err) || ctx.Err() != nil {
			return nil, err
		}
		if err := c.Failover(ctx); err != nil {
			return nil, fmt.Errorf("%w (after %w)", err, last)
		}
	}
	return nil, last
}

// Failover marks the active path down and selects the next best.
func (c *Client) Failover(ctx context.Context) error {
	ep, ok := c.Radar.Active()
	c.tr.CloseIdleConnections()
	var err error
	if ok {
		_, err = c.Radar.MarkDown(ctx, ep)
	} else {
		_, err = c.Radar.Select(ctx)
	}
	if next, ok := c.Radar.Active(); ok {
		c.n.emit("peer.changed", map[string]any{"peer_id": c.Peer, "transport_badge": next.Badge, "addr": next.Addr, "failover": true})
	}
	return err
}

func (c *Client) touch() {
	ep, ok := c.Radar.Active()
	if !ok {
		return
	}
	_ = c.n.DB.WriteTx(context.Background(), func(tx *sql.Tx) error { return ledger.TouchPeer(tx, c.Peer, ep.Badge) })
}

// JSON posts a JSON body and decodes a JSON answer.
func (c *Client) JSON(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	resp, err := c.Do(ctx, method, path, body, http.Header{"Content-Type": {"application/json"}})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ProbeInfo is the /v1/probe answer without a payload.
type ProbeInfo struct {
	PeerID       string   `json:"peer_id"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
}

// Info fetches the peer's identity and capabilities.
func (c *Client) Info(ctx context.Context) (*ProbeInfo, error) {
	var p ProbeInfo
	return &p, c.JSON(ctx, http.MethodGet, "/v1/probe", nil, &p)
}

// Fetch GETs a granted item from byte offset (Range: bytes=offset-). The
// full-file SHA-256 arrives as the X-Ferry-SHA256 trailer once the body
// has been read to EOF.
func (c *Client) Fetch(ctx context.Context, transferID, itemID, grant string, offset int64) (*http.Response, error) {
	hdr := http.Header{"X-Ferry-Grant": {grant}}
	if offset > 0 {
		hdr.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	return c.Do(ctx, http.MethodGet, "/v1/transfers/"+transferID+"/items/"+itemID, nil, hdr)
}

func randomPayload(n int64) io.Reader { return io.LimitReader(rand.Reader, n) }
