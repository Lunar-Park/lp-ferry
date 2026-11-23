package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Lunar-Park/lp-ferry/internal/checksum"
	"github.com/Lunar-Park/lp-ferry/internal/identity"
	"github.com/Lunar-Park/lp-ferry/internal/ledger"
	"github.com/Lunar-Park/lp-ferry/internal/model"
	"github.com/Lunar-Park/lp-ferry/internal/receipt"
	"github.com/Lunar-Park/lp-ferry/internal/storage"
)

// Named points in the item and transfer lifecycle. Hooks use them to
// inject crashes and faults at the exact windows recovery must handle.
const (
	PointSourceOpened  = "source_opened"
	PointPartWritten   = "after_part_written"    // crash window 1: temp written, unpublished
	PointBeforePublish = "before_publish"        // last moment before publication
	PointPublished     = "after_publish"         // crash window 2: published, not committed
	PointBeforeExport  = "before_receipt_export" // crash window 3: committed, receipt not exported
)

// ErrSimulatedCrash is returned when a Hooks.Crash point fires. Nothing is
// written to disk or ledger after it, exactly like a killed process.
var ErrSimulatedCrash = errors.New("simulated crash")

// ErrNotResumable is returned by Resume for a transfer in a state that
// cannot be resumed.
var ErrNotResumable = errors.New("transfer is not resumable")

// Hooks are test seams for failure injection. All are optional.
type Hooks struct {
	// Crash returning true at a point simulates process death there.
	Crash func(point string, it model.TransferItem) bool
	// At runs a side effect at a point (e.g. swap a mount mid-transfer).
	At func(point string, it model.TransferItem)
	// WrapSink wraps one target's writer for one item (fault injection).
	WrapSink func(target int, it model.TransferItem, w io.Writer) io.Writer
}

// Runner executes approved plans durably.
type Runner struct {
	DB          *ledger.DB
	ReceiptsDir string
	OnProgress  func(Progress)
	OnState     func(transferID, status string)
	// ClassOf reports a path's storage class for scheduling; nil = unknown.
	ClassOf func(path string) identity.StorageClass
	Hooks   Hooks
}

// Result summarizes a finished run.
type Result struct {
	TransferID  string
	Status      string
	ReceiptID   string
	ReceiptPath string
	ExportError string
	Findings    []string
}

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// DestKey is the reservation namespace for a destination root: the machine
// plus the root's resolved path, so two spellings of one directory share it.
func DestKey(peerID, realRoot string) string { return peerID + ":" + realRoot }

// Create starts a transfer for an approved plan. The fingerprint must be
// the one the plan was approved under; anything else is a stale approval.
// Every destination path is reserved; a path another live transfer holds
// leaves this transfer needs_attention naming the holder, and returns a
// *ledger.ReservationConflict alongside the new transfer ID.
func (r *Runner) Create(ctx context.Context, planID, fingerprint, actor string) (string, error) {
	plan, err := ledger.GetPlan(ctx, r.DB.Read(), planID)
	if err != nil {
		return "", err
	}
	if plan.Status != model.PlanApproved || plan.Fingerprint != fingerprint {
		return "", fmt.Errorf("%w: plan %s is %s with fingerprint %s", ledger.ErrStaleApproval, planID, plan.Status, plan.Fingerprint)
	}
	if !checksum.Valid(plan.ChecksumAlgo) {
		return "", fmt.Errorf("plan %s: unsupported checksum algorithm %q", planID, plan.ChecksumAlgo)
	}
	for _, e := range plan.Entries {
		switch e.Action {
		case model.ActionCopy, model.ActionDir, model.ActionExclude:
		default:
			return "", fmt.Errorf("plan %s entry %d: action %q cannot execute (resolve it and re-approve)", planID, e.ID, e.Action)
		}
	}
	t := &model.Transfer{ID: newID("xfr_"), PlanID: planID, Status: model.TransferQueued, Actor: actor, ChecksumAlgo: plan.ChecksumAlgo, StartedAt: ledger.Now()}
	var targets []model.TransferTarget
	byDest := map[string]string{}
	var keys [][2]string
	realRoot := map[string]string{}
	for _, d := range plan.Destinations {
		if d.PeerID != model.LocalPeer && d.PeerID != "" {
			return "", fmt.Errorf("destination %s is on peer %s; remote targets arrive with the P2P transport", d.ID, d.PeerID)
		}
		root, err := storage.OpenRoot(d.RootPath)
		if err != nil {
			return "", fmt.Errorf("destination %s: %w", d.Label, err)
		}
		strategy := storage.Strategy(d.PublishStrategy)
		if !strategy.Valid() {
			dir, err := storage.OpenDir(root, nil, false)
			if err != nil {
				return "", err
			}
			strategy, err = storage.ProbeStrategy(dir)
			_ = dir.Close()
			if err != nil {
				return "", fmt.Errorf("destination %s: %w", d.Label, err)
			}
		}
		g := model.TransferTarget{
			ID: newID("tgt_"), TransferID: t.ID, PlanDestID: d.ID, Label: d.Label, PeerID: model.LocalPeer,
			RootPath: d.RootPath, RootDev: root.Dev, RootIno: root.Ino, TransportKind: "local",
			PublishStrategy: string(strategy), ReadbackMode: storage.ReadbackMode, Status: model.TransferQueued,
		}
		targets = append(targets, g)
		byDest[d.ID] = g.ID
		realRoot[d.ID] = root.Real
	}
	var items []model.TransferItem
	for _, e := range plan.Entries {
		tid, ok := byDest[e.PlanDestID]
		if !ok {
			return "", fmt.Errorf("plan entry %d names unknown destination %s", e.ID, e.PlanDestID)
		}
		items = append(items, model.TransferItem{TargetID: tid, PlanEntryID: e.ID, Status: model.ItemPending, ChecksumAlgo: plan.ChecksumAlgo})
		if e.Action != model.ActionExclude {
			keys = append(keys, [2]string{DestKey(model.LocalPeer, realRoot[e.PlanDestID]), e.DestRelPath})
		}
	}
	var conflict *ledger.ReservationConflict
	err = r.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := ledger.InsertTransfer(tx, t, targets, items); err != nil {
			return err
		}
		if _, err := tx.Exec(`SAVEPOINT reserve`); err != nil {
			return err
		}
		err := ledger.ReservePaths(tx, t.ID, keys)
		if errors.As(err, &conflict) {
			if _, err := tx.Exec(`ROLLBACK TO reserve`); err != nil {
				return err
			}
			return ledger.SetTransferStatus(tx, t.ID, model.TransferNeedsAttention, conflict.Error(), false)
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(`RELEASE reserve`)
		return err
	})
	if err != nil {
		return "", err
	}
	if conflict != nil {
		r.state(t.ID, model.TransferNeedsAttention)
		return t.ID, conflict
	}
	return t.ID, nil
}

