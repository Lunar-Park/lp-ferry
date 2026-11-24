package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Lunar-Park/lp-ferry/internal/ledger"
	"github.com/Lunar-Park/lp-ferry/internal/model"
	"github.com/Lunar-Park/lp-ferry/internal/storage"
)

// env is a real temp-dir world: a source folder, destination roots, a real
// SQLite WAL ledger and a receipts directory. Nothing is mocked.
type env struct {
	t        *testing.T
	base     string
	src      string
	db       *ledger.DB
	runner   *Runner
	receipts string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	base := t.TempDir()
	e := &env{t: t, base: base, src: filepath.Join(base, "CARD"), receipts: filepath.Join(base, "receipts")}
	if err := os.MkdirAll(e.src, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := ledger.Open(filepath.Join(base, "state", "ferry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e.db = db
	e.runner = &Runner{DB: db, ReceiptsDir: e.receipts}
	return e
}

func (e *env) dest(name string) string {
	p := filepath.Join(e.base, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func (e *env) write(rel string, data []byte) {
	p := filepath.Join(e.src, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

type planOpts struct {
	strategy storage.Strategy
	algo     string
}

// plan builds and approves a plan copying every source file (sorted) to
// each destination under the same relative path.
func (e *env) plan(rels []string, dests []string, o planOpts) (string, string) {
	e.t.Helper()
	if o.algo == "" {
		o.algo = "sha256"
	}
	p := &model.Plan{ID: newID("pln_"), Actor: "human", ChecksumAlgo: o.algo}
	for i, d := range dests {
		p.Destinations = append(p.Destinations, model.PlanDestination{
			ID: fmt.Sprintf("%s_d%d", p.ID, i+1), Label: filepath.Base(d), RootPath: d, PeerID: model.LocalPeer,
			StorageClass: "ssd", PublishStrategy: string(o.strategy),
		})
	}
	sort.Strings(rels)
	h := sha256.New()
	for _, d := range p.Destinations {
		for _, rel := range rels {
			fi, err := os.Lstat(filepath.Join(e.src, rel))
			if err != nil {
				e.t.Fatal(err)
			}
			mt := fi.ModTime().UnixNano()
			p.Entries = append(p.Entries, model.PlanEntry{PlanDestID: d.ID, SourceRoot: e.src, SourceRel: rel,
				SourcePath: filepath.Join(e.src, rel), DestRelPath: rel, Action: model.ActionCopy, Size: fi.Size(), MtimeNS: mt})
			fmt.Fprintf(h, "%s|%s|%d|%d\n", d.RootPath, rel, fi.Size(), mt)
		}
	}
	p.Fingerprint = hex.EncodeToString(h.Sum(nil))
	ctx := context.Background()
	if err := e.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := ledger.InsertPlan(tx, p); err != nil {
			return err
		}
		return ledger.ApprovePlan(tx, p.ID, p.Fingerprint)
	}); err != nil {
		e.t.Fatal(err)
	}
	return p.ID, p.Fingerprint
}

func (e *env) start(planID, fp string) string {
	e.t.Helper()
	id, err := e.runner.Create(context.Background(), planID, fp, "human")
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) items(id string) []model.TransferItem {
	e.t.Helper()
	items, err := ledger.GetItems(context.Background(), e.db.Read(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return items
}

func (e *env) transfer(id string) *model.Transfer {
	e.t.Helper()
	t, err := ledger.GetTransfer(context.Background(), e.db.Read(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return t
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// noTemps fails if any .ferry-part file exists under root.
func noTemps(t *testing.T, root string) {
	t.Helper()
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, storage.TempSuffix) {
			t.Errorf("leftover temp %s", p)
		}
		return nil
	})
}

// opens counts source opens per relative path.
type opens struct {
	mu sync.Mutex
	n  map[string]int
}

func (o *opens) hook(point string, it model.TransferItem) {
	if point != PointSourceOpened {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.n == nil {
		o.n = map[string]int{}
	}
	o.n[it.Entry.SourceRel]++
}

func (o *opens) get(rel string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.n[rel]
}
