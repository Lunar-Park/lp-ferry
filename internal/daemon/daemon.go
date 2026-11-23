// Package daemon wires Ferry's services together behind the Unix-socket
// IPC: ledger, presets, planner, runner, P2P node and the event bus. The
// SwiftUI app, the CLI and the MCP bridge all talk to it the same way.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/config"
	"github.com/Lunar-Park/lp-ferry/internal/engine"
	"github.com/Lunar-Park/lp-ferry/internal/identity"
	"github.com/Lunar-Park/lp-ferry/internal/ipc"
	"github.com/Lunar-Park/lp-ferry/internal/ledger"
	"github.com/Lunar-Park/lp-ferry/internal/metadata"
	"github.com/Lunar-Park/lp-ferry/internal/model"
	"github.com/Lunar-Park/lp-ferry/internal/p2p"
	"github.com/Lunar-Park/lp-ferry/internal/planner"
	"github.com/Lunar-Park/lp-ferry/internal/policy"
	"github.com/Lunar-Park/lp-ferry/internal/preset"
	"github.com/Lunar-Park/lp-ferry/internal/receipt"
	"github.com/Lunar-Park/lp-ferry/internal/storage"
	"github.com/Lunar-Park/lp-ferry/internal/verify"
)

// Options configure a daemon.
type Options struct {
	Config   config.Config
	Paths    config.Paths
	P2PAddrs []string // empty: no P2P listener
	MDNS     bool
	Prober   identity.Prober
	Version  string
	Name     string
}

// Daemon is the running engine.
type Daemon struct {
	opts    Options
	DB      *ledger.DB
	Presets *preset.Service
	Planner *planner.Planner
	Runner  *engine.Runner
	Node    *p2p.Node
	Bus     *Bus
	IPC     *ipc.Server

	started time.Time
	mu      sync.Mutex
	running map[string]context.CancelFunc
	last    map[string]engine.Progress
	wg      sync.WaitGroup
	stop    context.CancelFunc
}

// New opens state, recovers interrupted transfers and registers methods.
func New(ctx context.Context, o Options) (*Daemon, error) {
	if o.Prober == nil {
		o.Prober = identity.System()
	}
	if o.Name == "" {
		o.Name, _ = os.Hostname()
	}
	db, err := ledger.Open(o.Paths.DB)
	if err != nil {
		return nil, err
	}
	d := &Daemon{opts: o, DB: db, Bus: &Bus{}, IPC: ipc.NewServer(), started: time.Now(),
		running: map[string]context.CancelFunc{}, last: map[string]engine.Progress{}}
	d.Presets = &preset.Service{DB: db}
	d.Planner = &planner.Planner{DB: db, Presets: d.Presets, Prober: o.Prober}
	d.Runner = &engine.Runner{DB: db, ReceiptsDir: o.Paths.ReceiptsDir,
		OnProgress: d.onProgress, OnState: d.onState}
	if len(o.P2PAddrs) > 0 {
		id, err := p2p.LoadOrCreate(o.Paths.IdentityKey)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		d.Node = &p2p.Node{Identity: id, Name: o.Name, DB: db, Roots: func() []config.Root { return d.opts.Config.Roots },
			OnEvent: func(kind string, data any) { d.Bus.Publish(kind, data) }}
		if err := d.Node.Listen(o.P2PAddrs...); err != nil {
			_ = db.Close()
			return nil, err
		}
		remote := p2p.Remote{N: d.Node}
		d.Planner.Remote, d.Runner.Remote = remote, remote
	}
	if err := d.Presets.EnsureBuiltins(ctx); err != nil {
		d.Close()
		return nil, err
	}
	if _, err := d.Runner.RecoverOnStartup(ctx); err != nil {
		d.Close()
		return nil, err
	}
	d.register()
	return d, nil
}

// Serve listens on the socket and serves until Close.
func (d *Daemon) Serve(ctx context.Context) error {
	if err := d.IPC.Listen(d.opts.Paths.Socket); err != nil {
		return err
	}
	sctx, cancel := context.WithCancel(ctx)
	d.stop = cancel
	if d.Node != nil && d.opts.MDNS {
		if addrs := d.Node.Addrs(); len(addrs) > 0 {
			_, port, _ := net.SplitHostPort(addrs[0])
			p, _ := strconv.Atoi(port)
			_ = d.Node.Advertise(sctx, p)
		}
	}
	go d.watchDrives(sctx, 3*time.Second)
	return d.IPC.Serve()
}