func (r *Runner) state(id, status string) {
	if r.OnState != nil {
		r.OnState(id, status)
	}
}

type tstate struct {
	idx      int
	mu       sync.Mutex
	g        model.TransferTarget
	root     *storage.Root
	strategy storage.Strategy
	class    identity.StorageClass
	halted   error
}

func (s *tstate) haltErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.halted
}

func (s *tstate) halt(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.halted == nil {
		s.halted = err
	}
}

type run struct {
	r        *Runner
	ctx      context.Context
	t        *model.Transfer
	plan     *model.Plan
	targets  []*tstate
	byID     map[string]*tstate
	items    []*model.TransferItem
	tele     *Telemetry
	crashed  atomic.Bool
	mu       sync.Mutex
	findings []string
	srcRoots map[string]*storage.Root
}

func (x *run) finding(format string, args ...any) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.findings = append(x.findings, fmt.Sprintf(format, args...))
}

func (x *run) crash(point string, it *model.TransferItem) bool {
	if x.crashed.Load() {
		return true
	}
	if x.r.Hooks.At != nil {
		x.r.Hooks.At(point, *it)
	}
	if x.r.Hooks.Crash != nil && x.r.Hooks.Crash(point, *it) {
		x.crashed.Store(true)
		return true
	}
	return false
}

// write runs a ledger transaction unless the run has "crashed".
func (x *run) write(fn func(*sql.Tx) error) error {
	if x.crashed.Load() {
		return ErrSimulatedCrash
	}
	return x.r.DB.WriteTx(x.ctx, fn)
}

func (x *run) load(ctx context.Context, id string) error {
	q := x.r.DB.Read()
	t, err := ledger.GetTransfer(ctx, q, id)
	if err != nil {
		return err
	}
	plan, err := ledger.GetPlan(ctx, q, t.PlanID)
	if err != nil {
		return err
	}
	targets, err := ledger.GetTargets(ctx, q, id)
	if err != nil {
		return err
	}
	items, err := ledger.GetItems(ctx, q, id)
	if err != nil {
		return err
	}
	x.t, x.plan = t, plan
	x.byID = map[string]*tstate{}
	classOf := map[string]string{}
	for _, d := range plan.Destinations {
		classOf[d.ID] = d.StorageClass
	}
	for i, g := range targets {
		s := &tstate{idx: i, g: g, strategy: storage.Strategy(g.PublishStrategy), class: identity.StorageClass(classOf[g.PlanDestID])}
		if s.class == "" {
			s.class = identity.ClassUnknown
		}
		x.targets = append(x.targets, s)
		x.byID[g.ID] = s
	}
	for i := range items {
		x.items = append(x.items, &items[i])
	}
	x.srcRoots = map[string]*storage.Root{}
	return nil
}

// Run executes every pending item of a transfer and writes its receipt.
func (r *Runner) Run(ctx context.Context, transferID string) (*Result, error) {
	x := &run{r: r, ctx: ctx}
	if err := x.load(ctx, transferID); err != nil {
		return nil, err
	}
	switch x.t.Status {
	case model.TransferQueued, model.TransferRunning:
	default:
		return nil, fmt.Errorf("%w: %s is %s", ErrNotResumable, transferID, x.t.Status)
	}
	return x.execute()
}

