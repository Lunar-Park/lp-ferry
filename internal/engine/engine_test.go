package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/ledger"
	"github.com/Lunar-Park/lp-ferry/internal/model"
	"github.com/Lunar-Park/lp-ferry/internal/receipt"
	"github.com/Lunar-Park/lp-ferry/internal/storage"
)

func TestFanoutHappyPathThreeDestinations(t *testing.T) {
	e := newEnv(t)
	files := map[string][]byte{
		"PRIVATE/M4ROOT/CLIP/C0001.MP4": randomBytes(5*ChunkSize + 123),
		"PRIVATE/M4ROOT/CLIP/C0001.XML": []byte("<xml/>"),
		"empty.txt":                     {},
	}
	var rels []string
	for rel, b := range files {
		e.write(rel, b)
		rels = append(rels, rel)
	}
	dests := []string{e.dest("SSD"), e.dest("NAS"), e.dest("Backup")}
	pid, fp := e.plan(rels, dests, planOpts{})
	var o opens
	e.runner.Hooks.At = o.hook
	var events atomic.Int32
	var last Progress
	var mu sync.Mutex
	e.runner.OnProgress = func(p Progress) {
		events.Add(1)
		mu.Lock()
		last = p
		mu.Unlock()
	}
	res, err := e.runner.Run(context.Background(), e.start(pid, fp))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != model.TransferCompleted || res.ExportError != "" {
		t.Fatalf("%+v", res)
	}
	for rel, want := range files {
		if o.get(rel) != 1 {
			t.Fatalf("%s source opened %d times; fan-out must read once", rel, o.get(rel))
		}
		srcFi, _ := os.Stat(filepath.Join(e.src, rel))
		for _, d := range dests {
			p := filepath.Join(d, rel)
			if !bytes.Equal(readFile(t, p), want) {
				t.Fatalf("%s differs", p)
			}
			fi, _ := os.Stat(p)
			if !fi.ModTime().Equal(srcFi.ModTime()) {
				t.Fatalf("mtime not preserved on %s", p)
			}
		}
	}
	for _, d := range dests {
		noTemps(t, d)
	}
	rb := map[string]string{"darwin": "f_nocache", "linux": "fadvise_dontneed"}[runtime.GOOS]
	for _, it := range e.items(res.TransferID) {
		if it.Status != model.ItemVerified || it.ReadbackMode != rb || it.SourceHash == "" || it.SourceHash != it.DestHash {
			t.Fatalf("item %+v", it)
		}
	}
	rc, err := receipt.Parse(readFile(t, res.ReceiptPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.Files) != 9 || len(rc.Targets) != 3 || rc.Status != model.TransferCompleted {
		t.Fatalf("receipt %+v", rc)
	}
	for _, tg := range rc.Targets {
		if tg.PublishStrategy != string(storage.StrategyHardlink) || tg.Status != model.TransferCompleted || tg.FilesDone != 3 {
			t.Fatalf("target %+v", tg)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if events.Load() == 0 || last.OverallFraction != 1 || last.ETASeconds != 0 || last.Actor != "human" {
		t.Fatalf("final progress %+v (events %d)", last, events.Load())
	}
	// Reservations were released with the receipt.
	var n int
	_ = e.db.Read().QueryRow(`SELECT COUNT(*) FROM path_reservations`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d reservations left", n)
	}
}

// T1.1 — source mutated mid-copy: fails cleanly, nothing published.
func TestT1_1_SourceMutatedMidCopy(t *testing.T) {
	for _, mode := range []string{"append", "rewrite-same-size"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			e.write("A001.MOV", randomBytes(6*ChunkSize))
			d1, d2 := e.dest("SSD"), e.dest("NAS")
			pid, fp := e.plan([]string{"A001.MOV"}, []string{d1, d2}, planOpts{})
			var once sync.Once
			e.runner.Hooks.WrapSink = func(target int, it model.TransferItem, w io.Writer) io.Writer {
				return writerFunc(func(p []byte) (int, error) {
					once.Do(func() {
						path := filepath.Join(e.src, "A001.MOV")
						if mode == "append" {
							f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
							_, _ = f.Write([]byte("edited"))
							_ = f.Close()
						} else {
							f, _ := os.OpenFile(path, os.O_WRONLY, 0)
							_, _ = f.WriteAt([]byte("X"), 5*ChunkSize)
							_ = f.Close()
							later := time.Now().Add(3 * time.Second)
							_ = os.Chtimes(path, later, later)
						}
					})
					return w.Write(p)
				})
			}
			res, err := e.runner.Run(context.Background(), e.start(pid, fp))
			if err != nil {
				t.Fatal(err)
			}
			if res.Status != model.TransferFailed {
				t.Fatalf("status %s", res.Status)
			}
			for _, d := range []string{d1, d2} {
				if _, err := os.Lstat(filepath.Join(d, "A001.MOV")); !os.IsNotExist(err) {
					t.Fatalf("partial published at %s", d)
				}
				noTemps(t, d)
			}
			for _, it := range e.items(res.TransferID) {
				if it.Status != model.ItemFailed || !bytes.Contains([]byte(it.Error), []byte("source changed")) {
					t.Fatalf("%+v", it)
				}
			}
		})
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// T1.2 — a file created at the destination after planning is never
// overwritten, under every publish strategy.
func TestT1_2_DestinationCreatedAfterPlan(t *testing.T) {
	for _, s := range []storage.Strategy{storage.StrategyHardlink, storage.StrategyRenameNoReplace, storage.StrategyReserveRename} {
		t.Run(string(s), func(t *testing.T) {
			e := newEnv(t)
			e.write("a.mov", randomBytes(3*ChunkSize))
			e.write("b.mov", randomBytes(1000))
			d := e.dest("SSD")
			pid, fp := e.plan([]string{"a.mov", "b.mov"}, []string{d}, planOpts{strategy: s})
			id := e.start(pid, fp)
			// b.mov appears after approval, before the run.
			if err := os.WriteFile(filepath.Join(d, "b.mov"), []byte("theirs-b"), 0o644); err != nil {
				t.Fatal(err)
			}
			// a.mov appears in the race window right before publication.
			e.runner.Hooks.At = func(point string, it model.TransferItem) {
				if point == PointBeforePublish && it.Entry.DestRelPath == "a.mov" {
					p := filepath.Join(d, "a.mov")
					if s == storage.StrategyReserveRename {
						// Our reservation is there; someone replaces it.
						_ = os.Remove(p)
					}
					_ = os.WriteFile(p, []byte("theirs-a"), 0o644)
				}
			}
			res, err := e.runner.Run(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if res.Status != model.TransferFailed {
				t.Fatalf("status %s", res.Status)
			}
			if string(readFile(t, filepath.Join(d, "a.mov"))) != "theirs-a" || string(readFile(t, filepath.Join(d, "b.mov"))) != "theirs-b" {
				t.Fatal("an existing destination file was overwritten")
			}
			noTemps(t, d)
			for _, it := range e.items(id) {
				if it.Status != model.ItemFailed || !bytes.Contains([]byte(it.Error), []byte("already exists")) {
					t.Fatalf("%+v", it)
				}
			}
		})
	}
}

// T1.3 — symlinks at the leaf or any ancestor are refused; zero writes.
func TestT1_3_SymlinksBlocked(t *testing.T) {
	e := newEnv(t)
	e.write("Day1/a.mov", randomBytes(100))
	e.write("Day1/b.mov", randomBytes(100))
	outside := e.dest("outside")
	victim := filepath.Join(outside, "victim.mov")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := e.dest("SSD")
	pid, fp := e.plan([]string{"Day1/a.mov", "Day1/b.mov"}, []string{d}, planOpts{})
	id := e.start(pid, fp)
	// After planning: the destination ancestor becomes a link out of the root.
	if err := os.Symlink(outside, filepath.Join(d, "Day1")); err != nil {
		t.Fatal(err)
	}
	res, err := e.runner.Run(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != model.TransferFailed {
		t.Fatalf("status %s", res.Status)
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 1 || string(readFile(t, victim)) != "keep" {
		t.Fatalf("wrote through a symlink: %v", entries)
	}

	// Leaf symlink at the destination, and a source that became a symlink.
	e2 := newEnv(t)
	e2.write("x.mov", randomBytes(10))
	e2.write("y.mov", randomBytes(10))
	d2 := e2.dest("SSD")
	pid, fp = e2.plan([]string{"x.mov", "y.mov"}, []string{d2}, planOpts{})
	id = e2.start(pid, fp)
	if err := os.Symlink(victim, filepath.Join(d2, "x.mov")); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(e2.src, "y.mov"))
	if err := os.Symlink(victim, filepath.Join(e2.src, "y.mov")); err != nil {
		t.Fatal(err)
	}
	res, err = e2.runner.Run(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != model.TransferFailed || string(readFile(t, victim)) != "keep" {
		t.Fatalf("status %s", res.Status)
	}
	if _, err := os.Lstat(filepath.Join(d2, "y.mov")); !os.IsNotExist(err) {
		t.Fatal("followed a source symlink")
	}
	noTemps(t, d2)
}

// T1.4 — the destination's storage changes mid-transfer (an unmounted
// share leaving its mount directory behind): halt before publishing.
func TestT1_4_MountChangesMidTransfer(t *testing.T) {
	e := newEnv(t)
	e.write("1.mov", randomBytes(1000))
	e.write("2.mov", randomBytes(3*ChunkSize))
	nas := e.dest("NAS")
	pid, fp := e.plan([]string{"1.mov", "2.mov"}, []string{nas}, planOpts{})
	id := e.start(pid, fp)
	e.runner.Hooks.At = func(point string, it model.TransferItem) {
		if point == PointBeforePublish && it.Entry.DestRelPath == "2.mov" {
			_ = os.Rename(nas, filepath.Join(e.base, "NAS-unmounted"))
			_ = os.Mkdir(nas, 0o755) // the leftover mount directory
		}
	}
	// Serialize so 1.mov settles first.
	res, err := e.runner.Run(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != model.TransferNeedsAttention {
		t.Fatalf("status %s", res.Status)
	}
	if entries, _ := os.ReadDir(nas); len(entries) != 0 {
		t.Fatalf("wrote into the leftover mount directory: %v", entries)
	}
	noTemps(t, filepath.Join(e.base, "NAS-unmounted"))
	var n int
	_ = e.db.Read().QueryRow(`SELECT COUNT(*) FROM path_reservations WHERE transfer_id = ?`, id).Scan(&n)
	if n == 0 {
		t.Fatal("needs_attention must keep its reservations")
	}
	// Resuming against the wrong directory is refused, still writing nothing.
	if _, err := e.runner.Resume(context.Background(), id); err == nil {
		t.Fatal("resume onto a different directory succeeded")
	}
	if entries, _ := os.ReadDir(nas); len(entries) != 0 {
		t.Fatalf("resume wrote into the leftover directory: %v", entries)
	}
}

// T1.5 — single-read fan-out to three destinations; #2 hits ENOSPC.
func TestT1_5_OneDestinationFailsOthersComplete(t *testing.T) {
	e := newEnv(t)
	big := randomBytes(10*ChunkSize + 7)
	e.write("big.mov", big)
	e.write("small.xml", []byte("<x/>"))
	dests := []string{e.dest("D1"), e.dest("D2"), e.dest("D3")}
	pid, fp := e.plan([]string{"big.mov", "small.xml"}, dests, planOpts{})
	var o opens
	e.runner.Hooks.At = o.hook
	e.runner.Hooks.WrapSink = func(target int, it model.TransferItem, w io.Writer) io.Writer {
		if target != 1 || it.Entry.DestRelPath != "big.mov" {
			return w
		}
		var n int
		return writerFunc(func(p []byte) (int, error) {
			if n+len(p) > 3*ChunkSize {
				return 0, &os.PathError{Op: "write", Path: "D2/big.mov", Err: syscall.ENOSPC}
			}
			n += len(p)
			return w.Write(p)
		})
	}
	res, err := e.runner.Run(context.Background(), e.start(pid, fp))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != model.TransferPartial {
		t.Fatalf("status %s", res.Status)
	}
	if o.get("big.mov") != 1 {
		t.Fatalf("source read %d times", o.get("big.mov"))
	}

	for i, d := range dests {
		p := filepath.Join(d, "big.mov")
		if i == 1 {
			if _, err := os.Lstat(p); !os.IsNotExist(err) {
				t.Fatal("failed destination published a file")
			}
		} else if !bytes.Equal(readFile(t, p), big) {
			t.Fatalf("%s wrong", p)
		}
		noTemps(t, d)
	}
	rc, err := receipt.Parse(readFile(t, res.ReceiptPath))
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, tg := range rc.Targets {
		statuses[tg.Label] = tg.Status
	}
	if statuses["D1"] != model.TransferCompleted || statuses["D2"] != model.TransferPartial || statuses["D3"] != model.TransferCompleted {
		t.Fatalf("per-target truth wrong: %v", statuses)
	}
	var d2Failed bool
	for _, f := range rc.Files {
		if f.DestRelPath == "big.mov" && f.Action == receipt.ActionFailed {
			d2Failed = bytes.Contains([]byte(f.Reason), []byte("no space left"))
		}
	}
	if !d2Failed {
		t.Fatalf("receipt does not record D2's ENOSPC: %+v", rc.Files)
	}
}

// T1.6 — cancel mid-chunk on a 100 MiB file: stops within 200 ms, cleans
// the job's temp, keeps the file already verified.
func TestT1_6_CancelMidChunk(t *testing.T) {
	e := newEnv(t)
	e.write("first.wav", randomBytes(4096))
	e.write("huge.mov", randomBytes(100<<20))
	d := e.dest("SSD")
	pid, fp := e.plan([]string{"first.wav", "huge.mov"}, []string{d}, planOpts{})
	id := e.start(pid, fp)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstDone := make(chan struct{})
	e.runner.Hooks.At = func(point string, it model.TransferItem) {
		if point == PointPublished && it.Entry.DestRelPath == "first.wav" {
			close(firstDone)
		}
	}
	var canceledAt atomic.Int64
	e.runner.Hooks.WrapSink = func(_ int, it model.TransferItem, w io.Writer) io.Writer {
		if it.Entry.DestRelPath != "huge.mov" {
			return w
		}
		var n int
		return writerFunc(func(p []byte) (int, error) {
			if n == 0 {
				<-firstDone
			}
			n += len(p)
			if n >= 20<<20 && canceledAt.Load() == 0 {
				canceledAt.Store(time.Now().UnixNano())
				cancel()
			}
			return w.Write(p)
		})
	}
	res, err := e.runner.Run(ctx, id)
	returned := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	elapsed := returned.Sub(time.Unix(0, canceledAt.Load()))
	t.Logf("cancel to return: %s", elapsed)
	if elapsed > 200*time.Millisecond {
		t.Fatalf("cancellation took %s (> 200ms)", elapsed)
	}
	if res.Status != model.TransferCanceled {
		t.Fatalf("status %s", res.Status)
	}
	noTemps(t, d)
	if _, err := os.Lstat(filepath.Join(d, "huge.mov")); !os.IsNotExist(err) {
		t.Fatal("canceled file was published")
	}
	if got := readFile(t, filepath.Join(d, "first.wav")); len(got) != 4096 {
		t.Fatal("prior verified file lost")
	}
	st := map[string]string{}
	for _, it := range e.items(id) {
		st[it.Entry.DestRelPath] = it.Status
	}
	if st["first.wav"] != model.ItemVerified || st["huge.mov"] != model.ItemCanceled {
		t.Fatalf("%v", st)
	}
	// And the canceled transfer resumes to completion.
	e.runner.Hooks = Hooks{}
	res, err = e.runner.Resume(context.Background(), id)
	if err != nil || res.Status != model.TransferCompleted {
		t.Fatalf("resume: %+v %v", res, err)
	}
}

func TestEveryDestinationFailingKeepsEachError(t *testing.T) {
	e := newEnv(t)
	e.write("a.mov", randomBytes(3*ChunkSize))
	pid, fp := e.plan([]string{"a.mov"}, []string{e.dest("D1"), e.dest("D2")}, planOpts{})
	e.runner.Hooks.WrapSink = func(target int, _ model.TransferItem, w io.Writer) io.Writer {
		errno := []syscall.Errno{syscall.ENOSPC, syscall.EIO}[target]
		return writerFunc(func([]byte) (int, error) { return 0, errno })
	}
	res, err := e.runner.Run(context.Background(), e.start(pid, fp))
	if err != nil || res.Status != model.TransferFailed {
		t.Fatalf("%+v %v", res, err)
	}
	items := e.items(res.TransferID)
	if !bytes.Contains([]byte(items[0].Error+items[1].Error), []byte("no space left")) ||
		!bytes.Contains([]byte(items[0].Error+items[1].Error), []byte("input/output error")) {
		t.Fatalf("per-destination errors lost: %q / %q", items[0].Error, items[1].Error)
	}
}

func crashAt(point, rel string) func(string, model.TransferItem) bool {
	return func(p string, it model.TransferItem) bool { return p == point && it.Entry.DestRelPath == rel }
}

func (e *env) crashThenResume(id string, crash func(string, model.TransferItem) bool, between func()) (*Result, *opens) {
	e.t.Helper()
	e.runner.Hooks = Hooks{Crash: crash}
	if _, err := e.runner.Run(context.Background(), id); !errors.Is(err, ErrSimulatedCrash) {
		e.t.Fatalf("want simulated crash, got %v", err)
	}
	// A fresh "process": new runner over the same ledger file.
	db2, err := ledger.Open(e.db.Path())
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = db2.Close() })
	e.db = db2
	o := &opens{}
	e.runner = &Runner{DB: db2, ReceiptsDir: e.receipts, Hooks: Hooks{At: o.hook}}
	if _, err := e.runner.RecoverOnStartup(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	if between != nil {
		between()
	}
	res, err := e.runner.Resume(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return res, o
}

// T1.7 — the three crash windows recover on resume without re-copying
// verified files.
func TestT1_7_CrashWindows(t *testing.T) {
	setup := func(t *testing.T, s storage.Strategy) (*env, string, string, map[string][]byte) {
		e := newEnv(t)
		files := map[string][]byte{"A.mov": randomBytes(3 * ChunkSize), "B.mov": randomBytes(2*ChunkSize + 9), "C.mov": randomBytes(512)}
		for rel, b := range files {
			e.write(rel, b)
		}
		d := e.dest("SSD")
		pid, fp := e.plan([]string{"A.mov", "B.mov", "C.mov"}, []string{d}, planOpts{strategy: s})
		return e, d, e.start(pid, fp), files
	}
	inode := func(p string) uint64 {
		fi, err := os.Stat(p)
		if err != nil {
			return 0
		}
		return fi.Sys().(*syscall.Stat_t).Ino
	}

	for _, s := range []storage.Strategy{storage.StrategyHardlink, storage.StrategyReserveRename} {
		t.Run("window1-unpublished-part/"+string(s), func(t *testing.T) {
			e, d, id, files := setup(t, s)
			foreign := filepath.Join(d, ".B.mov.999999.ferry-part")
			var aIno uint64
			res, o := e.crashThenResume(id, crashAt(PointPartWritten, "B.mov"), func() {
				st := map[string]model.TransferItem{}
				for _, it := range e.items(id) {
					st[it.Entry.DestRelPath] = it
				}
				if st["B.mov"].Status != model.ItemCopying || st["B.mov"].PartPath == "" {
					t.Fatalf("crash state %+v", st["B.mov"])
				}
				if _, err := os.Stat(st["B.mov"].PartPath); err != nil {
					t.Fatalf("part should exist after crash: %v", err)
				}
				if err := os.WriteFile(foreign, []byte("not ours"), 0o644); err != nil {
					t.Fatal(err)
				}
				if st["A.mov"].Status == model.ItemVerified {
					aIno = inode(filepath.Join(d, "A.mov"))
				}
			})
			if res.Status != model.TransferCompleted {
				t.Fatalf("%+v", res)
			}
			for rel, b := range files {
				if !bytes.Equal(readFile(t, filepath.Join(d, rel)), b) {
					t.Fatalf("%s wrong after resume", rel)
				}
			}
			if string(readFile(t, foreign)) != "not ours" {
				t.Fatal("a foreign .ferry-part was touched")
			}
			if aIno != 0 && (inode(filepath.Join(d, "A.mov")) != aIno || o.get("A.mov") != 0) {
				t.Fatal("A.mov was verified before the crash and was re-copied")
			}
			if o.get("B.mov") != 1 {
				t.Fatalf("B.mov opened %d times on resume", o.get("B.mov"))
			}
		})
	}

	t.Run("window2-published-uncommitted", func(t *testing.T) {
		e, d, id, files := setup(t, storage.StrategyHardlink)
		var bIno uint64
		res, o := e.crashThenResume(id, crashAt(PointPublished, "B.mov"), func() {
			for _, it := range e.items(id) {
				if it.Entry.DestRelPath == "B.mov" && it.Status != model.ItemPublishing {
					t.Fatalf("crash state %+v", it)
				}
			}
			bIno = inode(filepath.Join(d, "B.mov"))
			// Simulate dying between link() and unlink(): the temp is
			// still a second name for the published file.
			for _, it := range e.items(id) {
				if it.Entry.DestRelPath == "B.mov" {
					_ = os.Link(filepath.Join(d, "B.mov"), filepath.Join(d, partName("B.mov", it.ID)))
				}
			}
		})
		if res.Status != model.TransferCompleted {
			t.Fatalf("%+v", res)
		}
		if o.get("B.mov") != 0 || inode(filepath.Join(d, "B.mov")) != bIno {
			t.Fatal("published-but-uncommitted file was re-copied")
		}
		if !bytes.Equal(readFile(t, filepath.Join(d, "B.mov")), files["B.mov"]) {
			t.Fatal("B.mov wrong")
		}
		noTemps(t, d)
		for _, it := range e.items(id) {
			if it.Entry.DestRelPath == "B.mov" && (it.Status != model.ItemVerified || it.Warning == "") {
				t.Fatalf("%+v", it)
			}
		}
	})

	t.Run("window2-tampered-is-never-overwritten", func(t *testing.T) {
		e, d, id, _ := setup(t, storage.StrategyHardlink)
		res, _ := e.crashThenResume(id, crashAt(PointPublished, "B.mov"), func() {
			if err := os.WriteFile(filepath.Join(d, "B.mov"), []byte("edited by someone"), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		if res.Status != model.TransferPartial {
			t.Fatalf("%+v", res)
		}
		if string(readFile(t, filepath.Join(d, "B.mov"))) != "edited by someone" {
			t.Fatal("overwrote a file that no longer matched")
		}
	})

	t.Run("window3-committed-receipt-unexported", func(t *testing.T) {
		e, d, id, _ := setup(t, storage.StrategyHardlink)
		var exported []string
		inodes := map[string]uint64{}
		e.runner.Hooks = Hooks{Crash: func(p string, _ model.TransferItem) bool { return p == PointBeforeExport }}
		if _, err := e.runner.Run(context.Background(), id); !errors.Is(err, ErrSimulatedCrash) {
			t.Fatalf("want crash, got %v", err)
		}
		for _, rel := range []string{"A.mov", "B.mov", "C.mov"} {
			inodes[rel] = inode(filepath.Join(d, rel))
		}
		if entries, _ := os.ReadDir(e.receipts); len(entries) != 0 {
			t.Fatalf("receipt exported before the crash point: %v", entries)
		}
		if tr := e.transfer(id); tr.Status != model.TransferCompleted {
			t.Fatalf("status %s", tr.Status)
		}
		o := &opens{}
		e.runner = &Runner{DB: e.db, ReceiptsDir: e.receipts, Hooks: Hooks{At: o.hook}}
		if _, err := e.runner.RecoverOnStartup(context.Background()); err != nil {
			t.Fatal(err)
		}
		res, err := e.runner.Resume(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		exported, _ = filepath.Glob(filepath.Join(e.receipts, "*.json"))
		if len(exported) != 1 || res.ReceiptPath != exported[0] {
			t.Fatalf("receipt not re-exported: %v %+v", exported, res)
		}
		if _, err := receipt.Parse(readFile(t, exported[0])); err != nil {
			t.Fatal(err)
		}
		for rel, ino := range inodes {
			if o.get(rel) != 0 || inode(filepath.Join(d, rel)) != ino {
				t.Fatalf("%s re-copied during receipt re-export", rel)
			}
		}
	})
}

func TestStaleApprovalAndReservationConflict(t *testing.T) {
	e := newEnv(t)
	e.write("a.mov", randomBytes(10))
	d := e.dest("SSD")
	pid, fp := e.plan([]string{"a.mov"}, []string{d}, planOpts{})
	if _, err := e.runner.Create(context.Background(), pid, "not-the-fingerprint", "agent:test"); !errors.Is(err, ledger.ErrStaleApproval) {
		t.Fatalf("stale fingerprint accepted: %v", err)
	}
	first := e.start(pid, fp)
	// A second plan writing the same path (different case) to the same root.
	e.write("A.MOV", randomBytes(10))
	pid2, fp2 := e.plan([]string{"A.MOV"}, []string{d}, planOpts{})
	second, err := e.runner.Create(context.Background(), pid2, fp2, "human")
	var rc *ledger.ReservationConflict
	if !errors.As(err, &rc) || rc.HolderID != first || second == "" {
		t.Fatalf("want conflict naming %s, got %v", first, err)
	}
	if tr := e.transfer(second); tr.Status != model.TransferNeedsAttention || !bytes.Contains([]byte(tr.ErrorSummary), []byte(first)) {
		t.Fatalf("%+v", tr)
	}
	if _, err := e.runner.Run(context.Background(), first); err != nil {
		t.Fatal(err)
	}
}

func TestStartupMarksRunningNeedsAttention(t *testing.T) {
	e := newEnv(t)
	e.write("a.mov", randomBytes(10))
	pid, fp := e.plan([]string{"a.mov"}, []string{e.dest("SSD")}, planOpts{})
	id := e.start(pid, fp)
	e.runner.Hooks.Crash = crashAt(PointSourceOpened, "a.mov")
	if _, err := e.runner.Run(context.Background(), id); !errors.Is(err, ErrSimulatedCrash) {
		t.Fatal(err)
	}
	if e.transfer(id).Status != model.TransferRunning {
		t.Fatal("expected a crashed running transfer")
	}
	ids, err := e.runner.RecoverOnStartup(context.Background())
	if err != nil || len(ids) != 1 || e.transfer(id).Status != model.TransferNeedsAttention {
		t.Fatalf("%v %v %s", ids, err, e.transfer(id).Status)
	}
}

func TestBlake3AndDirsAndExclusions(t *testing.T) {
	e := newEnv(t)
	e.write("keep.mov", randomBytes(ChunkSize+1))
	e.write(".DS_Store", []byte("junk"))
	d := e.dest("SSD")
	pid, fp := e.plan([]string{"keep.mov", ".DS_Store"}, []string{d}, planOpts{algo: "blake3"})
	// Turn .DS_Store into an exclusion and add an empty directory entry.
	_, err := e.db.Read().Exec(`UPDATE plan_entries SET action='exclude', exclusion_reason='system artifact' WHERE source_rel='.DS_Store'`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.db.Read().Exec(`INSERT INTO plan_entries (plan_id, plan_dest_id, source_root, source_rel, source_path, dest_rel_path, action)
		VALUES (?, ?, ?, 'Empty', ?, 'Empty/Folder', 'dir')`, pid, pid+"_d1", e.src, e.src)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.runner.Run(context.Background(), e.start(pid, fp))
	if err != nil || res.Status != model.TransferCompleted {
		t.Fatalf("%+v %v", res, err)
	}
	if fi, err := os.Stat(filepath.Join(d, "Empty", "Folder")); err != nil || !fi.IsDir() {
		t.Fatal("empty directory not retained")
	}
	if _, err := os.Lstat(filepath.Join(d, ".DS_Store")); !os.IsNotExist(err) {
		t.Fatal("excluded file copied")
	}
	rc, _ := receipt.Parse(readFile(t, res.ReceiptPath))
	if rc.ChecksumAlgo != "blake3" {
		t.Fatal(rc.ChecksumAlgo)
	}
	var excluded bool
	for _, f := range rc.Files {
		if f.Action == receipt.ActionExcluded && f.Reason == "system artifact" {
			excluded = true
		}
	}
	if !excluded {
		t.Fatal("exclusion not recorded with its reason")
	}
}

// A cancel that arrives before the run even loads its state still ends
// the transfer as canceled with a receipt, never stuck in queued.
func TestCancelBeforeRunStarts(t *testing.T) {
	e := newEnv(t)
	e.write("a.mov", randomBytes(3*ChunkSize))
	pid, fp := e.plan([]string{"a.mov"}, []string{e.dest("SSD")}, planOpts{})
	id := e.start(pid, fp)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := e.runner.Run(ctx, id)
	if err != nil || res.Status != model.TransferCanceled || res.ReceiptPath == "" {
		t.Fatalf("%+v %v", res, err)
	}
	if tr := e.transfer(id); tr.Status != model.TransferCanceled {
		t.Fatalf("status %s", tr.Status)
	}
}