// watchDrives is the hot-plug radar: it diffs the mount list and announces
// drive.mounted (with identity, matched saved destinations and card
// detection) and drive.unmounted. Recognition never starts a transfer.
func (d *Daemon) watchDrives(ctx context.Context, every time.Duration) {
	known := map[string]bool{}
	for _, m := range identity.MountPoints(ctx) {
		known[m] = true
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := map[string]bool{}
		for _, m := range identity.MountPoints(ctx) {
			now[m] = true
		}
		for m := range now {
			if !known[m] {
				d.Bus.Publish("drive.mounted", d.describeDrive(ctx, m))
			}
		}
		for m := range known {
			if !now[m] {
				d.Bus.Publish("drive.unmounted", map[string]string{"mount_path": m})
			}
		}
		known = now
	}
}

func (d *Daemon) describeDrive(ctx context.Context, mp string) Drive {
	res := d.opts.Prober.Identify(ctx, []string{mp})
	info := res.Mounts[mp]
	dr := Drive{MountPath: mp, Identity: info.Identity, Class: info.Class}
	dr.FreeBytes, _ = storage.FreeBytes(mp)
	if saved, err := ledger.ListDestinations(ctx, d.DB.Read(), false); err == nil && info.Identity != nil {
		for _, s := range saved {
			if s.IdentityKind == info.Identity.Kind && s.IdentityValue == info.Identity.Value {
				dr.SavedDestinations = append(dr.SavedDestinations, s.ID)
			}
		}
	}
	dr.Card = metadata.DetectCard(shallow(mp, 4, 4000), nil)
	return dr
}

// Close cancels running transfers (they record canceled and stay
// resumable), stops serving and closes state.
func (d *Daemon) Close() {
	if d.stop != nil {
		d.stop()
	}
	d.mu.Lock()
	for _, c := range d.running {
		c()
	}
	d.mu.Unlock()
	d.wg.Wait()
	_ = d.IPC.Close()
	if d.Node != nil {
		_ = d.Node.Close()
	}
	_ = d.DB.Close()
	_ = os.Remove(d.opts.Paths.Socket)
}

func (d *Daemon) onProgress(p engine.Progress) {
	d.mu.Lock()
	d.last[p.TransferID] = p
	d.mu.Unlock()
	d.Bus.Publish("transfer.progress", p)
}

func (d *Daemon) onState(id, status string) {
	actor := ""
	if t, err := ledger.GetTransfer(context.Background(), d.DB.Read(), id); err == nil {
		actor = t.Actor
	}
	d.Bus.Publish("transfer.state", map[string]string{"transfer_id": id, "status": status, "actor": actor})
}

// mapErr turns domain errors into JSON-RPC codes.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	code := ipc.CodeFailed
	switch {
	case errors.Is(err, ledger.ErrStaleApproval):
		code = ipc.CodeStale
	case errors.Is(err, planner.ErrBlocked):
		code = ipc.CodeBlocked
	case errors.Is(err, ledger.ErrNotFound), errors.Is(err, os.ErrNotExist):
		code = ipc.CodeNotFound
	}
	return &ipc.Error{Code: code, Message: err.Error()}
}

func handle[P any](d *Daemon, method string, fn func(ctx context.Context, c *ipc.Conn, p P) (any, error)) {
	d.IPC.Handle(method, func(ctx context.Context, c *ipc.Conn, raw json.RawMessage) (any, error) {
		var p P
		if err := ipc.Params(raw, &p); err != nil {
			return nil, err
		}
		out, err := fn(ctx, c, p)
		return out, mapErr(err)
	})
}

type none struct{}

func actorOf(a string) (string, error) {
	if a == "" {
		return "human", nil
	}
	if !policy.ValidActor(a) {
		return "", &ipc.Error{Code: ipc.CodeInvalidParams, Message: fmt.Sprintf("actor %q: want \"human\" or \"agent:<name>\"", a)}
	}
	return a, nil
}

