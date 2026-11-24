package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/config"
	"github.com/Lunar-Park/lp-ferry/internal/ipc"
	"github.com/Lunar-Park/lp-ferry/internal/mcp"
)

type env struct {
	t      *testing.T
	d      *Daemon
	socket string
	base   string
}

func start(t *testing.T, cfg config.Config) *env {
	t.Helper()
	base := t.TempDir()
	// Unix socket paths are limited to ~104 bytes; test temp dirs are long.
	sockDir, err := os.MkdirTemp("/tmp", "fy")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	paths := config.Paths{Home: base, Socket: filepath.Join(sockDir, "ferry.sock"), DB: filepath.Join(base, "ferry.db"),
		ReceiptsDir: filepath.Join(base, "receipts"), IdentityKey: filepath.Join(base, "identity.key")}
	ctx := context.Background()
	d, err := New(ctx, Options{Config: cfg, Paths: paths, Version: "test", P2PAddrs: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = d.Serve(ctx) }()
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(paths.Socket); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(d.Close)
	return &env{t: t, d: d, socket: paths.Socket, base: base}
}

func (e *env) client() *ipc.Client {
	e.t.Helper()
	c, err := ipc.Dial(context.Background(), e.socket)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = c.Close() })
	return c
}

func (e *env) dir(name string) string {
	p := filepath.Join(e.base, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func writeRandom(t *testing.T, p string, n int) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	b := make([]byte, n)
	_, _ = rand.Read(b)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// mcpSession runs an MCP server over pipes, as an agent client would.
type mcpSession struct {
	t   *testing.T
	in  *io.PipeWriter
	out *bufio.Scanner
	mu  sync.Mutex
	id  int
}

func newMCP(t *testing.T, c *ipc.Client, clientName string) *mcpSession {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := &mcp.Server{Daemon: c, Version: "test"}
	go func() { _ = srv.Serve(context.Background(), inR, outW); _ = outW.Close() }()
	t.Cleanup(func() { _ = inW.Close() })
	s := &mcpSession{t: t, in: inW, out: bufio.NewScanner(outR)}
	s.out.Buffer(make([]byte, 64<<10), 16<<20)
	s.rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]string{"name": clientName}, "capabilities": map[string]any{}})
	_, _ = fmt.Fprintln(s.in, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return s
}

func (s *mcpSession) rpc(method string, params any) json.RawMessage {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": s.id, "method": method, "params": params})
	if _, err := s.in.Write(append(b, '\n')); err != nil {
		s.t.Fatal(err)
	}
	if !s.out.Scan() {
		s.t.Fatal("mcp server closed")
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *ipc.Error      `json:"error"`
	}
	if err := json.Unmarshal(s.out.Bytes(), &resp); err != nil {
		s.t.Fatal(err)
	}
	if resp.Error != nil {
		s.t.Fatalf("%s: %v", method, resp.Error)
	}
	return resp.Result
}