func (x *run) execute() (*Result, error) {
	now := ledger.Now()
	if err := x.write(func(tx *sql.Tx) error {
		if err := ledger.SetTransferStatus(tx, x.t.ID, model.TransferRunning, "", false); err != nil {
			return err
		}
		for _, s := range x.targets {
			s.g.Status, s.g.StartedAt, s.g.EndedAt, s.g.Error = model.TransferRunning, now, "", ""
			if err := ledger.UpdateTarget(tx, &s.g); err != nil {
				return err
			}
		}
		for _, it := range x.items {
			if it.Status == model.ItemPending && it.Entry.Action == model.ActionExclude {
				it.Status = model.ItemExcluded
				if err := ledger.UpdateItem(tx, it); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	x.t.Status = model.TransferRunning
	x.r.state(x.t.ID, model.TransferRunning)

	// Bind every target: the root must still be the directory it was when
	// the transfer was created.
	for _, s := range x.targets {
		root, err := storage.OpenRoot(s.g.RootPath)
		if err == nil && (root.Dev != s.g.RootDev || root.Ino != s.g.RootIno) {
			err = fmt.Errorf("%w: %s is no longer the directory this transfer was bound to (device %d inode %d, bound %d/%d)",
				storage.ErrBindingChanged, s.g.RootPath, root.Dev, root.Ino, s.g.RootDev, s.g.RootIno)
		}
		if err != nil {
			s.halt(err)
			x.finding("target %s halted: %v", s.g.Label, err)
			continue
		}
		s.root = root
	}

	x.tele = NewTelemetry(x.t.ID, x.t.Actor, nil)
	for _, s := range x.targets {
		var total, files int64
		for _, it := range x.items {
			if it.TargetID == s.g.ID && it.Entry.Action == model.ActionCopy {
				total += it.Entry.Size
				files++
			}
		}
		x.tele.AddTarget(TargetProgress{TargetID: s.g.ID, Label: s.g.Label, PeerID: s.g.PeerID, TransportBadge: badge(s.class),
			TotalBytes: total, TotalFiles: files, BytesWritten: s.g.BytesWritten, BytesVerified: s.g.BytesVerified,
			DoneFiles: s.g.FilesDone}, s.class)
	}
	stop := make(chan struct{})
	emitted := make(chan struct{})
	go func() {
		defer close(emitted)
		if x.r.OnProgress != nil {
			x.tele.Emit(stop, x.r.OnProgress)
		}
	}()

	x.dispatch()

	close(stop)
	<-emitted
	if x.crashed.Load() {
		return nil, ErrSimulatedCrash
	}
	return x.finish()
}

func badge(c identity.StorageClass) string {
	switch c {
	case identity.ClassSSD:
		return "Local SSD"
	case identity.ClassHDD:
		return "Local HDD"
	case identity.ClassRemovable:
		return "Removable"
	case identity.ClassNetwork:
		return "Network Share"
	}
	return "Local"
}

type unit struct {
	entry model.PlanEntry
	items []*model.TransferItem
}

func (x *run) units() []*unit {
	var out []*unit
	index := map[string]*unit{}
	for _, it := range x.items {
		if it.Status != model.ItemPending {
			continue
		}
		key := it.Entry.Action + "\x00" + it.Entry.SourceRoot + "\x00" + it.Entry.SourceRel
		if it.Entry.Action == model.ActionDir {
			key += "\x00" + it.TargetID + "\x00" + it.Entry.DestRelPath
		}
		u := index[key]
		if u == nil {
			u = &unit{entry: it.Entry}
			index[key] = u
			out = append(out, u)
		}
		u.items = append(u.items, it)
	}
	return out
}

func (x *run) dispatch() {
	classes := []identity.StorageClass{}
	for _, s := range x.targets {
		classes = append(classes, s.class)
	}
	units := x.units()
	if x.r.ClassOf != nil && len(units) > 0 {
		classes = append(classes, x.r.ClassOf(units[0].entry.SourceRoot))
	}
	lanes := LanesForAll(classes...)
	var large, small []*unit
	for _, u := range units {
		if IsLarge(u.entry.Size) {
			large = append(large, u)
		} else {
			small = append(small, u)
		}
	}
	var wg sync.WaitGroup
	lane := func(us []*unit, width int) {
		defer wg.Done()
		sem := make(chan struct{}, width)
		var inner sync.WaitGroup
		for _, u := range us {
			if x.ctx.Err() != nil || x.crashed.Load() {
				break
			}
			select {
			case sem <- struct{}{}:
			case <-x.ctx.Done():
			}
			if x.ctx.Err() != nil {
				break
			}
			inner.Add(1)
			go func(u *unit) {
				defer inner.Done()
				defer func() { <-sem }()
				x.runUnit(u)
			}(u)
		}
		inner.Wait()
	}
	wg.Add(2)
	go lane(large, lanes.Large)
	go lane(small, lanes.Small)
	wg.Wait()
}

func (x *run) runUnit(u *unit) {
	switch u.entry.Action {
	case model.ActionDir:
		for _, it := range u.items {
			x.makeDir(it)
		}
	case model.ActionCopy:
		x.copyUnit(u)
	}
}

func (x *run) makeDir(it *model.TransferItem) {
	s := x.byID[it.TargetID]
	if err := s.haltErr(); err != nil {
		x.settleFailure(it, nil, model.ItemFailed, err)
		return
	}
	parts, err := storage.ValidateRel(it.Entry.DestRelPath)
	if err == nil {
		var d *storage.Dir
		if d, err = storage.OpenDir(s.root, parts, true); err == nil {
			_ = d.Close()
		}
	}
	if err != nil {
		x.settleFailure(it, nil, model.ItemFailed, err)
		return
	}
	it.Status, it.Error = model.ItemVerified, ""
	_ = x.commitItem(it, s, 0, false)
}

// sink is one target's in-flight copy of the unit's source.
type sink struct {
	it      *model.TransferItem
	s       *tstate
	dir     *storage.Dir
	leaf    string
	part    string
	file    *os.File
	res     *storage.Reservation
	created bool
	written int64
}

func partName(leaf string, itemID int64) string {
	const maxLeaf = 180 // keeps the temp name under NAME_MAX for any leaf
	if len(leaf) > maxLeaf {
		cut := maxLeaf
		for cut > 0 && !utf8.RuneStart(leaf[cut]) {
			cut--
		}
		leaf = leaf[:cut]
	}
	return fmt.Sprintf(".%s.%d%s", leaf, itemID, storage.TempSuffix)
}

// cleanup removes job-owned state for a sink: its temp and, under
// reserve_rename, its own untouched reservation. Never anything else.
func (k *sink) cleanup() {
	if k.file != nil {
		_ = k.file.Close()
		k.file = nil
	}
	if k.dir == nil {
		return
	}
	if k.created {
		_ = k.dir.Remove(k.part)
		k.created = false
	}
	if k.res != nil && storage.OwnsReservation(k.dir, k.leaf, k.res.Ino) {
		_ = k.dir.Remove(k.leaf)
	}
	k.res = nil
	_ = k.dir.Sync()
}

func (k *sink) close() {
	if k.dir != nil {
		_ = k.dir.Close()
		k.dir = nil
	}
}

func (x *run) sourceRoot(path string) (*storage.Root, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if r := x.srcRoots[path]; r != nil {
		return r, nil
	}
	r, err := storage.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	x.srcRoots[path] = r
	return r, nil
}

func (x *run) copyUnit(u *unit) {
	algo := x.t.ChecksumAlgo
	var sinks []*sink
	defer func() {
		for _, k := range sinks {
			k.close()
		}
	}()

	// 1. Prepare each destination: contained parent dir, free final name,
	//    reservation if needed, and the temp name recorded before it exists.
	for _, it := range u.items {
		s := x.byID[it.TargetID]
		if err := s.haltErr(); err != nil {
			x.settleFailure(it, nil, model.ItemFailed, fmt.Errorf("target halted: %w", err))
			continue
		}
		dir, leaf, err := storage.OpenFileIn(s.root, it.Entry.DestRelPath, true)
		if err != nil {
			if errors.Is(err, storage.ErrBindingChanged) {
				s.halt(err)
			}
			x.settleFailure(it, nil, model.ItemFailed, err)
			continue
		}
		k := &sink{it: it, s: s, dir: dir, leaf: leaf, part: partName(leaf, it.ID)}
		sinks = append(sinks, k)
		if err := dir.CheckFree(leaf); err != nil {
			x.settleFailure(it, k, model.ItemFailed, fmt.Errorf("%w; Ferry never overwrites", err))
			continue
		}
		it.Status, it.Error, it.Warning = model.ItemCopying, "", ""
		it.PartPath, it.FinalPath = dir.Path(k.part), dir.Path(leaf)
		it.PublishStrategy, it.ReadbackMode, it.ReservationIno = string(s.strategy), "", 0
		it.SourceHash, it.DestHash, it.BytesCopied = "", "", 0
		it.Attempts++
		if err := x.write(func(tx *sql.Tx) error { return ledger.UpdateItem(tx, it) }); err != nil {
			return
		}
		if s.strategy == storage.StrategyReserveRename {
			res, err := storage.Reserve(dir, leaf)
			if err != nil {
				x.settleFailure(it, k, model.ItemFailed, err)
				continue
			}
			k.res = res
			it.ReservationIno = res.Ino
			if err := x.write(func(tx *sql.Tx) error { return ledger.UpdateItem(tx, it) }); err != nil {
				return
			}
		}
		f, err := dir.CreateExcl(k.part)
		if err != nil {
			x.settleFailure(it, k, model.ItemFailed, err)
			continue
		}
		k.file, k.created = f, true
	}
	live := sinks[:0:0]
	for _, k := range sinks {
		if k.file != nil {
			live = append(live, k)
		}
	}
	if len(live) == 0 {
		return
	}
	abortAll := func(status string, err error) {
		for _, k := range live {
			x.settleFailure(k.it, k, status, err)
		}
	}

	// 2. Open the source once, without following links, and check it is
	//    still the file the plan was approved against.
	srcRoot, err := x.sourceRoot(u.entry.SourceRoot)
	if err != nil {
		abortAll(model.ItemFailed, fmt.Errorf("source root: %w", err))
		return
	}
	sdir, sname, err := storage.OpenFileIn(srcRoot, u.entry.SourceRel, false)
	if err != nil {
		abortAll(model.ItemFailed, fmt.Errorf("source: %w", err))
		return
	}
	src, err := sdir.OpenRead(sname)
	_ = sdir.Close()
	if err != nil {
		abortAll(model.ItemFailed, fmt.Errorf("source: %w", err))
		return
	}
	defer func() { _ = src.Close() }()
	before, err := storage.Fstat(src)
	if err != nil {
		abortAll(model.ItemFailed, err)
		return
	}
	if before.Size != u.entry.Size || before.MtimeNS != u.entry.MtimeNS {
		abortAll(model.ItemFailed, fmt.Errorf("%w since planning (size %d->%d, mtime %d->%d); replan",
			storage.ErrSourceChanged, u.entry.Size, before.Size, u.entry.MtimeNS, before.MtimeNS))
		return
	}
	storage.AdviseSource(src)
	if x.crash(PointSourceOpened, live[0].it) {
		return
	}

	// 3. Single read, N sinks.
	writers := make([]io.Writer, len(live))
	for i, k := range live {
		writers[i] = k.file
		if x.r.Hooks.WrapSink != nil {
			writers[i] = x.r.Hooks.WrapSink(k.s.idx, *k.it, k.file)
		}
		x.tele.Phase(k.s.g.ID, k.it.Entry.DestRelPath, PhaseStreaming)
	}
	written := make([]int64, len(live))
	res, ferr := Fanout(x.ctx, src, algo, writers, func(i, n int) {
		written[i] += int64(n)
		x.tele.Wrote(live[i].s.g.ID, int64(n))
	})
	for i, k := range live {
		k.written = written[i]
	}
	if x.crashed.Load() {
		return
	}
	if ferr != nil {
		for i, k := range live {
			switch {
			case res.SinkErrs != nil && res.SinkErrs[i] != nil:
				// The sink's own failure is the truth for that target.
				x.settleFailure(k.it, k, model.ItemFailed, fmt.Errorf("writing %s: %w", k.it.FinalPath, res.SinkErrs[i]))
			case x.ctx.Err() != nil:
				x.settleFailure(k.it, k, model.ItemCanceled, fmt.Errorf("canceled: %w", x.ctx.Err()))
			default:
				x.settleFailure(k.it, k, model.ItemFailed, fmt.Errorf("reading source: %w", ferr))
			}
		}
		return
	}
	after, err := storage.Fstat(src)
	if err == nil && (after.Dev != before.Dev || after.Ino != before.Ino || after.Size != before.Size || after.MtimeNS != before.MtimeNS) {
		err = fmt.Errorf("%w (size %d->%d, mtime %d->%d)", storage.ErrSourceChanged, before.Size, after.Size, before.MtimeNS, after.MtimeNS)
	}
	if err == nil && res.Bytes != before.Size {
		err = fmt.Errorf("%w: read %d bytes of %d", storage.ErrSourceChanged, res.Bytes, before.Size)
	}
	if err != nil {
		abortAll(model.ItemFailed, err)
		return
	}

	// 4. Each sink independently: durability, physical readback, binding
	//    recheck, atomic no-replace publication, commit.
	var wg sync.WaitGroup
	for i, k := range live {
		if serr := res.SinkErrs[i]; serr != nil {
			x.settleFailure(k.it, k, model.ItemFailed, fmt.Errorf("writing %s: %w", k.it.FinalPath, serr))
			continue
		}
		wg.Add(1)
		go func(k *sink) {
			defer wg.Done()
			x.finishSink(k, res.SourceHash, before)
		}(k)
	}
	wg.Wait()
}

func (x *run) finishSink(k *sink, sourceHash string, src storage.FileInfo) {
	it, s := k.it, k.s
	if err := k.file.Sync(); err != nil {
		x.settleFailure(it, k, model.ItemFailed, fmt.Errorf("fsync %s: %w", it.PartPath, err))
		return
	}
	if err := k.file.Close(); err != nil {
		k.file = nil
		x.settleFailure(it, k, model.ItemFailed, fmt.Errorf("close %s: %w", it.PartPath, err))
		return
	}
	k.file = nil
	if x.crash(PointPartWritten, it) {
		return
	}

	x.tele.Phase(s.g.ID, it.Entry.DestRelPath, PhaseVerifying)
	rb, mode, err := k.dir.OpenReadback(k.part)
	if err != nil {
		x.settleFailure(it, k, model.ItemFailed, fmt.Errorf("readback: %w", err))
		return
	}
	destHash, n, err := hashReader(x.ctx, rb, x.t.ChecksumAlgo, func(n int) { x.tele.Verified(s.g.ID, int64(n)) })
	_ = rb.Close()
	if err != nil {
		if x.ctx.Err() != nil {
			x.settleFailure(it, k, model.ItemCanceled, fmt.Errorf("canceled: %w", x.ctx.Err()))
		} else {
			x.settleFailure(it, k, model.ItemFailed, fmt.Errorf("readback: %w", err))
		}
		return
	}
	if destHash != sourceHash || n != src.Size {
		x.settleFailure(it, k, model.ItemFailed, fmt.Errorf("%w: source %s, readback %s (%d of %d bytes); not published",
			storage.ErrChecksumMismatch, sourceHash, destHash, n, src.Size))
		return
	}

	x.tele.Phase(s.g.ID, it.Entry.DestRelPath, PhasePublishing)
	if x.crash(PointBeforePublish, it) {
		return
	}
	// The window between approval and here is closed by rechecking that
	// the storage under the root is still the bound storage.
	if err := k.dir.CheckBinding(); err != nil {
		s.halt(err)
		x.finding("target %s halted before publishing %s: %v", s.g.Label, it.Entry.DestRelPath, err)
		x.settleFailure(it, k, model.ItemFailed, err)
		return
	}
	preserved := k.dir.Chtimes(k.part, src.MtimeNS, src.MtimeNS) == nil
	it.Status, it.SourceHash, it.DestHash, it.ReadbackMode = model.ItemPublishing, sourceHash, destHash, mode
	it.BytesCopied, it.MtimePreserved = k.written, &preserved
	if err := x.write(func(tx *sql.Tx) error { return ledger.UpdateItem(tx, it) }); err != nil {
		return
	}
	if err := storage.Publish(k.dir, k.part, k.leaf, s.strategy, k.res); err != nil {
		if errors.Is(err, storage.ErrDestinationExists) {
			err = fmt.Errorf("%w; the existing file was left untouched", err)
		}
		x.settleFailure(it, k, model.ItemFailed, err)
		return
	}
	k.created, k.res = false, nil
	if x.crash(PointPublished, it) {
		return
	}
	it.Status, it.Error, it.PartPath = model.ItemVerified, "", ""
	_ = x.commitItem(it, s, n, true)
}

// commitItem records a settled item and its target counters in one tx.
func (x *run) commitItem(it *model.TransferItem, s *tstate, bytes int64, isFile bool) error {
	s.mu.Lock()
	if isFile {
		s.g.FilesDone++
		s.g.BytesWritten += bytes
		s.g.BytesVerified += bytes
	}
	g := s.g
	s.mu.Unlock()
	err := x.write(func(tx *sql.Tx) error {
		if err := ledger.UpdateItem(tx, it); err != nil {
			return err
		}
		return ledger.UpdateTarget(tx, &g)
	})
	if isFile {
		x.tele.Phase(s.g.ID, it.Entry.DestRelPath, "")
		x.tele.FileDone(s.g.ID, false)
	}
	return err
}

// settleFailure cleans job-owned state and records a failed or canceled item.
func (x *run) settleFailure(it *model.TransferItem, k *sink, status string, cause error) {
	if x.crashed.Load() {
		return
	}
	if k != nil {
		k.cleanup()
	}
	s := x.byID[it.TargetID]
	it.Status, it.Error, it.PartPath = status, cause.Error(), ""
	it.ReservationIno = 0
	s.mu.Lock()
	if status == model.ItemFailed {
		s.g.FilesFailed++
	}
	g := s.g
	s.mu.Unlock()
	_ = x.write(func(tx *sql.Tx) error {
		if err := ledger.UpdateItem(tx, it); err != nil {
			return err
		}
		return ledger.UpdateTarget(tx, &g)
	})
	x.tele.Phase(s.g.ID, it.Entry.DestRelPath, "")
	if status == model.ItemFailed {
		x.tele.FileDone(s.g.ID, true)
	}
}

func hashReader(ctx context.Context, r io.Reader, algo string, onRead func(int)) (string, int64, error) {
	h, err := checksum.New(algo)
	if err != nil {
		return "", 0, err
	}
	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	buf := *bp
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return "", total, err
		}
		n, err := r.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
			total += int64(n)
			if onRead != nil {
				onRead(n)
			}
		}
		if errors.Is(err, io.EOF) {
			return checksum.Hex(h), total, nil
		}
		if err != nil {
			return "", total, err
		}
	}
}

func (x *run) finish() (*Result, error) {
	halted := false
	for _, s := range x.targets {
		if s.haltErr() != nil {
			halted = true
		}
	}
	for _, it := range x.items {
		if it.Status == model.ItemPending || it.Status == model.ItemCopying || it.Status == model.ItemPublishing {
			if x.ctx.Err() != nil {
				it.Status, it.Error = model.ItemCanceled, "transfer canceled before this item ran"
			} else if s := x.byID[it.TargetID]; s.haltErr() != nil {
				it.Status, it.Error = model.ItemFailed, "target halted: "+s.haltErr().Error()
			}
		}
	}
	// Canceled only if the cancellation actually left work undone; a
	// cancel that lands after the last file settled changes nothing.
	canceled := false
	for _, it := range x.items {
		if it.Status == model.ItemCanceled {
			canceled = true
		}
	}
	type tally struct{ settled, copied, failed, canceled, other int }
	per := map[string]*tally{}
	all := &tally{}
	for _, it := range x.items {
		tl := per[it.TargetID]
		if tl == nil {
			tl = &tally{}
			per[it.TargetID] = tl
		}
		for _, c := range []*tally{tl, all} {
			switch {
			case model.ItemSettled(it.Status):
				c.settled++
				if it.Status != model.ItemExcluded {
					c.copied++
				}
			case it.Status == model.ItemFailed:
				c.failed++
			case it.Status == model.ItemCanceled:
				c.canceled++
			default:
				c.other++
			}
		}
	}
	verdict := func(c *tally, halt bool) string {
		switch {
		case canceled:
			return model.TransferCanceled
		case halt:
			return model.TransferNeedsAttention
		case c.failed == 0 && c.canceled == 0 && c.other == 0:
			return model.TransferCompleted
		case c.copied > 0:
			return model.TransferPartial
		default:
			return model.TransferFailed
		}
	}
	end := ledger.Now()
	for _, s := range x.targets {
		tl := per[s.g.ID]
		if tl == nil {
			tl = &tally{}
		}
		s.g.Status = verdict(tl, s.haltErr() != nil)
		if err := s.haltErr(); err != nil {
			s.g.Error = err.Error()
		}
		s.g.EMAWriteBPS, s.g.EMAVerifyBPS = x.tele.Rates(s.g.ID)
		s.g.EndedAt = end
	}
	status := verdict(all, halted)
	x.t.Status = status
	summary := ""
	if all.failed > 0 || halted {
		summary = fmt.Sprintf("%d item(s) failed", all.failed)
		if len(x.findings) > 0 {
			summary += "; " + strings.Join(x.findings, "; ")
		}
	}
	targets := make([]model.TransferTarget, len(x.targets))
	for i, s := range x.targets {
		targets[i] = s.g
	}
	items := make([]model.TransferItem, len(x.items))
	for i, it := range x.items {
		items[i] = *it
	}
	sort.Strings(x.findings)
	rc := receipt.Build(receipt.NewID(), x.plan, x.t, targets, items, x.findings, time.Now())
	body, err := json.Marshal(rc)
	if err != nil {
		return nil, err
	}
	// The receipt, the transfer's final state and the reservation release
	// commit together. needs_attention keeps its reservations: the job
	// still owns its paths until it is resumed or abandoned.
	if err := x.write(func(tx *sql.Tx) error {
		for i := range targets {
			if err := ledger.UpdateTarget(tx, &targets[i]); err != nil {
				return err
			}
		}
		for _, it := range x.items {
			if err := ledger.UpdateItem(tx, it); err != nil {
				return err
			}
		}
		if err := ledger.SetTransferStatus(tx, x.t.ID, status, summary, status != model.TransferNeedsAttention); err != nil {
			return err
		}
		if err := ledger.InsertReceipt(tx, &ledger.Receipt{ID: rc.ReceiptID, TransferID: x.t.ID, PlanID: x.plan.ID,
			Status: status, Actor: x.t.Actor, Digest: rc.Digest, BodyJSON: string(body), CreatedAt: rc.CreatedAt}); err != nil {
			return err
		}
		if status != model.TransferNeedsAttention {
			return ledger.ReleaseReservations(tx, x.t.ID)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("recording transfer outcome: %w", err)
	}
	x.r.state(x.t.ID, status)
	result := &Result{TransferID: x.t.ID, Status: status, ReceiptID: rc.ReceiptID, Findings: x.findings}
	if x.crash(PointBeforeExport, &model.TransferItem{}) {
		return nil, ErrSimulatedCrash
	}
	result.ReceiptPath, result.ExportError = x.r.export(context.WithoutCancel(x.ctx), rc)
	return result, nil
}

func (r *Runner) export(ctx context.Context, rc *receipt.Receipt) (string, string) {
	if r.ReceiptsDir == "" {
		return "", ""
	}
	path, err := receipt.Export(r.ReceiptsDir, rc)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	_ = r.DB.WriteTx(ctx, func(tx *sql.Tx) error { return ledger.SetReceiptExport(tx, rc.ReceiptID, path, msg) })
	return path, msg
}

// ExportPending re-exports every receipt whose JSON file was never written
// (crash window 3). No bytes are copied.
func (r *Runner) ExportPending(ctx context.Context) ([]string, error) {
	rows, err := r.DB.Read().QueryContext(ctx, `SELECT body_json FROM receipts WHERE exported_path IS NULL`)
	if err != nil {
		return nil, err
	}
	var bodies []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			_ = rows.Close()
			return nil, err
		}
		bodies = append(bodies, b)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	var out []string
	for _, b := range bodies {
		rc, err := receipt.Parse([]byte(b))
		if err != nil {
			return out, err
		}
		path, msg := r.export(ctx, rc)
		if msg != "" {
			return out, errors.New(msg)
		}
		out = append(out, path)
	}
	return out, nil
}

// RecoverOnStartup turns transfers a crash left running into
// needs_attention, drops reservations held by finished transfers, and
// re-exports receipts that never reached disk.
func (r *Runner) RecoverOnStartup(ctx context.Context) ([]string, error) {
	ids, err := ledger.ListTransfersByStatus(ctx, r.DB.Read(), model.TransferQueued, model.TransferRunning, model.TransferVerifying)
	if err != nil {
		return nil, err
	}
	if err := r.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if err := ledger.SetTransferStatus(tx, id, model.TransferNeedsAttention,
				"interrupted while running; resume to reconcile and continue", false); err != nil {
				return err
			}
		}
		_, err := ledger.ReleaseDeadReservations(tx, model.TransferQueued, model.TransferRunning, model.TransferVerifying, model.TransferNeedsAttention)
		return err
	}); err != nil {
		return nil, err
	}
	for _, id := range ids {
		r.state(id, model.TransferNeedsAttention)
	}
	_, err = r.ExportPending(ctx)
	return ids, err
}