func (d *Daemon) register() {
	handle(d, "daemon.status", func(ctx context.Context, _ *ipc.Conn, _ none) (any, error) { return d.status(ctx) })
	handle(d, "doctor.run", func(ctx context.Context, _ *ipc.Conn, _ none) (any, error) { return d.doctor(ctx), nil })
	handle(d, "events.subscribe", d.subscribe)

	handle(d, "drive.list", func(ctx context.Context, _ *ipc.Conn, _ none) (any, error) { return d.drives(ctx) })

	handle(d, "peer.list", d.peerList)
	handle(d, "peer.pair.start", func(_ context.Context, _ *ipc.Conn, p struct {
		TTLSeconds int `json:"ttl_seconds"`
	}) (any, error) {
		if d.Node == nil {
			return nil, errors.New("P2P is disabled on this daemon")
		}
		ttl := time.Duration(p.TTLSeconds) * time.Second
		if ttl <= 0 || ttl > 30*time.Minute {
			ttl = 5 * time.Minute
		}
		code, err := d.Node.StartPairing(ttl)
		return map[string]any{"code": code, "expires_at": time.Now().Add(ttl).UTC(), "peer_id": d.Node.Identity.ID}, err
	})
	handle(d, "peer.pair.complete", func(ctx context.Context, _ *ipc.Conn, p struct {
		Code string `json:"code"`
	}) (any, error) {
		if d.Node == nil {
			return nil, errors.New("P2P is disabled on this daemon")
		}
		return d.Node.Pair(ctx, p.Code)
	})
	handle(d, "peer.probe", d.peerProbe)
	handle(d, "peer.remove", func(ctx context.Context, _ *ipc.Conn, p struct {
		PeerID string `json:"peer_id"`
	}) (any, error) {
		return map[string]bool{"revoked": true}, d.DB.WriteTx(ctx, func(tx *sqlTx) error { return ledger.RevokePeer(tx, p.PeerID) })
	})

	handle(d, "dest.list", d.destList)
	handle(d, "dest.save", func(ctx context.Context, _ *ipc.Conn, p planner.SaveDestinationRequest) (any, error) {
		return d.Planner.SaveDestination(ctx, p)
	})
	handle(d, "dest.resolve", func(ctx context.Context, _ *ipc.Conn, p struct {
		ID string `json:"id"`
	}) (any, error) {
		return d.resolveDest(ctx, p.ID)
	})
	handle(d, "dest.rebind", func(ctx context.Context, _ *ipc.Conn, p struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}) (any, error) {
		cur, err := ledger.GetDestination(ctx, d.DB.Read(), p.ID)
		if err != nil {
			return nil, err
		}
		return d.Planner.SaveDestination(ctx, planner.SaveDestinationRequest{ID: cur.ID, Name: cur.Name, Path: p.Path, PresetID: cur.PinnedPresetID,
			PresetRev: cur.PinnedPresetRev, DefaultConflict: cur.DefaultConflict, ChecksumAlgo: cur.ChecksumAlgo, ReserveBytes: cur.ReserveBytes})
	})
	handle(d, "dest.archive", func(ctx context.Context, _ *ipc.Conn, p struct {
		ID string `json:"id"`
	}) (any, error) {
		cur, err := ledger.GetDestination(ctx, d.DB.Read(), p.ID)
		if err != nil {
			return nil, err
		}
		cur.Archived = true
		return cur, d.DB.WriteTx(ctx, func(tx *sqlTx) error { _, err := ledger.SaveDestination(tx, cur); return err })
	})

	handle(d, "preset.list", func(ctx context.Context, _ *ipc.Conn, _ none) (any, error) { return d.Presets.List(ctx) })
	handle(d, "preset.get", func(ctx context.Context, _ *ipc.Conn, p struct {
		ID  string `json:"id"`
		Rev int    `json:"revision"`
	}) (any, error) {
		return d.Presets.Get(ctx, p.ID, p.Rev)
	})
	handle(d, "preset.save", func(ctx context.Context, _ *ipc.Conn, p struct {
		ID     string         `json:"id,omitempty"`
		Preset *preset.Preset `json:"preset"`
	}) (any, error) {
		if p.Preset == nil {
			return nil, errors.New("preset is required")
		}
		if p.ID == "" {
			return d.Presets.Create(ctx, p.Preset)
		}
		return d.Presets.Update(ctx, p.ID, p.Preset)
	})
	handle(d, "preset.import", func(ctx context.Context, _ *ipc.Conn, p struct {
		JSON json.RawMessage `json:"json"`
	}) (any, error) {
		return d.Presets.Import(ctx, p.JSON)
	})
	handle(d, "preset.export", func(ctx context.Context, _ *ipc.Conn, p struct {
		ID  string `json:"id"`
		Rev int    `json:"revision"`
	}) (any, error) {
		b, err := d.Presets.Export(ctx, p.ID, p.Rev)
		return json.RawMessage(b), err
	})
	handle(d, "preset.preview", func(ctx context.Context, _ *ipc.Conn, p struct {
		ID    string   `json:"id"`
		Rev   int      `json:"revision"`
		Label string   `json:"source_label"`
		Paths []string `json:"paths"`
	}) (any, error) {
		r, err := d.Presets.Get(ctx, p.ID, p.Rev)
		if err != nil {
			return nil, err
		}
		var es []preset.Entry
		for _, rel := range p.Paths {
			es = append(es, preset.Entry{Rel: rel, MtimeNS: time.Now().UnixNano()})
		}
		return preset.Evaluate(r.Preset, p.Label, es), nil
	})
	handle(d, "camera.bind", func(ctx context.Context, _ *ipc.Conn, p struct {
		Make   string `json:"make"`
		Model  string `json:"model"`
		Serial string `json:"serial"`
		Role   string `json:"role"`
	}) (any, error) {
		return map[string]string{"camera_key": metadata.CameraKey(p.Make, p.Model, p.Serial), "role": p.Role},
			d.Planner.BindCamera(ctx, p.Make, p.Model, p.Serial, p.Role)
	})

	handle(d, "plan.create", func(ctx context.Context, _ *ipc.Conn, p model.PlanRequest) (any, error) {
		a, err := actorOf(p.Actor)
		if err != nil {
			return nil, err
		}
		p.Actor = a
		pl, err := d.Planner.Plan(ctx, p)
		if err != nil {
			return nil, err
		}
		return summarize(pl), nil
	})
	handle(d, "plan.get", func(ctx context.Context, _ *ipc.Conn, p struct {
		PlanID string `json:"plan_id"`
	}) (any, error) {
		pl, err := ledger.GetPlan(ctx, d.DB.Read(), p.PlanID)
		if err != nil {
			return nil, err
		}
		return summarize(pl), nil
	})
	handle(d, "plan.tree", func(ctx context.Context, _ *ipc.Conn, q planner.TreeQuery) (any, error) {
		return d.Planner.Tree(ctx, q)
	})
	handle(d, "plan.entries", func(ctx context.Context, _ *ipc.Conn, p struct {
		PlanID string `json:"plan_id"`
		DestID string `json:"destination_id,omitempty"`
		Cursor string `json:"cursor,omitempty"`
		Limit  int    `json:"limit,omitempty"`
	}) (any, error) {
		return d.Planner.Entries(ctx, p.PlanID, p.DestID, p.Cursor, p.Limit)
	})
	handle(d, "plan.resolve", func(ctx context.Context, _ *ipc.Conn, p struct {
		PlanID   string           `json:"plan_id"`
		Revision planner.Revision `json:"revision"`
	}) (any, error) {
		pl, err := d.Planner.Revise(ctx, p.PlanID, p.Revision)
		if err != nil {
			return nil, err
		}
		return summarize(pl), nil
	})
	handle(d, "plan.approve", d.planApprove)

	handle(d, "transfer.start", d.transferStart)
	handle(d, "transfer.list", func(ctx context.Context, _ *ipc.Conn, p struct {
		Limit int `json:"limit,omitempty"`
	}) (any, error) {
		if p.Limit <= 0 || p.Limit > 1000 {
			p.Limit = 100
		}
		return ledger.ListTransfers(ctx, d.DB.Read(), p.Limit)
	})
	handle(d, "transfer.get", func(ctx context.Context, _ *ipc.Conn, p struct {
		TransferID string `json:"transfer_id"`
	}) (any, error) {
		return d.transferGet(ctx, p.TransferID)
	})
	handle(d, "transfer.cancel", func(_ context.Context, _ *ipc.Conn, p struct {
		TransferID string `json:"transfer_id"`
	}) (any, error) {
		d.mu.Lock()
		c, ok := d.running[p.TransferID]
		d.mu.Unlock()
		if !ok {
			return nil, &ipc.Error{Code: ipc.CodeNotFound, Message: "transfer " + p.TransferID + " is not running"}
		}
		c()
		return map[string]bool{"cancel_requested": true}, nil
	})
	handle(d, "transfer.resume", func(_ context.Context, _ *ipc.Conn, p struct {
		TransferID string `json:"transfer_id"`
	}) (any, error) {
		d.launch(p.TransferID, d.Runner.Resume)
		return map[string]string{"transfer_id": p.TransferID, "status": "resuming"}, nil
	})

	handle(d, "receipt.list", func(ctx context.Context, _ *ipc.Conn, p struct {
		TransferID string `json:"transfer_id,omitempty"`
		Limit      int    `json:"limit,omitempty"`
	}) (any, error) {
		if p.Limit <= 0 || p.Limit > 1000 {
			p.Limit = 100
		}
		rs, err := ledger.ListReceipts(ctx, d.DB.Read(), p.TransferID, p.Limit)
		return rs, err
	})
	handle(d, "receipt.get", func(ctx context.Context, _ *ipc.Conn, p receiptRef) (any, error) {
		r, err := d.receiptRow(ctx, p)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(r.BodyJSON), nil
	})
	handle(d, "receipt.export", func(ctx context.Context, _ *ipc.Conn, p receiptRef) (any, error) {
		r, err := d.receiptRow(ctx, p)
		if err != nil {
			return nil, err
		}
		rc, err := receipt.Parse([]byte(r.BodyJSON))
		if err != nil {
			return nil, err
		}
		path, err := receipt.Export(d.opts.Paths.ReceiptsDir, rc)
		if err == nil {
			_ = d.DB.WriteTx(ctx, func(tx *sqlTx) error { return ledger.SetReceiptExport(tx, rc.ReceiptID, path, "") })
		}
		return map[string]string{"receipt_id": rc.ReceiptID, "path": path}, err
	})
	handle(d, "verify.run", d.verifyRun)
}

