package p2p

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/checksum"
	"github.com/Lunar-Park/lp-ferry/internal/config"
	"github.com/Lunar-Park/lp-ferry/internal/engine"
	"github.com/Lunar-Park/lp-ferry/internal/ledger"
	"github.com/Lunar-Park/lp-ferry/internal/model"
	"github.com/Lunar-Park/lp-ferry/internal/planner"
	"github.com/Lunar-Park/lp-ferry/internal/preset"
	"github.com/Lunar-Park/lp-ferry/internal/receipt"
)

func TestIdentityPersistsAndRefusesLooseKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.key")
	a, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(path)
	if err != nil || a.ID != b.ID || !strings.HasPrefix(a.ID, "fry_") {
		t.Fatalf("%v %s %s", err, a.ID, b.ID)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", fi.Mode())
	}
	_ = os.Chmod(path, 0o644)
	if _, err := LoadOrCreate(path); err == nil {
		t.Fatal("a world-readable identity key was accepted")
	}
}

func TestPairingIsChannelBoundAndSingleUse(t *testing.T) {
	a, b := newNode(t, "studio"), newNode(t, "server")
	code, err := b.StartPairing(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// A wrong secret is refused.
	bad := strings.Replace(code, "code=", "code=AAAA", 1)
	bad = bad[:strings.Index(bad, "code=")+5] + strings.Repeat("A", 26) + bad[strings.Index(bad, "&"):]
	if _, err := a.Pair(context.Background(), bad); err == nil {
		t.Fatal("pairing with a wrong secret succeeded")
	}
	// The real code still works (a failed attempt does not burn it)...
	if _, err := a.Pair(context.Background(), code); err != nil {
		t.Fatal(err)
	}
	// ...exactly once.
	c := newNode(t, "intruder")
	if _, err := c.Pair(context.Background(), code); err == nil {
		t.Fatal("a pairing code was redeemed twice")
	}
	// A code naming a different key than the server's is refused before
	// any secret is sent.
	evil := strings.Replace(code, b.Identity.ID, c.Identity.ID, 1)
	if _, err := a.Pair(context.Background(), evil); err == nil {
		t.Fatal("paired with a server whose key does not match the code")
	}
	for _, n := range []*testNode{a, b} {
		peers, _ := ledger.ListPeers(context.Background(), n.DB.Read())
		if len(peers) != 1 {
			t.Fatalf("%s pinned %d peers", n.Name, len(peers))
		}
	}
}

// T3.1 — two daemons pair and discover each other, then a verified
// fan-out writes a local SSD and the remote peer simultaneously from a
// single read, and the remote copy is re-verified on the remote machine.
func TestT3_1_PairFanOutAndRemoteVerify(t *testing.T) {
	base := t.TempDir()
	card := dirIn(t, base, "CARD")
	ssd := dirIn(t, base, "SSD")
	nas := dirIn(t, base, "NAS")
	studio := newNode(t, "studio", config.Root{Path: card, Access: "ro"}, config.Root{Path: ssd, Access: "rw"})
	server := newNode(t, "server", config.Root{Path: nas, Access: "rw"})
	pair(t, studio, server)

	files := map[string]int{"CLIP/A001.MOV": 9<<20 + 123, "CLIP/A002.MOV": 3 << 20, "AUDIO/T1.WAV": 4096}
	sums := map[string]string{}
	for rel, n := range files {
		sums[rel] = randFile(t, filepath.Join(card, rel), n)
	}
	ctx := context.Background()
	remote := Remote{N: studio.Node}
	pl := &planner.Planner{DB: studio.DB, Presets: &preset.Service{DB: studio.DB}, Remote: remote}
	plan, err := pl.Plan(ctx, model.PlanRequest{
		Sources: []model.SourceSpec{{Location: model.Location{Path: card}, Label: "A-cam"}},
		Destinations: []model.DestinationSpec{
			{Location: model.Location{PeerID: "local", Path: ssd}},
			{Location: model.Location{PeerID: server.Identity.ID, Path: nas}, Label: "NAS"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if b := plan.Blocking(); len(b) != 0 {
		t.Fatalf("%+v", b)
	}
	if plan.Destinations[1].PublishStrategy == "" || plan.Destinations[1].FreeBytes == 0 {
		t.Fatalf("remote preflight missing: %+v", plan.Destinations[1])
	}
	if err := pl.Approve(ctx, plan.ID, plan.Fingerprint); err != nil {
		t.Fatal(err)
	}
	var opens atomic.Int32
	run := &engine.Runner{DB: studio.DB, ReceiptsDir: filepath.Join(base, "receipts"), Remote: remote,
		Hooks: engine.Hooks{At: func(p string, _ model.TransferItem) {
			if p == engine.PointSourceOpened {
				opens.Add(1)
			}
		}}}
	id, err := run.Create(ctx, plan.ID, plan.Fingerprint, "human")
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Run(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != model.TransferCompleted {
		items, _ := ledger.GetItems(ctx, studio.DB.Read(), id)
		for _, it := range items {
			t.Logf("%s %s %s", it.Entry.DestRelPath, it.Status, it.Error)
		}
		t.Fatalf("%+v", res)
	}
	if opens.Load() != int32(len(files)) {
		t.Fatalf("source opened %d times for %d files; fan-out reads once", opens.Load(), len(files))
	}
	for rel, sum := range files {
		_ = sum
		for _, root := range []string{ssd, nas} {
			p := filepath.Join(root, "Sources", "A-cam", filepath.FromSlash(rel))
			if fileSHA(t, p) != sums[rel] {
				t.Fatalf("%s wrong", p)
			}
		}
	}
	rc, err := receipt.Parse(mustRead(t, res.ReceiptPath))
	if err != nil {
		t.Fatal(err)
	}
	var nasTarget receipt.Target
	for _, tg := range rc.Targets {
		if tg.PeerID == server.Identity.ID {
			nasTarget = tg
		}
	}
	if nasTarget.Transport != "p2p" || nasTarget.ReadbackMode != engine.ReadbackRemote || nasTarget.Status != model.TransferCompleted {
		t.Fatalf("remote target in receipt: %+v", nasTarget)
	}
	// Remote verify, on the remote machine, with its own readback.
	rep, err := remote.VerifyRemote(ctx, server.Identity.ID, rc, nasTarget.TargetID, nas)
	if err != nil || !rep.Clean || rep.Checked != 3 {
		t.Fatalf("remote verify: %+v %v", rep, err)
	}
	p := filepath.Join(nas, "Sources", "A-cam", "AUDIO", "T1.WAV")
	b := mustRead(t, p)
	b[100] ^= 1
	_ = os.WriteFile(p, b, 0o644)
	rep, _ = remote.VerifyRemote(ctx, server.Identity.ID, rc, nasTarget.TargetID, nas)
	if rep.Clean || rep.Modified != 1 {
		t.Fatalf("remote verify missed a bit flip: %+v", rep)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Discovery over real mDNS. Multicast is not available in every sandbox;
// the test says so rather than passing silently.
func TestT3_1_MDNSDiscovery(t *testing.T) {
	a := newNode(t, "studio")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, port, _ := net.SplitHostPort(a.Addrs()[0])
	var p int
	fmt.Sscan(port, &p)
	if err := a.Advertise(ctx, p); err != nil {
		t.Skipf("mDNS advertise unavailable here: %v", err)
	}
	found, err := Browse(ctx, 3*time.Second)
	if err != nil {
		t.Skipf("mDNS browse unavailable here: %v", err)
	}
	for _, d := range found {
		if d.PeerID == a.Identity.ID {
			if len(d.Endpoints) == 0 {
				t.Fatal("discovered without endpoints")
			}
			return
		}
	}
	t.Skipf("multicast did not loop back on this machine (%d other peers seen)", len(found))
}

// T3.2 — unpaired TLS is refused; missing, expired, wrong-peer, wrong-item
// and forged grants get 403; paths outside allowed roots get 403.
func TestT3_2_SecurityBoundaries(t *testing.T) {
	base := t.TempDir()
	card := dirIn(t, base, "CARD")
	secret := dirIn(t, base, "SECRET")
	randFile(t, filepath.Join(card, "clip.mov"), 1000)
	randFile(t, filepath.Join(secret, "passwords.txt"), 100)
	if err := os.Symlink(filepath.Join(secret, "passwords.txt"), filepath.Join(card, "escape.mov")); err != nil {
		t.Fatal(err)
	}
	src := newNode(t, "source", config.Root{Path: card, Access: "ro"})
	dst := newNode(t, "dest")
	other := newNode(t, "other")
	pair(t, dst, src)
	pair(t, other, src)
	ctx := context.Background()

	// Unpaired key: the TLS handshake itself is refused.
	stranger := newNode(t, "stranger")
	conn, err := tls.Dial("tcp", src.Addrs()[0], &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{stranger.Identity.Cert},
		InsecureSkipVerify: true}) //nolint:gosec // test client
	if err == nil {
		_, err = conn.Write([]byte("GET /v1/probe HTTP/1.1\r\nHost: x\r\n\r\n"))
		if err == nil {
			buf := make([]byte, 64)
			_, err = conn.Read(buf)
		}
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("unpaired peer completed a request")
	}

	fi, _ := os.Stat(filepath.Join(card, "clip.mov"))
	item := GrantItem{Root: card, Rel: "clip.mov", Size: fi.Size(), MtimeNS: fi.ModTime().UnixNano()}
	grant, err := src.IssueGrant(GrantRequest{TransferID: "xfr_1", Grantee: dst.Identity.ID, Items: map[string]GrantItem{"1": item}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := dst.Client(ctx, src.Identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	status := func(c *Client, tid, itemID, g string) int {
		resp, err := c.Fetch(ctx, tid, itemID, g, 0)
		var re *RemoteError
		if errors.As(err, &re) {
			return re.Status
		}
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if s := status(c, "xfr_1", "1", grant); s != http.StatusOK {
		t.Fatalf("valid grant: %d", s)
	}
	expired, _ := SignGrant(src.Identity, Grant{TransferID: "xfr_1", Grantee: dst.Identity.ID, ExpiresAt: time.Now().Add(-time.Second), Items: map[string]GrantItem{"1": item}})
	forged, _ := SignGrant(dst.Identity, Grant{TransferID: "xfr_1", Grantee: dst.Identity.ID, ExpiresAt: time.Now().Add(time.Hour), Items: map[string]GrantItem{"1": item}})
	oc, _ := other.Client(ctx, src.Identity.ID)
	for name, got := range map[string]int{
		"missing":        status(c, "xfr_1", "1", ""),
		"expired":        status(c, "xfr_1", "1", expired),
		"forged":         status(c, "xfr_1", "1", forged),
		"wrong item":     status(c, "xfr_1", "2", grant),
		"wrong transfer": status(c, "xfr_2", "1", grant),
		"wrong peer":     status(oc, "xfr_1", "1", grant), // stolen grant presented by another paired peer
	} {
		if got != http.StatusForbidden {
			t.Errorf("%s grant: %d, want 403", name, got)
		}
	}
	// Grants cannot name anything outside allowed roots, or through links.
	for name, it := range map[string]GrantItem{
		"outside root": {Root: secret, Rel: "passwords.txt", Size: 100},
		"traversal":    {Root: card, Rel: "../SECRET/passwords.txt", Size: 100},
		"symlink":      {Root: card, Rel: "escape.mov", Size: 100},
	} {
		var out map[string]string
		err := c.JSON(ctx, http.MethodPost, "/v1/grants", GrantRequest{TransferID: "x", Grantee: dst.Identity.ID, Items: map[string]GrantItem{"1": it}}, &out)
		var re *RemoteError
		if !errors.As(err, &re) || re.Status != http.StatusForbidden {
			t.Errorf("%s: %v", name, err)
		}
	}
	var inv planner.Inventory
	for _, p := range []string{secret, card + "/../SECRET", "/etc"} {
		err := c.JSON(ctx, http.MethodPost, "/v1/inventory", inventoryReq{Path: p}, &inv)
		var re *RemoteError
		if !errors.As(err, &re) || re.Status != http.StatusForbidden {
			t.Errorf("inventory %s: %v", p, err)
		}
	}
	// A read-only root never accepts writes.
	push, err := c.Push(ctx, "xfr_1", "9", card, "dropped.mov", 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = push.Write([]byte("abc"))
	if _, err := push.Finish("x"); err == nil {
		t.Fatal("push into a read-only root accepted")
	}
	if _, err := os.Stat(filepath.Join(card, "dropped.mov")); !os.IsNotExist(err) {
		t.Fatal("file written into a read-only root")
	}
}

// T3.3 — a peer that answers with a redirect never gets the grant
// forwarded anywhere: the client refuses to follow.
func TestT3_3_RedirectRefused(t *testing.T) {
	b := newNode(t, "client")
	evil, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	var leaked atomic.Int32
	sink := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Add(1)
	}), ReadHeaderTimeout: time.Second}
	sinkLn, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { _ = sink.Serve(sinkLn) }()
	defer sink.Close()

	m := &Node{Identity: evil, Name: "evil", DB: b.DB}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+sinkLn.Addr().String()+"/steal", http.StatusFound)
	}), TLSConfig: m.serverTLS(), ReadHeaderTimeout: time.Second}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	defer srv.Close()
	// Mutual pinning, as after a (compromised) pairing.
	if err := b.pin(evil.ID, "evil", evil.Pub, []string{Endpoint{Addr: ln.Addr().String(), Tier: TierLAN}.String()}); err != nil {
		t.Fatal(err)
	}
	if err := m.pin(b.Identity.ID, "client", b.Identity.Pub, nil); err != nil {
		t.Fatal(err)
	}
	c, err := b.Client(context.Background(), evil.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.Radar.SetCandidates([]Endpoint{{Addr: ln.Addr().String(), Tier: TierLAN}})
	c.Radar.Probe = func(context.Context, Endpoint) Measurement { return Measurement{} } // the evil server's probe also redirects
	_, err = c.Fetch(context.Background(), "xfr_1", "1", "SECRET-GRANT", 0)
	if !errors.Is(err, ErrRedirect) {
		t.Fatalf("want ErrRedirect, got %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if leaked.Load() != 0 {
		t.Fatal("the grant left the original peer")
	}
}

func setupPull(t *testing.T, size int) (src, dst *testNode, card, ssd, sum string, item GrantItem) {
	base := t.TempDir()
	card = dirIn(t, base, "CARD")
	ssd = dirIn(t, base, "SSD")
	sum = randFile(t, filepath.Join(card, "BIG.MOV"), size)
	src = newNode(t, "source", config.Root{Path: card, Access: "ro"})
	dst = newNode(t, "dest", config.Root{Path: ssd, Access: "rw"})
	fi, _ := os.Stat(filepath.Join(card, "BIG.MOV"))
	item = GrantItem{Root: card, Rel: "BIG.MOV", Size: fi.Size(), MtimeNS: fi.ModTime().UnixNano()}
	return
}

// T3.4 — the TCP connection dies at 50% of a 64 MiB file; the pull resumes
// from the exact byte offset already on disk and verifies the whole file.
func TestT3_4_ResumeFromExactOffset(t *testing.T) {
	const size = 64 << 20
	src, dst, _, ssd, sum, item := setupPull(t, size)
	px := newProxy(t, src.Addrs()[0])
	src.Endpoints = func() []Endpoint { return []Endpoint{{Addr: px.Addr(), Tier: TierLAN, Badge: "LAN"}} }
	pair(t, dst, src)
	grant, err := src.IssueGrant(GrantRequest{TransferID: "xfr_1", Grantee: dst.Identity.ID, Items: map[string]GrantItem{"1": item}})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := dst.Client(context.Background(), src.Identity.ID)
	if _, err := c.Radar.Select(context.Background()); err != nil {
		t.Fatal(err)
	}
	px.limit.Store(px.down.Load() + size/2) // cut at 50% of the file body
	res := dst.Land(context.Background(), c, "xfr_1", grant, ssd, []LandItem{{ItemID: "1", DestRel: "Shoot/BIG.MOV", Size: size, MtimeNS: item.MtimeNS}}, LandHooks{})
	r := res[0]
	if r.Status != "verified" {
		t.Fatalf("%+v", r)
	}
	if px.cuts.Load() != 1 || r.Resumes != 1 || len(r.ResumeOffsets) != 1 {
		t.Fatalf("expected exactly one cut and one resume: cuts=%d %+v", px.cuts.Load(), r)
	}
	off := r.ResumeOffsets[0]
	if off < size/4 || off >= size {
		t.Fatalf("resumed from offset %d", off)
	}
	t.Logf("resumed at byte %d of %d; relayed %d bytes total", off, size, px.down.Load())
	if px.down.Load() > size+size/8 {
		t.Fatalf("relayed %d bytes for a %d byte file: the resume restarted from zero", px.down.Load(), size)
	}
	if got := fileSHA(t, filepath.Join(ssd, "Shoot", "BIG.MOV")); got != sum || r.DestHash != sum || r.SourceHash != sum {
		t.Fatalf("final SHA-256 %s, want %s", got, sum)
	}
	if matches, _ := filepath.Glob(filepath.Join(ssd, "Shoot", "*.ferry-part")); len(matches) != 0 {
		t.Fatalf("temp left: %v", matches)
	}
}

// T3.5 — Path Radar picks the Tier-1 path; when it drops mid-transfer the
// pull fails over to Tier-4 and completes.
func TestT3_5_PathRadarFailover(t *testing.T) {
	const size = 48 << 20
	src, dst, _, ssd, sum, item := setupPull(t, size)
	fast := newProxy(t, src.Addrs()[0])
	slow := newProxy(t, src.Addrs()[0])
	src.Endpoints = func() []Endpoint {
		return []Endpoint{{Addr: slow.Addr(), Tier: TierTailscale, Badge: "Tailscale Direct"}, {Addr: fast.Addr(), Tier: TierDirect, Badge: "10GbE Direct"}}
	}
	pair(t, dst, src)
	var badges []string
	dst.OnEvent = func(kind string, data any) {
		if m, ok := data.(map[string]any); ok && kind == "peer.changed" && m["transport_badge"] != nil {
			badges = append(badges, m["transport_badge"].(string))
		}
	}
	c, _ := dst.Client(context.Background(), src.Identity.ID)
	ep, err := c.Radar.Select(context.Background())
	if err != nil || ep.Tier != TierDirect {
		t.Fatalf("radar picked %+v (%v); want the tier-1 path", ep, err)
	}
	grant, _ := src.IssueGrant(GrantRequest{TransferID: "xfr_1", Grantee: dst.Identity.ID, Items: map[string]GrantItem{"1": item}})
	fast.dropAll.Store(true)
	fast.limit.Store(fast.down.Load() + size/2)
	res := dst.Land(context.Background(), c, "xfr_1", grant, ssd, []LandItem{{ItemID: "1", DestRel: "BIG.MOV", Size: size, MtimeNS: item.MtimeNS}}, LandHooks{})
	r := res[0]
	if r.Status != "verified" || r.Resumes < 1 {
		t.Fatalf("%+v", r)
	}
	if now, _ := c.Radar.Active(); now.Tier != TierTailscale {
		t.Fatalf("after failover the active path is %+v", now)
	}
	if len(badges) == 0 || badges[len(badges)-1] != "Tailscale Direct" {
		t.Fatalf("live transport badge never moved to the failover path: %v", badges)
	}
	if slow.down.Load() >= size {
		t.Fatalf("tier-4 relayed %d bytes: the failover restarted from zero", slow.down.Load())
	}
	if fileSHA(t, filepath.Join(ssd, "BIG.MOV")) != sum {
		t.Fatal("final SHA-256 mismatch")
	}
}

// Pull modes through the engine: a remote source landing locally, and a
// coordinator telling one peer to pull from another so it carries no bytes.
func TestEnginePullAndRemoteLand(t *testing.T) {
	for _, algo := range []string{checksum.SHA256, checksum.BLAKE3} {
		t.Run(algo, func(t *testing.T) { testEnginePullAndRemoteLand(t, algo) })
	}
}

func testEnginePullAndRemoteLand(t *testing.T, algo string) {
	base := t.TempDir()
	card := dirIn(t, base, "CARD")
	local := dirIn(t, base, "LOCAL")
	nas := dirIn(t, base, "NAS")
	sum := randFile(t, filepath.Join(card, "A.MOV"), 5<<20)
	coord := newNode(t, "coordinator", config.Root{Path: local, Access: "rw"})
	source := newNode(t, "source", config.Root{Path: card, Access: "ro"})
	dest := newNode(t, "dest", config.Root{Path: nas, Access: "rw"})
	pair(t, coord, source)
	pair(t, coord, dest)
	pair(t, dest, source)
	ctx := context.Background()
	remote := Remote{N: coord.Node}
	pl := &planner.Planner{DB: coord.DB, Presets: &preset.Service{DB: coord.DB}, Remote: remote}
	plan, err := pl.Plan(ctx, model.PlanRequest{
		ChecksumAlgo: algo,
		Sources:      []model.SourceSpec{{Location: model.Location{PeerID: source.Identity.ID, Path: card}, Label: "card"}},
		Destinations: []model.DestinationSpec{
			{Location: model.Location{Path: local}},
			{Location: model.Location{PeerID: dest.Identity.ID, Path: nas}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.Approve(ctx, plan.ID, plan.Fingerprint); err != nil {
		t.Fatal(err)
	}
	run := &engine.Runner{DB: coord.DB, Remote: remote}
	id, err := run.Create(ctx, plan.ID, plan.Fingerprint, "agent:test")
	if err != nil {
		t.Fatal(err)
	}
	res, err := run.Run(ctx, id)
	if err != nil || res.Status != model.TransferCompleted {
		items, _ := ledger.GetItems(ctx, coord.DB.Read(), id)
		for _, it := range items {
			t.Logf("%s %s %s", it.FinalPath, it.Status, it.Error)
		}
		t.Fatalf("%+v %v", res, err)
	}
	items, err := ledger.GetItems(ctx, coord.DB.Read(), id)
	if err != nil {
		t.Fatal(err)
	}
	digest, _, err := checksum.Reader(algo, bytes.NewReader(mustRead(t, filepath.Join(card, "A.MOV"))))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected two copied items, got %d", len(items))
	}
	for _, item := range items {
		if item.SourceHash != digest || item.DestHash != digest {
			t.Fatalf("wrong %s digest on %s", algo, item.FinalPath)
		}
	}
	for _, root := range []string{local, nas} {
		if got := fileSHA(t, filepath.Join(root, "Sources", "card", "A.MOV")); got != sum {
			t.Fatalf("%s: %s", root, got)
		}
	}
}

// A daemon bound to named addresses (ferry daemon --listen A --listen B)
// advertises each of them, and Path Radar falls back to the other when one
// stops answering.
func TestNamedListenersAreEachAdvertisedAndFailOver(t *testing.T) {
	a := newNode(t, "a")
	b := newNode(t, "b")
	if err := b.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	eps := b.AdvertisedEndpoints()
	if len(eps) != 2 || eps[0].Addr == eps[1].Addr {
		t.Fatalf("advertised %v, want both bound addresses", eps)
	}
	for _, e := range eps {
		if e.Tier != TierLAN {
			t.Fatalf("loopback endpoint %v: tier %d, want LAN", e.Addr, e.Tier)
		}
	}
	pair(t, a, b)
	ctx := context.Background()
	c, err := a.Client(ctx, b.Identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Info(ctx); err != nil {
		t.Fatal(err)
	}
	active, _ := c.Radar.Active()
	b.mu.Lock()
	for _, ln := range b.lns {
		if ln.Addr().String() == active.Addr {
			_ = ln.Close()
		}
	}
	b.mu.Unlock()
	c.tr.CloseIdleConnections()
	if _, err := c.Info(ctx); err != nil {
		t.Fatalf("after closing %s: %v", active.Addr, err)
	}
	if now, _ := c.Radar.Active(); now.Addr == active.Addr {
		t.Fatalf("still on the closed listener %s", now.Addr)
	}
}