// tool calls a tool and decodes its structured result; isError fails.
func (s *mcpSession) tool(name string, args any, out any) {
	s.t.Helper()
	raw := s.rpc("tools/call", map[string]any{"name": name, "arguments": args})
	var r struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	_ = json.Unmarshal(raw, &r)
	if r.IsError {
		s.t.Fatalf("%s failed: %s", name, r.Content[0].Text)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(r.Content[0].Text), out); err != nil {
			s.t.Fatalf("%s: %v in %s", name, err, r.Content[0].Text)
		}
	}
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// T4.1 — the socket is 0600 in a 0700 directory; CLI-style clients, an
// MCP session and event subscribers all run concurrently under -race.
func TestT4_1_SocketAndConcurrentClients(t *testing.T) {
	e := start(t, config.Default())
	fi, err := os.Stat(e.socket)
	if err != nil || fi.Mode().Perm() != 0o600 || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket mode %v %v", fi.Mode(), err)
	}
	if di, _ := os.Stat(filepath.Dir(e.socket)); di.Mode().Perm() != 0o700 {
		t.Fatalf("socket dir mode %v", di.Mode().Perm())
	}
	// A second daemon on the same socket is refused, not allowed to steal it.
	if err := ipc.NewServer().Listen(e.socket); err == nil {
		t.Fatal("a second listener replaced a live socket")
	}

	card := e.dir("CARD")
	for i := 0; i < 20; i++ {
		writeRandom(t, filepath.Join(card, fmt.Sprintf("C%03d.MOV", i)), 256<<10)
	}
	ctx := context.Background()

	// Subscribers first.
	var subs []*ipc.Client
	for i := 0; i < 3; i++ {
		c := e.client()
		if err := c.Call(ctx, "events.subscribe", map[string]any{"topics": []string{"transfer.*"}}, nil); err != nil {
			t.Fatal(err)
		}
		subs = append(subs, c)
	}
	sess := newMCP(t, e.client(), "concurrency")
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			c, err := ipc.Dial(ctx, e.socket)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			for i := 0; i < 5; i++ {
				var st map[string]any
				if err := c.Call(ctx, "daemon.status", nil, &st); err != nil {
					errs <- err
					return
				}
				var pl PlanSummary
				if err := c.Call(ctx, "plan.create", map[string]any{
					"sources":      []map[string]any{{"path": card, "label": "card"}},
					"destinations": []map[string]any{{"path": e.dir(fmt.Sprintf("D%d_%d", w, i))}},
				}, &pl); err != nil {
					errs <- err
					return
				}
				if w == 0 && i == 0 {
					var started map[string]any
					if err := c.Call(ctx, "transfer.start", map[string]any{"plan_id": pl.ID, "fingerprint": pl.Fingerprint}, &started); err != nil {
						errs <- err
						return
					}
				}
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			sess.rpc("tools/list", nil)
			var st map[string]any
			sess.tool("ferry_status", map[string]any{}, &st)
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for i, s := range subs {
		got := false
		timeout := time.After(20 * time.Second)
		for !got {
			select {
			case ev := <-s.Events():
				if ev.Topic == "transfer.state" {
					if m, _ := ev.Data.(map[string]any); m["status"] == "completed" {
						got = true
					}
				}
			case <-timeout:
				t.Fatalf("subscriber %d never saw the transfer complete", i)
			}
		}
	}
}