// PlanSummary is what plan.* methods return: everything review needs
// except the entries themselves (paged via plan.entries / plan.tree).
type PlanSummary struct {
	ID           string                  `json:"plan_id"`
	Fingerprint  string                  `json:"fingerprint"`
	Status       string                  `json:"status"`
	Actor        string                  `json:"actor"`
	ChecksumAlgo string                  `json:"checksum_algo"`
	Destinations []model.PlanDestination `json:"destinations"`
	Findings     []model.Finding         `json:"findings"`
	Blocking     int                     `json:"blocking_count"`
	Counts       map[string]int          `json:"counts"`
	TotalBytes   int64                   `json:"total_bytes"`
	DerivedFrom  string                  `json:"derived_from,omitempty"`
	SupersededBy string                  `json:"superseded_by,omitempty"`
	Invalidated  string                  `json:"invalidated_reason,omitempty"`
}

func summarize(p *model.Plan) PlanSummary {
	s := PlanSummary{ID: p.ID, Fingerprint: p.Fingerprint, Status: p.Status, Actor: p.Actor, ChecksumAlgo: p.ChecksumAlgo,
		Destinations: p.Destinations, Findings: p.Findings, Counts: map[string]int{}, DerivedFrom: p.DerivedFrom,
		SupersededBy: p.SupersededBy, Invalidated: p.Invalidated}
	if s.Findings == nil {
		s.Findings = []model.Finding{}
	}
	s.Blocking = len(p.Blocking())
	for _, e := range p.Entries {
		s.Counts[e.Action]++
		if e.Action == model.ActionNeedsReview {
			s.Blocking++
		}
		if e.Action == model.ActionCopy {
			s.TotalBytes += e.Size
		}
	}
	return s
}

