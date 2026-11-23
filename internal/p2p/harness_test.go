package p2p

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/config"
	"github.com/Lunar-Park/lp-ferry/internal/ledger"
)

// testNode is one "machine": its own identity, ledger, allowed roots and a
// real mTLS listener on loopback.
type testNode struct {
	*Node
	home string
}

func newNode(t *testing.T, name string, roots ...config.Root) *testNode {
	t.Helper()
	home := t.TempDir()
	db, err := ledger.Open(filepath.Join(home, "ferry.db"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := LoadOrCreate(filepath.Join(home, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	n := &Node{Identity: id, Name: name, DB: db, Roots: func() []config.Root { return roots }, ProbeTimeout: 2 * time.Second}
	if err := n.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Close(); _ = db.Close() })
	return &testNode{Node: n, home: home}
}

// pair runs the real pairing flow: b opens a window, a redeems the code.
func pair(t *testing.T, a, b *testNode) {
	t.Helper()
	code, err := b.StartPairing(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Pair(context.Background(), code); err != nil {
		t.Fatal(err)
	}
}

func dirIn(t *testing.T, parent, name string) string {
	t.Helper()
	p := filepath.Join(parent, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	r, _ := filepath.EvalSymlinks(p)
	return r
}

func randFile(t *testing.T, p string, n int) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, n)
	_, _ = rand.Read(b)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func fileSHA(t *testing.T, p string) string {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	_, _ = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}

// proxy is a TCP relay standing in for one network path, so tests can cut
// a connection mid-file or drop the whole path.
type proxy struct {
	ln      net.Listener
	target  string
	mu      sync.Mutex
	conns   []net.Conn
	down    atomic.Int64 // server -> client bytes relayed
	limit   atomic.Int64 // cut connections once down reaches this (0 = never)
	dropAll atomic.Bool  // when cutting, also close the listener (path gone)
	cuts    atomic.Int32
}

func newProxy(t *testing.T, target string) *proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &proxy{ln: ln, target: target}
	go p.serve()
	t.Cleanup(func() { _ = ln.Close(); p.cutAll() })
	return p
}

func (p *proxy) Addr() string { return p.ln.Addr().String() }

func (p *proxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		s, err := net.Dial("tcp", p.target)
		if err != nil {
			_ = c.Close()
			continue
		}
		p.mu.Lock()
		p.conns = append(p.conns, c, s)
		p.mu.Unlock()
		go func() { _, _ = io.Copy(s, c); _ = s.Close() }()
		go func() {
			buf := make([]byte, 32<<10)
			for {
				n, err := s.Read(buf)
				if n > 0 {
					if _, werr := c.Write(buf[:n]); werr != nil {
						break
					}
					if total := p.down.Add(int64(n)); p.limit.Load() > 0 && total >= p.limit.Load() {
						p.limit.Store(0)
						p.cuts.Add(1)
						if p.dropAll.Load() {
							_ = p.ln.Close()
						}
						p.cutAll()
						return
					}
				}
				if err != nil {
					break
				}
			}
			_ = c.Close()
		}()
	}
}

func (p *proxy) cutAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}