// Resume reconciles a stopped transfer's crash windows and runs whatever
// is left. A completed transfer only has its receipt re-exported.
func (r *Runner) Resume(ctx context.Context, transferID string) (*Result, error) {
	x := &run{r: r, ctx: ctx}
	if err := x.load(ctx, transferID); err != nil {
		return nil, err
	}
	switch x.t.Status {
	case model.TransferCompleted:
		rec, err := ledger.LatestReceipt(ctx, r.DB.Read(), transferID)
		if err != nil {
			return nil, err
		}
		res := &Result{TransferID: transferID, Status: x.t.Status, ReceiptID: rec.ID, ReceiptPath: rec.ExportedPath}
		if rec.ExportedPath == "" {
			rc, err := receipt.Parse([]byte(rec.BodyJSON))
			if err != nil {
				return nil, err
			}
			res.ReceiptPath, res.ExportError = r.export(ctx, rc)
		}
		return res, nil
	case model.TransferNeedsAttention, model.TransferPartial, model.TransferFailed, model.TransferCanceled:
	default:
		return nil, fmt.Errorf("%w: %s is %s", ErrNotResumable, transferID, x.t.Status)
	}

	// Window 3 for non-completed outcomes: export any receipt left behind.
	if _, err := r.ExportPending(ctx); err != nil {
		return nil, err
	}

	// Re-acquire reservations (released when the transfer last ended).
	realRoots := map[string]string{}
	for _, s := range x.targets {
		root, err := storage.OpenRoot(s.g.RootPath)
		if err != nil || root.Dev != s.g.RootDev || root.Ino != s.g.RootIno {
			if err == nil {
				err = fmt.Errorf("%w: %s now names a different directory than this transfer was bound to; replan",
					storage.ErrBindingChanged, s.g.RootPath)
			}
			msg := fmt.Sprintf("cannot resume target %s: %v", s.g.Label, err)
			_ = r.DB.WriteTx(ctx, func(tx *sql.Tx) error {
				return ledger.SetTransferStatus(tx, transferID, model.TransferNeedsAttention, msg, false)
			})
			return nil, errors.New(msg)
		}
		s.root = root
		realRoots[s.g.ID] = root.Real
	}
	var keys [][2]string
	for _, it := range x.items {
		if it.Entry.Action != model.ActionExclude {
			keys = append(keys, [2]string{DestKey(model.LocalPeer, realRoots[it.TargetID]), it.Entry.DestRelPath})
		}
	}
	var conflict *ledger.ReservationConflict
	if err := r.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := ledger.ReservePaths(tx, transferID, keys); err != nil {
			if errors.As(err, &conflict) {
				return err
			}
			return err
		}
		return nil
	}); err != nil {
		if conflict != nil {
			_ = r.DB.WriteTx(ctx, func(tx *sql.Tx) error {
				return ledger.SetTransferStatus(tx, transferID, model.TransferNeedsAttention, conflict.Error(), false)
			})
		}
		return nil, err
	}

	if err := x.reconcile(); err != nil {
		return nil, err
	}
	if err := r.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		return ledger.SetTransferStatus(tx, transferID, model.TransferQueued, "", false)
	}); err != nil {
		return nil, err
	}
	x.t.Status = model.TransferQueued
	for _, s := range x.targets {
		s.root = nil
	}
	return x.execute()
}