// T4.2 — an agent drives the whole flow through MCP; a socket subscriber
// sees live transfer.progress attributed to agent:test with an honest ETA.
func TestT4_2_MCPEndToEnd(t *testing.T) {
	cfg := config.Default()
	cfg.AgentPolicy.AllowUnsavedDestinations = true
	e := start(t, cfg)
	ctx := context.Background()
	card := e.dir("CARD")
	writeRandom(t, filepath.Join(card, "CLIP", "A001.MOV"), 96<<20)
	writeRandom(t, filepath.Join(card, "CLIP", "A002.MOV"), 1<<20)
	ssd := e.dir("SSD")

	watcher := e.client()
	if err := watcher.Call(ctx, "events.subscribe", map[string]any{"topics": []string{"transfer.progress", "transfer.state"}}, nil); err != nil {
		t.Fatal(err)
	}
	var progress []map[string]any
	var pmu sync.Mutex
	go func() {
		for ev := range watcher.Events() {
			if ev.Topic == "transfer.progress" {
				if m, ok := ev.Data.(map[string]any); ok {
					pmu.Lock()
					progress = append(progress, m)
					pmu.Unlock()
				}
			}
		}
	}()

	s := newMCP(t, e.client(), "test")
	var tools struct {
		Tools []mcp.Tool `json:"tools"`
	}
	_ = json.Unmarshal(s.rpc("tools/list", nil), &tools)
	if len(tools.Tools) != 12 {
		t.Fatalf("%d tools", len(tools.Tools))
	}
	var drives, peers, presets any
	s.tool("ferry_drives", map[string]any{}, &drives)
	s.tool("ferry_peers", map[string]any{}, &peers)
	s.tool("ferry_presets", map[string]any{}, &presets)
	var plan PlanSummary
	s.tool("ferry_plan", map[string]any{
		"sources":      []map[string]any{{"path": card, "label": "A-cam"}},
		"destinations": []map[string]any{{"path": ssd}},
	}, &plan)
	if plan.Actor != "agent:test" || plan.Blocking != 0 {
		t.Fatalf("%+v", plan)
	}
	var tree map[string]any
	s.tool("ferry_plan_tree", map[string]any{"plan_id": plan.ID, "destination_id": plan.Destinations[0].ID, "depth": 4}, &tree)
	if tree["file_count"].(float64) != 2 {
		t.Fatalf("tree %+v", tree)
	}
	var started map[string]any
	s.tool("ferry_transfer_start", map[string]any{"plan_id": plan.ID, "fingerprint": plan.Fingerprint}, &started)
	if started["status"] != "started" || started["actor"] != "agent:test" {
		t.Fatalf("%+v", started)
	}
	tid := started["transfer_id"].(string)
	var status map[string]any
	waitFor(t, "transfer completion", func() bool {
		s.tool("ferry_transfer_status", map[string]any{"transfer_id": tid}, &status)
		tr := status["transfer"].(map[string]any)
		return tr["status"] == "completed"
	})
	var rc map[string]any
	s.tool("ferry_receipt", map[string]any{"transfer_id": tid}, &rc)
	if rc["actor"] != "agent:test" || rc["status"] != "completed" {
		t.Fatalf("receipt %+v", rc["actor"])
	}
	var rep map[string]any
	s.tool("ferry_verify", map[string]any{"transfer_id": tid}, &rep)
	if rep["clean"] != true || rep["checked"].(float64) != 2 {
		t.Fatalf("verify %+v", rep)
	}
	pmu.Lock()
	defer pmu.Unlock()
	if len(progress) < 2 {
		t.Fatalf("only %d progress events streamed", len(progress))
	}
	sawPartial := false
	for _, p := range progress {
		if p["actor"] != "agent:test" || p["transfer_id"] != tid {
			t.Fatalf("progress not attributed: %+v", p)
		}
		if _, ok := p["eta_seconds"]; !ok {
			t.Fatal("progress without eta_seconds")
		}
		if f := p["overall_fraction"].(float64); f > 0 && f < 1 {
			sawPartial = true
			if eta := p["eta_seconds"].(float64); eta == 0 {
				t.Fatalf("ETA 0 while %.0f%% done", f*100)
			}
		}
	}
	last := progress[len(progress)-1]
	if last["overall_fraction"].(float64) != 1 || last["eta_seconds"].(float64) != 0 {
		t.Fatalf("final progress %+v", last)
	}
	t.Logf("%d progress events; mid-transfer event seen: %v", len(progress), sawPartial)
}