func (d *Daemon) status(ctx context.Context) (any, error) {
	d.mu.Lock()
	active := make([]engine.Progress, 0, len(d.running))
	for id := range d.running {
		if p, ok := d.last[id]; ok {
			active = append(active, p)
		} else {
			active = append(active, engine.Progress{TransferID: id})
		}
	}
	d.mu.Unlock()
	sort.Slice(active, func(i, j int) bool { return active[i].TransferID < active[j].TransferID })
	out := map[string]any{
		"version": d.opts.Version, "protocol": ipc.ProtocolVersion, "name": d.opts.Name,
		"uptime_seconds": int(time.Since(d.started).Seconds()), "socket": d.opts.Paths.Socket,
		"active_transfers": active, "events_dropped": d.Bus.Dropped.Load(),
	}
	awaiting := []string{}
	rows, err := d.DB.Read().QueryContext(ctx, `SELECT id FROM plans WHERE status = 'awaiting_approval' ORDER BY created_at`)
	if err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				awaiting = append(awaiting, id)
			}
		}
		_ = rows.Close()
	}
	out["awaiting_approval"] = awaiting
	if d.Node != nil {
		out["peer_id"] = d.Node.Identity.ID
		out["p2p_addrs"] = d.Node.Addrs()
	}
	return out, nil
}

func (d *Daemon) subscribe(_ context.Context, c *ipc.Conn, p struct {
	Topics []string `json:"topics"`
}) (any, error) {
	ch, cancel := d.Bus.Subscribe(p.Topics)
	c.OnClose(cancel)
	go func() {
		for ev := range ch {
			if c.Push(ev) != nil {
				cancel()
				return
			}
		}
	}()
	return map[string]any{"subscribed": p.Topics}, nil
}

// launch runs fn for a transfer in the background, cancellable via
// transfer.cancel.
func (d *Daemon) launch(id string, fn func(context.Context, string) (*engine.Result, error)) {
	ctx, cancel := context.WithCancel(context.Background())
	d.mu.Lock()
	d.running[id] = cancel
	d.mu.Unlock()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer cancel()
		res, err := fn(ctx, id)
		d.mu.Lock()
		delete(d.running, id)
		d.mu.Unlock()
		if err != nil {
			d.Bus.Publish("transfer.error", map[string]string{"transfer_id": id, "error": err.Error()})
			return
		}
		d.Bus.Publish("transfer.finished", res)
	}()
}

type startParams struct {
	PlanID      string `json:"plan_id"`
	Fingerprint string `json:"fingerprint"`
	Actor       string `json:"actor,omitempty"`
}