// reconcile settles the crash windows item by item before anything runs.
//
//   - copying (window 1): remove this job's recorded temp — never a foreign
//     .ferry-part — and its own untouched reservation; re-arm the item.
//   - publishing (window 2): if the final name exists, a full physical
//     readback decides. Equal to the recorded source hash commits it
//     without re-copying; anything else fails the item and is never
//     overwritten. If the final name does not exist, the publish never
//     happened: discard the temp and re-arm.
//   - failed / canceled: re-armed; resuming is the operator saying the
//     cause is gone, and the source and binding are rechecked as it runs.
func (x *run) reconcile() error {
	for _, it := range x.items {
		s := x.byID[it.TargetID]
		switch it.Status {
		case model.ItemCopying:
			if err := x.discardOwned(s, it); err != nil {
				return err
			}
			x.rearm(it)
		case model.ItemPublishing:
			if err := x.reconcilePublished(s, it); err != nil {
				return err
			}
		case model.ItemFailed, model.ItemCanceled:
			x.rearm(it)
		default:
			continue
		}
		if err := x.r.DB.WriteTx(x.ctx, func(tx *sql.Tx) error { return ledger.UpdateItem(tx, it) }); err != nil {
			return err
		}
	}
	// Counters are rebuilt from item truth so a resumed run never counts a
	// file twice.
	for _, s := range x.targets {
		s.g.FilesDone, s.g.FilesFailed, s.g.BytesWritten, s.g.BytesVerified = 0, 0, 0, 0
		for _, it := range x.items {
			if it.TargetID == s.g.ID && it.Status == model.ItemVerified && it.Entry.Action == model.ActionCopy {
				s.g.FilesDone++
				s.g.BytesWritten += it.BytesCopied
				s.g.BytesVerified += it.BytesCopied
			}
		}
	}
	return nil
}