// T4.3 — an agent asking for an unsaved destination, or more than the
// auto-approve limit, waits in awaiting_approval until a human approves;
// and the agent's tool surface cannot delete, move or overwrite.
func TestT4_3_AgentPolicy(t *testing.T) {
	cfg := config.Default()
	cfg.AgentPolicy.AutoApproveSavedDestinations = true
	cfg.AgentPolicy.AllowUnsavedDestinations = false
	cfg.AgentPolicy.AutoApproveMaxBytes = 1 << 20
	e := start(t, cfg)
	ctx := context.Background()
	card := e.dir("CARD")
	writeRandom(t, filepath.Join(card, "big.mov"), 2<<20)
	human := e.client()
	s := newMCP(t, e.client(), "test")

	var plan PlanSummary
	s.tool("ferry_plan", map[string]any{"sources": []map[string]any{{"path": card}}, "destinations": []map[string]any{{"path": e.dir("SSD")}}}, &plan)
	var res map[string]any
	s.tool("ferry_transfer_start", map[string]any{"plan_id": plan.ID, "fingerprint": plan.Fingerprint}, &res)
	if res["status"] != "awaiting_approval" {
		t.Fatalf("%+v", res)
	}
	reasons := fmt.Sprint(res["reasons"])
	if !strings.Contains(reasons, "not a saved destination") || !strings.Contains(reasons, "auto-approve limit") {
		t.Fatalf("reasons: %s", reasons)
	}
	var list []map[string]any
	_ = human.Call(ctx, "transfer.list", nil, &list)
	if len(list) != 0 {
		t.Fatalf("a transfer started without approval: %+v", list)
	}
	var st map[string]any
	_ = human.Call(ctx, "daemon.status", nil, &st)
	if fmt.Sprint(st["awaiting_approval"]) != "["+plan.ID+"]" {
		t.Fatalf("status does not show the waiting plan: %v", st["awaiting_approval"])
	}
	// Asking again does not sneak it through.
	s.tool("ferry_transfer_start", map[string]any{"plan_id": plan.ID, "fingerprint": plan.Fingerprint}, &res)
	if res["status"] != "awaiting_approval" {
		t.Fatalf("second request: %+v", res)
	}
	// An agent cannot approve its own request.
	err := human.Call(ctx, "plan.approve", map[string]any{"plan_id": plan.ID, "fingerprint": plan.Fingerprint, "actor": "agent:test"}, nil)
	if err == nil {
		t.Fatal("agent approved its own plan")
	}
	// The human approves; the transfer starts, still attributed to the agent.
	var approved map[string]any
	if err := human.Call(ctx, "plan.approve", map[string]any{"plan_id": plan.ID, "fingerprint": plan.Fingerprint}, &approved); err != nil {
		t.Fatal(err)
	}
	if approved["status"] != "started" || approved["actor"] != "agent:test" {
		t.Fatalf("%+v", approved)
	}
	tid := approved["transfer_id"].(string)
	waitFor(t, "approved transfer", func() bool {
		var g map[string]any
		_ = human.Call(ctx, "transfer.get", map[string]any{"transfer_id": tid}, &g)
		return g["transfer"].(map[string]any)["status"] == "completed"
	})

	// No tool can delete, move or overwrite; every tool declares itself
	// non-destructive.
	var tools struct {
		Tools []mcp.Tool `json:"tools"`
	}
	_ = json.Unmarshal(s.rpc("tools/list", nil), &tools)
	want := map[string]bool{"ferry_status": true, "ferry_drives": true, "ferry_peers": true, "ferry_destinations": true,
		"ferry_presets": true, "ferry_plan": true, "ferry_plan_tree": true, "ferry_transfer_start": true,
		"ferry_transfer_status": true, "ferry_transfer_cancel": true, "ferry_receipt": true, "ferry_verify": true}
	for _, tl := range tools.Tools {
		if !want[tl.Name] {
			t.Errorf("unexpected tool %s", tl.Name)
		}
		for _, bad := range []string{"delete", "remove", "move", "overwrite", "rename", "replace"} {
			if strings.Contains(tl.Name, bad) {
				t.Errorf("destructive tool %s", tl.Name)
			}
		}
		if tl.Annotations["destructiveHint"] != false {
			t.Errorf("%s does not declare destructiveHint=false", tl.Name)
		}
		delete(want, tl.Name)
	}
	if len(want) != 0 {
		t.Errorf("missing tools %v", want)
	}
	// Invalid actors are refused at the socket.
	if err := human.Call(ctx, "plan.create", map[string]any{"sources": []map[string]any{{"path": card}},
		"destinations": []map[string]any{{"path": e.dir("X")}}, "actor": "root"}, nil); err == nil {
		t.Fatal("bogus actor accepted")
	}
}