// transferStart is the single front door for execution. An agent whose
// request policy does not cover parks the plan in awaiting_approval; a
// human approval later starts it under the agent's name.
func (d *Daemon) transferStart(ctx context.Context, _ *ipc.Conn, p startParams) (any, error) {
	actor, err := actorOf(p.Actor)
	if err != nil {
		return nil, err
	}
	pl, err := ledger.GetPlan(ctx, d.DB.Read(), p.PlanID)
	if err != nil {
		return nil, err
	}
	if pl.Fingerprint != p.Fingerprint {
		return nil, fmt.Errorf("%w: fingerprint does not match plan %s", ledger.ErrStaleApproval, p.PlanID)
	}
	if policy.IsAgent(actor) {
		dec := policy.Evaluate(d.opts.Config.AgentPolicy, actor, pl)
		if dec.NeedsHuman {
			if b := summarize(pl); b.Blocking > 0 {
				return nil, fmt.Errorf("%w: resolve %d blocking item(s) first", planner.ErrBlocked, b.Blocking)
			}
			if pl.Status == model.PlanDraft {
				if err := d.DB.WriteTx(ctx, func(tx *sqlTx) error { return ledger.MarkPlanAwaiting(tx, pl.ID, pl.Fingerprint) }); err != nil {
					return nil, err
				}
			}
			d.Bus.Publish("plan.awaiting_approval", map[string]any{"plan_id": pl.ID, "actor": actor, "reasons": dec.Reasons})
			return map[string]any{"status": model.PlanAwaiting, "plan_id": pl.ID, "reasons": dec.Reasons}, nil
		}
		if pl.Status == model.PlanAwaiting {
			return map[string]any{"status": model.PlanAwaiting, "plan_id": pl.ID}, nil
		}
	}
	return d.approveAndStart(ctx, pl, actor)
}

func (d *Daemon) approveAndStart(ctx context.Context, pl *model.Plan, actor string) (any, error) {
	if pl.Status != model.PlanApproved {
		if err := d.Planner.Approve(ctx, pl.ID, pl.Fingerprint); err != nil {
			return nil, err
		}
	}
	id, err := d.Runner.Create(ctx, pl.ID, pl.Fingerprint, actor)
	if err != nil {
		var rc *ledger.ReservationConflict
		if errors.As(err, &rc) && id != "" {
			return map[string]any{"status": model.TransferNeedsAttention, "transfer_id": id, "reason": rc.Error()}, nil
		}
		return nil, err
	}
	d.launch(id, d.Runner.Run)
	return map[string]any{"status": "started", "transfer_id": id, "actor": actor}, nil
}

// planApprove is a human approval. A plan an agent parked in
// awaiting_approval starts now, attributed to that agent.
func (d *Daemon) planApprove(ctx context.Context, _ *ipc.Conn, p startParams) (any, error) {
	actor, err := actorOf(p.Actor)
	if err != nil {
		return nil, err
	}
	if policy.IsAgent(actor) {
		return nil, fmt.Errorf("%w: plan.approve is a human decision; agents use transfer.start", planner.ErrBlocked)
	}
	pl, err := ledger.GetPlan(ctx, d.DB.Read(), p.PlanID)
	if err != nil {
		return nil, err
	}
	if pl.Fingerprint != p.Fingerprint {
		return nil, fmt.Errorf("%w: fingerprint does not match plan %s", ledger.ErrStaleApproval, p.PlanID)
	}
	if pl.Status == model.PlanAwaiting {
		return d.approveAndStart(ctx, pl, pl.Actor)
	}
	if err := d.Planner.Approve(ctx, pl.ID, pl.Fingerprint); err != nil {
		return nil, err
	}
	return map[string]any{"status": model.PlanApproved, "plan_id": pl.ID}, nil
}

func (d *Daemon) transferGet(ctx context.Context, id string) (any, error) {
	t, err := ledger.GetTransfer(ctx, d.DB.Read(), id)
	if err != nil {
		return nil, err
	}
	targets, err := ledger.GetTargets(ctx, d.DB.Read(), id)
	if err != nil {
		return nil, err
	}
	items, err := ledger.GetItems(ctx, d.DB.Read(), id)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	var failures []map[string]string
	for _, it := range items {
		counts[it.Status]++
		if it.Status == model.ItemFailed && len(failures) < 50 {
			failures = append(failures, map[string]string{"dest_rel": it.Entry.DestRelPath, "target_id": it.TargetID, "error": it.Error})
		}
	}
	d.mu.Lock()
	prog, live := d.last[id]
	_, running := d.running[id]
	d.mu.Unlock()
	out := map[string]any{"transfer": t, "targets": targets, "item_counts": counts, "failures": failures, "running": running}
	if live {
		out["progress"] = prog
	}
	if r, err := ledger.LatestReceipt(ctx, d.DB.Read(), id); err == nil {
		out["receipt_id"], out["receipt_path"] = r.ID, r.ExportedPath
	}
	return out, nil
}

type receiptRef struct {
	ReceiptID  string `json:"receipt_id,omitempty"`
	TransferID string `json:"transfer_id,omitempty"`
}

func (d *Daemon) receiptRow(ctx context.Context, p receiptRef) (*ledger.Receipt, error) {
	if p.ReceiptID != "" {
		return ledger.GetReceipt(ctx, d.DB.Read(), p.ReceiptID)
	}
	if p.TransferID != "" {
		return ledger.LatestReceipt(ctx, d.DB.Read(), p.TransferID)
	}
	return nil, &ipc.Error{Code: ipc.CodeInvalidParams, Message: "receipt_id or transfer_id is required"}
}