func (x *run) rearm(it *model.TransferItem) {
	it.Status, it.Error, it.PartPath, it.ReservationIno = model.ItemPending, "", "", 0
	it.SourceHash, it.DestHash, it.BytesCopied, it.MtimePreserved = "", "", 0, nil
}

// ownedDir opens the item's parent directory and returns the temp's leaf
// name only if the recorded temp path really is this item's temp inside it.
func (x *run) ownedDir(s *tstate, it *model.TransferItem) (*storage.Dir, string, string, error) {
	dir, leaf, err := storage.OpenFileIn(s.root, it.Entry.DestRelPath, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", "", nil
		}
		return nil, "", "", err
	}
	part := ""
	if it.PartPath != "" && filepath.Dir(it.PartPath) == dir.DirPath() &&
		filepath.Base(it.PartPath) == partName(leaf, it.ID) && strings.HasSuffix(it.PartPath, storage.TempSuffix) {
		part = filepath.Base(it.PartPath)
	}
	return dir, leaf, part, nil
}

func (x *run) discardOwned(s *tstate, it *model.TransferItem) error {
	dir, leaf, part, err := x.ownedDir(s, it)
	if err != nil {
		return fmt.Errorf("reconciling %s: %w", it.Entry.DestRelPath, err)
	}
	if dir == nil {
		return nil
	}
	defer func() { _ = dir.Close() }()
	if part != "" {
		if err := dir.Remove(part); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("removing this job's temp %s: %w", it.PartPath, err)
		}
	}
	if it.ReservationIno != 0 && storage.OwnsReservation(dir, leaf, it.ReservationIno) {
		if err := dir.Remove(leaf); err != nil {
			return fmt.Errorf("removing this job's reservation %s: %w", dir.Path(leaf), err)
		}
	}
	return dir.Sync()
}