func (d *Daemon) verifyRun(ctx context.Context, _ *ipc.Conn, p struct {
	ReceiptID  string `json:"receipt_id,omitempty"`
	TransferID string `json:"transfer_id,omitempty"`
	TargetID   string `json:"target_id,omitempty"`
}) (any, error) {
	row, err := d.receiptRow(ctx, receiptRef{ReceiptID: p.ReceiptID, TransferID: p.TransferID})
	if err != nil {
		return nil, err
	}
	rc, err := receipt.Parse([]byte(row.BodyJSON))
	if err != nil {
		return nil, err
	}
	combined := &verify.Report{ReceiptID: rc.ReceiptID, TransferID: rc.TransferID, ChecksumAlgo: rc.ChecksumAlgo, ReadbackMode: storage.ReadbackMode}
	for _, tg := range rc.Targets {
		if p.TargetID != "" && tg.TargetID != p.TargetID {
			continue
		}
		var rep *verify.Report
		if tg.PeerID == "" || tg.PeerID == model.LocalPeer {
			rep, err = verify.Verify(ctx, rc, verify.Options{TargetID: tg.TargetID})
		} else if d.Node != nil {
			rep, err = p2p.Remote{N: d.Node}.VerifyRemote(ctx, tg.PeerID, rc, tg.TargetID, tg.RootPath)
		} else {
			err = errors.New("remote target but P2P is disabled")
		}
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", tg.Label, err)
		}
		if err := verify.Record(ctx, d.DB, rep); err != nil {
			return nil, err
		}
		combined.Targets = append(combined.Targets, rep.Targets...)
		combined.Checked += rep.Checked
		combined.Missing += rep.Missing
		combined.Modified += rep.Modified
		combined.Added += rep.Added
		combined.Unreadable += rep.Unreadable
	}
	switch {
	case combined.Unreadable > 0:
		combined.ExitCode = verify.ExitUnreadable
	case combined.Missing > 0:
		combined.ExitCode = verify.ExitMissing
	case combined.Modified > 0:
		combined.ExitCode = verify.ExitModified
	case combined.Added > 0:
		combined.ExitCode = verify.ExitAdded
	}
	combined.Clean = combined.ExitCode == 0
	d.Bus.Publish("verify.finished", map[string]any{"receipt_id": rc.ReceiptID, "clean": combined.Clean, "exit_code": combined.ExitCode})
	return combined, nil
}

func (d *Daemon) peerList(ctx context.Context, _ *ipc.Conn, _ none) (any, error) {
	peers, err := ledger.ListPeers(ctx, d.DB.Read())
	if err != nil {
		return nil, err
	}
	type peerView struct {
		ledger.Peer
		ActivePath *p2p.Endpoint `json:"active_path,omitempty"`
	}
	out := make([]peerView, 0, len(peers))
	for _, p := range peers {
		v := peerView{Peer: p}
		if d.Node != nil && !p.Revoked {
			if c, err := d.Node.Client(ctx, p.ID); err == nil {
				if ep, ok := c.Radar.Active(); ok {
					v.ActivePath = &ep
				}
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (d *Daemon) peerProbe(ctx context.Context, _ *ipc.Conn, p struct {
	PeerID string `json:"peer_id"`
}) (any, error) {
	if d.Node == nil {
		return nil, errors.New("P2P is disabled on this daemon")
	}
	c, err := d.Node.Client(ctx, p.PeerID)
	if err != nil {
		return nil, err
	}
	ep, err := c.Radar.Select(ctx)
	out := map[string]any{"peer_id": p.PeerID, "measurements": c.Radar.Last()}
	if err != nil {
		out["error"] = err.Error()
		return out, nil
	}
	out["active_path"] = ep
	if info, err := c.Info(ctx); err == nil {
		out["info"] = info
	}
	d.Bus.Publish("peer.changed", map[string]any{"peer_id": p.PeerID, "transport_badge": ep.Badge, "addr": ep.Addr})
	return out, nil
}

type destView struct {
	model.SavedDestination
	Resolution identity.Resolution `json:"resolution"`
}

func (d *Daemon) resolveDest(ctx context.Context, id string) (*destView, error) {
	s, err := ledger.GetDestination(ctx, d.DB.Read(), id)
	if err != nil {
		return nil, err
	}
	if s.PeerID != "" && s.PeerID != model.LocalPeer {
		return &destView{SavedDestination: *s, Resolution: identity.Resolution{State: "remote", Reason: "on peer " + s.PeerID}}, nil
	}
	obs, _ := identity.Observe(ctx, d.opts.Prober)
	res := identity.Resolve(identity.Saved{Identity: identity.VolumeIdentity{Kind: s.IdentityKind, Value: s.IdentityValue,
		Confidence: s.IdentityConfidence}, SubPath: s.RelSubpath, LastBinding: s.RootPath}, obs)
	return &destView{SavedDestination: *s, Resolution: res}, nil
}

func (d *Daemon) destList(ctx context.Context, _ *ipc.Conn, p struct {
	IncludeArchived bool `json:"include_archived,omitempty"`
}) (any, error) {
	ds, err := ledger.ListDestinations(ctx, d.DB.Read(), p.IncludeArchived)
	if err != nil {
		return nil, err
	}
	out := make([]*destView, 0, len(ds))
	for _, s := range ds {
		v, err := d.resolveDest(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Drive is one mounted volume as drive.list reports it.
type Drive struct {
	MountPath         string                   `json:"mount_path"`
	Identity          *identity.VolumeIdentity `json:"identity,omitempty"`
	Class             identity.StorageClass    `json:"class"`
	Writable          bool                     `json:"writable"`
	FreeBytes         int64                    `json:"free_bytes"`
	SavedDestinations []string                 `json:"saved_destinations,omitempty"`
	Card              *metadata.CardHint       `json:"card,omitempty"`
}

func (d *Daemon) drives(ctx context.Context) (any, error) {
	obs, res := identity.Observe(ctx, d.opts.Prober)
	saved, _ := ledger.ListDestinations(ctx, d.DB.Read(), false)
	out := []Drive{}
	for _, o := range obs {
		dr := Drive{MountPath: o.MountPath, Identity: o.Identity, Writable: o.Writable, Class: res.Mounts[o.MountPath].Class}
		dr.FreeBytes, _ = storage.FreeBytes(o.MountPath)
		for _, s := range saved {
			if o.Identity != nil && s.IdentityValue == o.Identity.Value && s.IdentityKind == o.Identity.Kind {
				dr.SavedDestinations = append(dr.SavedDestinations, s.ID)
			}
		}
		if o.MountPath != "/" && o.MountPath != "/System/Volumes/Data" {
			dr.Card = metadata.DetectCard(shallow(o.MountPath, 4, 4000), nil)
		}
		out = append(out, dr)
	}
	return map[string]any{"drives": out, "warnings": res.Warnings, "degraded": res.Degraded}, nil
}

// shallow lists relative paths up to depth levels and max entries.
func shallow(root string, depth, maxEntries int) []string {
	var out []string
	var walk func(dir, rel string, level int)
	walk = func(dir, rel string, level int) {
		if level > depth || len(out) >= maxEntries {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if len(out) >= maxEntries {
				return
			}
			r := e.Name()
			if rel != "" {
				r = rel + "/" + e.Name()
			}
			out = append(out, r)
			if e.IsDir() && !planner.SystemArtifact(e.Name()) {
				walk(filepath.Join(dir, e.Name()), r, level+1)
			}
		}
	}
	walk(root, "", 1)
	return out
}

// Check is one doctor finding.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func (d *Daemon) doctor(ctx context.Context) []Check {
	var out []Check
	add := func(name string, ok bool, detail string, args ...any) {
		out = append(out, Check{Name: name, OK: ok, Detail: fmt.Sprintf(detail, args...)})
	}
	add("runtime", true, "%s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	var mode string
	err := d.DB.Read().QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode)
	add("database", err == nil && mode == "wal", "%s (journal_mode=%s)", d.opts.Paths.DB, mode)
	if fi, err := os.Stat(d.opts.Paths.Socket); err == nil {
		add("socket", fi.Mode().Perm() == 0o600, "%s mode %v", d.opts.Paths.Socket, fi.Mode().Perm())
	} else {
		add("socket", false, "%v", err)
	}
	if d.Node != nil {
		fi, err := os.Stat(d.opts.Paths.IdentityKey)
		add("identity", err == nil && fi.Mode().Perm() == 0o600, "%s peer %s", d.opts.Paths.IdentityKey, d.Node.Identity.ID)
		add("p2p", true, "listening on %v", d.Node.Addrs())
	} else {
		add("p2p", true, "disabled")
	}
	add("readback", true, "physical readback mode %s", storage.ReadbackMode)
	if len(d.opts.Config.Roots) == 0 {
		add("roots", true, "no [[roots]] configured: nothing is exported to peers or agents")
	}
	for _, r := range d.opts.Config.Roots {
		root, err := storage.OpenRoot(r.Path)
		if err != nil {
			add("root "+r.Path, false, "%v", err)
			continue
		}
		free, _ := storage.FreeBytes(root.Real)
		if r.Access != config.AccessReadWrite {
			add("root "+r.Path, true, "read-only, %d bytes free", free)
			continue
		}
		dir, err := storage.OpenDir(root, nil, false)
		if err != nil {
			add("root "+r.Path, false, "%v", err)
			continue
		}
		s, err := storage.ProbeStrategy(dir)
		_ = dir.Close()
		add("root "+r.Path, err == nil, "rw, publish %s, %d bytes free%s", s, free, errSuffix(err))
	}
	return out
}

func errSuffix(err error) string {
	if err == nil {
		return ""
	}
	return ": " + err.Error()
}