func (x *run) reconcilePublished(s *tstate, it *model.TransferItem) error {
	dir, leaf, part, err := x.ownedDir(s, it)
	if err != nil {
		return fmt.Errorf("reconciling %s: %w", it.Entry.DestRelPath, err)
	}
	if dir == nil {
		x.rearm(it)
		return nil
	}
	defer func() { _ = dir.Close() }()
	fi, err := dir.Lstat(leaf)
	// Still our zero-byte reservation means the rename never happened.
	stillReserved := it.ReservationIno != 0 && storage.OwnsReservation(dir, leaf, it.ReservationIno)
	published := err == nil && fi.IsRegular() && !stillReserved
	if !published {
		if err := x.discardOwned(s, it); err != nil {
			return err
		}
		x.rearm(it)
		return nil
	}
	rb, mode, err := dir.OpenReadback(leaf)
	if err != nil {
		it.Status, it.Error = model.ItemFailed, "published output could not be read back after an interruption: "+err.Error()
		return nil
	}
	sum, _, err := hashReader(x.ctx, rb, x.t.ChecksumAlgo, nil)
	_ = rb.Close()
	if err != nil {
		it.Status, it.Error = model.ItemFailed, "published output could not be read back after an interruption: "+err.Error()
		return nil
	}
	if sum != it.SourceHash {
		it.Status = model.ItemFailed
		it.Error = "published output no longer matches its source after an interruption; refusing to overwrite — replan"
		return nil
	}
	// Ours, whole and correct: commit without copying a byte. A hard-link
	// crash between link and unlink leaves the temp as a second name.
	if part != "" {
		_ = dir.Remove(part)
		_ = dir.Sync()
	}
	it.Status, it.DestHash, it.ReadbackMode, it.Error, it.PartPath = model.ItemVerified, sum, mode, "", ""
	it.Warning = "verified after an interruption; not re-copied"
	return nil
}
