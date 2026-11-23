package planner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/engine"
	"github.com/Lunar-Park/lp-ferry/internal/ledger"
	mt "github.com/Lunar-Park/lp-ferry/internal/metadata/metadatatest"
	"github.com/Lunar-Park/lp-ferry/internal/model"
	"github.com/Lunar-Park/lp-ferry/internal/preset"
)

type world struct {
	t    *testing.T
	base string
	db   *ledger.DB
	p    *Planner
	run  *engine.Runner
}

func newWorld(t *testing.T) *world {
	t.Helper()
	base := t.TempDir()
	db, err := ledger.Open(filepath.Join(base, "state", "ferry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &world{t: t, base: base, db: db, p: &Planner{DB: db, Presets: &preset.Service{DB: db}},
		run: &engine.Runner{DB: db, ReceiptsDir: filepath.Join(base, "receipts")}}
}

func (w *world) dir(name string) string {
	p := filepath.Join(w.base, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		w.t.Fatal(err)
	}
	return p
}

func (w *world) put(root, rel string, b []byte) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) plan(req model.PlanRequest) *model.Plan {
	w.t.Helper()
	pl, err := w.p.Plan(context.Background(), req)
	if err != nil {
		w.t.Fatal(err)
	}
	return pl
}

func byDest(pl *model.Plan) map[string]model.PlanEntry {
	m := map[string]model.PlanEntry{}
	for _, e := range pl.Entries {
		m[e.SourceLabel+":"+e.SourceRel] = e
	}
	return m
}

func src(path, label string) model.SourceSpec {
	return model.SourceSpec{Location: model.Location{PeerID: "local", Path: path}, Label: label}
}

func dst(path string) model.DestinationSpec {
	return model.DestinationSpec{Location: model.Location{PeerID: "local", Path: path}}
}

func (w *world) execute(pl *model.Plan) *engine.Result {
	w.t.Helper()
	ctx := context.Background()
	if err := w.p.Approve(ctx, pl.ID, pl.Fingerprint); err != nil {
		w.t.Fatal(err)
	}
	id, err := w.run.Create(ctx, pl.ID, pl.Fingerprint, pl.Actor)
	if err != nil {
		w.t.Fatal(err)
	}
	res, err := w.run.Run(ctx, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return res
}

// T2.1 — 10,001 files planned and fully reachable through paginated
// plan.entries and plan.tree, with nothing truncated.
func TestT2_1_TenThousandAndOneFiles(t *testing.T) {
	w := newWorld(t)
	card := w.dir("CARD")
	exts := []string{".mov", ".wav", ".jpg", ".pdf", ".custom"}
	for d := 0; d < 100; d++ {
		for f := 0; f < 100; f++ {
			w.put(card, fmt.Sprintf("D%03d/f%03d%s", d, f, exts[(d+f)%len(exts)]), nil)
		}
	}
	w.put(card, "lonely.txt", []byte("x"))
	pl := w.plan(model.PlanRequest{Sources: []model.SourceSpec{src(card, "Drive-A")},
		Destinations: []model.DestinationSpec{dst(w.dir("SSD"))}, PresetID: preset.BuiltinCategory})
	if len(pl.Blocking()) != 0 {
		t.Fatalf("%+v", pl.Blocking())
	}
	ctx := context.Background()
	seen := map[int64]bool{}
	cursor := ""
	pages := 0
	for {
		page, err := w.p.Entries(ctx, pl.ID, "", cursor, 1000)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if page.Total != 10001 {
			t.Fatalf("total %d", page.Total)
		}
		for _, e := range page.Entries {
			seen[e.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 10001 || pages != 11 {
		t.Fatalf("entries reachable: %d in %d pages", len(seen), pages)
	}
	if page, _ := w.p.Entries(ctx, pl.ID, "", "", 0); len(page.Entries) != DefaultPageSize {
		t.Fatalf("default page %d", len(page.Entries))
	}
	if page, _ := w.p.Entries(ctx, pl.ID, "", "", 50000); len(page.Entries) != MaxPageSize {
		t.Fatal("page size must cap at 1000")
	}

	// Walk the whole tree through paged windows, counting files.
	var files int64
	type pending struct{ path, cursor string }
	queue := []pending{{"", ""}}
	for len(queue) > 0 {
		q := queue[0]
		queue = queue[1:]
		n, err := w.p.Tree(ctx, TreeQuery{PlanID: pl.ID, DestID: pl.Destinations[0].ID, ParentPath: q.path, Depth: 1, Limit: 37, Cursor: q.cursor})
		if err != nil {
			t.Fatal(err)
		}
		if q.path == "" && q.cursor == "" && n.FileCount != 10001 {
			t.Fatalf("root file_count %d", n.FileCount)
		}
		for _, c := range n.Children {
			if c.Kind == "file" {
				files++
			} else {
				queue = append(queue, pending{c.Path, ""})
			}
		}
		if n.NextCursor != "" {
			queue = append(queue, pending{q.path, n.NextCursor})
		}
	}
	if files != 10001 {
		t.Fatalf("tree walk reached %d files", files)
	}
}

func sonyCard(w *world, root string) {
	w.put(root, "PRIVATE/M4ROOT/CLIP/C0001.MP4", mt.MP4{Width: 3840, Height: 2160, MdatBytes: 300 << 10}.Bytes())
	w.put(root, "PRIVATE/M4ROOT/CLIP/C0001M01.XML", mt.SonyXML("Sony", "ILME-FX3", "12345", "2026-11-22T10:00:00+09:00", 3840, 2160))
	w.put(root, "PRIVATE/M4ROOT/CLIP/C0002.MP4", mt.MP4{Width: 3840, Height: 2160, MdatBytes: 200 << 10}.Bytes())
	w.put(root, "PRIVATE/M4ROOT/CLIP/C0002M01.XML", mt.SonyXML("Sony", "ILME-FX3", "12345", "2026-11-22T11:30:00+09:00", 3840, 2160))
	w.put(root, "PRIVATE/M4ROOT/MEDIAPRO.XML", []byte("<MediaProfile/>"))
}

// T2.2 — Sony card + ACAM binding + conform template → deterministic,
// verified output and a matching plan.tree.
func TestT2_2_CameraCardConform(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	card := w.dir("FX3_CARD")
	sonyCard(w, card)
	if err := w.p.BindCamera(ctx, "Sony", "ILME-FX3", "12345", "ACAM"); err != nil {
		t.Fatal(err)
	}
	ssd := w.dir("SSD")
	req := model.PlanRequest{Sources: []model.SourceSpec{src(card, "A-cam")}, Destinations: []model.DestinationSpec{dst(ssd)},
		PresetID: preset.BuiltinCamera}
	pl := w.plan(req)
	got := byDest(pl)
	want := map[string]string{
		"A-cam:PRIVATE/M4ROOT/CLIP/C0001.MP4":    "ACAM/2026-11-22/ACAM_C0001.MP4",
		"A-cam:PRIVATE/M4ROOT/CLIP/C0001M01.XML": "ACAM/2026-11-22/ACAM_C0001M01.XML",
		"A-cam:PRIVATE/M4ROOT/CLIP/C0002.MP4":    "ACAM/2026-11-22/ACAM_C0002.MP4",
		"A-cam:PRIVATE/M4ROOT/MEDIAPRO.XML":      "Card/A-cam/PRIVATE/M4ROOT/MEDIAPRO.XML",
	}
	for k, v := range want {
		if got[k].DestRelPath != v {
			t.Errorf("%s -> %q, want %q (%+v)", k, got[k].DestRelPath, v, got[k])
		}
	}
	if got["A-cam:PRIVATE/M4ROOT/CLIP/C0001.MP4"].CameraRole != "ACAM" {
		t.Fatal("camera role not recorded on the entry")
	}
	if again := w.plan(req); again.Fingerprint != pl.Fingerprint {
		t.Fatal("same inputs, different fingerprint")
	}
	tree, err := w.p.Tree(ctx, TreeQuery{PlanID: pl.ID, DestID: pl.Destinations[0].ID, Depth: 2, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var acam *TreeNode
	for i := range tree.Children {
		if tree.Children[i].Name == "ACAM" {
			acam = &tree.Children[i]
		}
	}
	if acam == nil || acam.FileCount != 4 || acam.RenamedCount != 4 || len(acam.Children) != 1 || acam.Children[0].Name != "2026-11-22" {
		t.Fatalf("tree: %+v", tree)
	}
	day := acam.Children[0]
	if len(day.SampleTransforms) == 0 || day.SampleTransforms[0].Before != "A-cam:PRIVATE/M4ROOT/CLIP/C0001.MP4" ||
		day.SampleTransforms[0].After != "ACAM/2026-11-22/ACAM_C0001.MP4" {
		t.Fatalf("samples: %+v", day.SampleTransforms)
	}
	res := w.execute(pl)
	if res.Status != model.TransferCompleted {
		t.Fatalf("%+v", res)
	}
	b, err := os.ReadFile(filepath.Join(ssd, "ACAM", "2026-11-22", "ACAM_C0001M01.XML"))
	if err != nil || !strings.Contains(string(b), "12345") {
		t.Fatalf("sidecar not delivered with its clip: %v", err)
	}
}

// T2.3 — missing capture date never fabricates one.
func TestT2_3_MissingCaptureDate(t *testing.T) {
	w := newWorld(t)
	card := w.dir("CARD")
	w.put(card, "CLIP/NODATE.MP4", mt.MP4{Width: 1920, Height: 1080, MdatBytes: 1000}.Bytes())
	pl := w.plan(model.PlanRequest{Sources: []model.SourceSpec{src(card, "B-cam")}, Destinations: []model.DestinationSpec{dst(w.dir("SSD"))},
		PresetID: preset.BuiltinCamera, CameraRoles: map[string]string{"B-cam": "BCAM"}})
	e := byDest(pl)["B-cam:CLIP/NODATE.MP4"]
	today := time.Now().Format("2006-01-02")
	if e.DestRelPath != "Card/B-cam/CLIP/NODATE.MP4" || e.MatchedRule != "fallback" || strings.Contains(e.DestRelPath, today) {
		t.Fatalf("%+v", e)
	}
	var warned bool
	for _, f := range pl.Findings {
		if f.Severity == model.SeverityWarning && strings.Contains(f.Message, "capture_date") && strings.Contains(f.SourceRel, "NODATE") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no explicit reason recorded: %+v", pl.Findings)
	}
}

// T2.4 — a keep-together group that collides moves whole, internal paths
// untouched; under needs_review it blocks whole.
func TestT2_4_GroupRootCollision(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	card := w.dir("CARD")
	for _, r := range []string{"Proj/clip.mov", "Proj/audio/take1.wav", "Proj/project.drp", "loose.mov"} {
		w.put(card, r, []byte(r))
	}
	if err := os.MkdirAll(filepath.Join(card, "Proj", "Empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	ssd := w.dir("SSD")
	w.put(ssd, "Collections/card/Proj/clip.mov", []byte("already here"))
	p := *preset.Builtins()[preset.BuiltinCategory]
	p.Groups = []preset.Group{{ID: "proj", When: preset.Conditions{PathGlob: []string{"Proj"}}, Template: "Collections/{source_label}/{relative_dir}/{filename}"}}
	rev, err := w.p.Presets.Create(ctx, &p)
	if err != nil {
		t.Fatal(err)
	}
	pl := w.plan(model.PlanRequest{Sources: []model.SourceSpec{src(card, "card")}, Destinations: []model.DestinationSpec{dst(ssd)}, PresetID: rev.PresetID})
	got := byDest(pl)
	for rel, want := range map[string]string{
		"card:Proj/clip.mov":        "Collections/card/Proj (2)/clip.mov",
		"card:Proj/audio/take1.wav": "Collections/card/Proj (2)/audio/take1.wav",
		"card:Proj/project.drp":     "Collections/card/Proj (2)/project.drp",
		"card:Proj/Empty":           "Collections/card/Proj (2)/Empty",
		"card:loose.mov":            "Video/card/loose.mov",
	} {
		if got[rel].DestRelPath != want {
			t.Errorf("%s -> %s, want %s", rel, got[rel].DestRelPath, want)
		}
	}
	for _, e := range pl.Entries {
		if strings.Contains(e.DestRelPath, "clip (2)") {
			t.Fatal("a group member was suffixed individually")
		}
	}
	res := w.execute(pl)
	if res.Status != model.TransferCompleted {
		t.Fatalf("%+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(ssd, "Collections/card/Proj/clip.mov")); string(b) != "already here" {
		t.Fatal("existing group tree touched")
	}

	pl = w.plan(model.PlanRequest{Sources: []model.SourceSpec{src(card, "card")}, Destinations: []model.DestinationSpec{dst(w.dir("SSD2"))},
		PresetID: rev.PresetID, ConflictPolicy: preset.NeedsReview})
	if len(pl.Blocking()) != 0 {
		t.Fatal("no collision on a fresh destination")
	}
	w.put(filepath.Join(w.base, "SSD3"), "Collections/card/Proj/x", nil)
	pl = w.plan(model.PlanRequest{Sources: []model.SourceSpec{src(card, "card")}, Destinations: []model.DestinationSpec{dst(filepath.Join(w.base, "SSD3"))},
		PresetID: rev.PresetID, ConflictPolicy: preset.NeedsReview})
	blocked := 0
	for _, e := range pl.Entries {
		if e.GroupID != "" && e.Action == model.ActionNeedsReview {
			blocked++
		}
	}
	if blocked != 4 || w.p.Approve(ctx, pl.ID, pl.Fingerprint) == nil {
		t.Fatalf("whole group must block under needs_review: %d blocked", blocked)
	}
}

// T2.5 — Unicode, case-only, same-size/different-bytes and duplicate
// targets are each caught deterministically; skip_identical needs a full
// checksum.
func TestT2_5_ConflictMatrix(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	a, b := w.dir("A"), w.dir("B")
	w.put(a, "café.wav", []byte("nfc"))
	w.put(b, "café.wav", []byte("nfd"))
	w.put(a, "Clip.MOV", []byte("upper"))
	w.put(b, "clip.mov", []byte("lower"))
	w.put(a, "dup.mov", []byte("from a"))
	w.put(b, "dup.mov", []byte("from b"))
	w.put(a, "same.bin", []byte("AAAA"))
	w.put(a, "ident.bin", []byte("identical bytes"))
	ssd := w.dir("SSD")
	w.put(ssd, "Flat/same.bin", []byte("BBBB")) // same size, different bytes
	w.put(ssd, "Flat/ident.bin", []byte("identical bytes"))
	flat := &preset.Preset{Schema: preset.Schema, Name: "flat", ConflictPolicy: preset.SkipIdentical,
		Rules: []preset.Rule{{ID: "flat", Template: "Flat/{filename}"}}, Fallback: "Flat/{filename}"}
	rev, err := w.p.Presets.Create(ctx, flat)
	if err != nil {
		t.Fatal(err)
	}
	req := model.PlanRequest{Sources: []model.SourceSpec{src(a, "A"), src(b, "B")}, Destinations: []model.DestinationSpec{dst(ssd)}, PresetID: rev.PresetID}
	pl := w.plan(req)
	got := byDest(pl)
	check := func(k, dest, kind, action string) {
		t.Helper()
		e := got[k]
		if e.DestRelPath != dest || e.ConflictKind != kind || e.Action != action {
			t.Errorf("%s: %s / %s / %s; want %s / %s / %s", k, e.DestRelPath, e.ConflictKind, e.Action, dest, kind, action)
		}
	}
	check("A:café.wav", "Flat/café.wav", "", model.ActionCopy)
	check("B:café.wav", "Flat/café (2).wav", ConflictUnicode, model.ActionCopy)
	check("A:Clip.MOV", "Flat/Clip.MOV", "", model.ActionCopy)
	check("B:clip.mov", "Flat/clip (2).mov", ConflictCaseOnly, model.ActionCopy)
	check("A:dup.mov", "Flat/dup.mov", "", model.ActionCopy)
	check("B:dup.mov", "Flat/dup (2).mov", ConflictDuplicateTarget, model.ActionCopy)
	check("A:same.bin", "Flat/same (2).bin", ConflictDifferentContent, model.ActionCopy)
	check("A:ident.bin", "Flat/ident.bin", ConflictExistingIdentical, model.ActionSkipIdentical)
	if got["A:ident.bin"].ExpectedHash == "" {
		t.Fatal("skip_identical without checksum evidence")
	}
	if again := w.plan(req); again.Fingerprint != pl.Fingerprint {
		t.Fatal("conflict resolution is not deterministic")
	}
	res := w.execute(pl)
	if res.Status != model.TransferCompleted {
		t.Fatalf("%+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(ssd, "Flat", "same.bin")); string(b) != "BBBB" {
		t.Fatal("existing different-content file was overwritten")
	}
	items, _ := ledger.GetItems(ctx, w.db.Read(), res.TransferID)
	for _, it := range items {
		if it.Entry.SourceRel == "ident.bin" && it.Status != model.ItemSkippedIdentical {
			t.Fatalf("%+v", it)
		}
	}

	// Ancestor conflict: an existing file where a folder is needed blocks.
	w.put(ssd, "Deep", []byte("a file"))
	deep := &preset.Preset{Schema: preset.Schema, Name: "deep", ConflictPolicy: preset.KeepBoth,
		Rules: []preset.Rule{{ID: "d", Template: "Deep/{filename}"}}, Fallback: "x/{filename}"}
	rev2, _ := w.p.Presets.Create(ctx, deep)
	pl = w.plan(model.PlanRequest{Sources: []model.SourceSpec{src(a, "A")}, Destinations: []model.DestinationSpec{dst(ssd)}, PresetID: rev2.PresetID})
	if !errors.Is(w.p.Approve(ctx, pl.ID, pl.Fingerprint), ErrBlocked) {
		t.Fatal("ancestor-is-file conflict not blocking")
	}
}

func TestSymlinksAndScanErrorsBlock(t *testing.T) {
	w := newWorld(t)
	card := w.dir("CARD")
	w.put(card, "ok.mov", []byte("x"))
	if err := os.Symlink("/etc/hosts", filepath.Join(card, "link.mov")); err != nil {
		t.Fatal(err)
	}
	w.put(card, ".DS_Store", []byte("junk"))
	pl := w.plan(model.PlanRequest{Sources: []model.SourceSpec{src(card, "c")}, Destinations: []model.DestinationSpec{dst(w.dir("SSD"))}})
	got := byDest(pl)
	if got["c:link.mov"].Action != model.ActionNeedsReview || got["c:.DS_Store"].ExclusionReason != "system artifact" {
		t.Fatalf("%+v", got)
	}
	ctx := context.Background()
	if !errors.Is(w.p.Approve(ctx, pl.ID, pl.Fingerprint), ErrBlocked) {
		t.Fatal("symlink did not block approval")
	}
	next, err := w.p.Revise(ctx, pl.ID, Revision{Exclude: []model.UserExclusion{{SourceLabel: "c", Rel: "link.mov", Reason: "not needed"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.p.Approve(ctx, next.ID, next.Fingerprint); err != nil {
		t.Fatalf("explicit exclusion should unblock: %v", err)
	}
	if old, _ := ledger.GetPlan(ctx, w.db.Read(), pl.ID); old.Status != model.PlanInvalidated || old.SupersededBy != next.ID {
		t.Fatalf("%+v", old)
	}
	if _, err := w.p.Plan(ctx, model.PlanRequest{Sources: []model.SourceSpec{src(card, "c")},
		Destinations: []model.DestinationSpec{dst(filepath.Join(card, "inside"))}}); !errors.Is(err, errSourceOverlap) {
		t.Fatalf("destination inside source accepted: %v", err)
	}
}

// T2.6 — editing a preset, destination, camera binding or camera role
// invalidates pending approval in the same transaction; the stale
// fingerprint is refused at transfer start.
func TestT2_6_EditsInvalidateApproval(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	card := w.dir("CARD")
	sonyCard(w, card)
	if err := w.p.BindCamera(ctx, "Sony", "ILME-FX3", "12345", "ACAM"); err != nil {
		t.Fatal(err)
	}
	approved := func(req model.PlanRequest) *model.Plan {
		t.Helper()
		pl := w.plan(req)
		if err := w.p.Approve(ctx, pl.ID, pl.Fingerprint); err != nil {
			t.Fatal(err)
		}
		return pl
	}
	stale := func(pl *model.Plan, why string) {
		t.Helper()
		got, _ := ledger.GetPlan(ctx, w.db.Read(), pl.ID)
		if got.Status != model.PlanInvalidated {
			t.Fatalf("%s: plan is %s", why, got.Status)
		}
		if _, err := w.run.Create(ctx, pl.ID, pl.Fingerprint, "human"); !errors.Is(err, ledger.ErrStaleApproval) {
			t.Fatalf("%s: stale fingerprint accepted: %v", why, err)
		}
	}

	// Preset edit.
	custom, _ := w.p.Presets.Create(ctx, preset.Builtins()[preset.BuiltinCamera])
	pl := approved(model.PlanRequest{Sources: []model.SourceSpec{src(card, "A")}, Destinations: []model.DestinationSpec{dst(w.dir("S1"))}, PresetID: custom.PresetID})
	edited := *custom.Preset
	edited.Fallback = "Other/{source_label}/{relative_dir}/{filename}"
	if _, err := w.p.Presets.Update(ctx, custom.PresetID, &edited); err != nil {
		t.Fatal(err)
	}
	stale(pl, "preset edit")

	// Destination edit.
	saved, err := w.p.SaveDestination(ctx, SaveDestinationRequest{Name: "Shuttle", Path: w.dir("S2"), PresetID: preset.BuiltinCamera})
	if err != nil {
		t.Fatal(err)
	}
	pl = approved(model.PlanRequest{Sources: []model.SourceSpec{src(card, "A")},
		Destinations: []model.DestinationSpec{{Location: model.Location{DestinationID: saved.ID}, ConfirmBinding: true}}})
	if pl.Destinations[0].PresetID != preset.BuiltinCamera {
		t.Fatal("saved destination's pinned preset not used")
	}
	if _, err := w.p.SaveDestination(ctx, SaveDestinationRequest{ID: saved.ID, Name: "Shuttle (renamed)", Path: saved.RootPath, PresetID: preset.BuiltinCamera}); err != nil {
		t.Fatal(err)
	}
	stale(pl, "destination edit")

	// Camera binding edit.
	pl = approved(model.PlanRequest{Sources: []model.SourceSpec{src(card, "A")}, Destinations: []model.DestinationSpec{dst(w.dir("S3"))}, PresetID: preset.BuiltinCamera})
	if err := w.p.BindCamera(ctx, "Sony", "ILME-FX3", "12345", "BCAM"); err != nil {
		t.Fatal(err)
	}
	stale(pl, "camera binding edit")

	// Camera role override after preview.
	pl = approved(model.PlanRequest{Sources: []model.SourceSpec{src(card, "A")}, Destinations: []model.DestinationSpec{dst(w.dir("S4"))}, PresetID: preset.BuiltinCamera})
	next, err := w.p.Revise(ctx, pl.ID, Revision{CameraRoles: map[string]string{"A": "CCAM"}})
	if err != nil {
		t.Fatal(err)
	}
	stale(pl, "camera role override")
	if byDest(next)["A:PRIVATE/M4ROOT/CLIP/C0001.MP4"].DestRelPath != "CCAM/2026-11-22/CCAM_C0001.MP4" {
		t.Fatal("role override not applied")
	}

	// Source changed after preview: approval refuses and invalidates.
	pl = w.plan(model.PlanRequest{Sources: []model.SourceSpec{src(card, "A")}, Destinations: []model.DestinationSpec{dst(w.dir("S5"))}})
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(filepath.Join(card, "PRIVATE/M4ROOT/MEDIAPRO.XML"), future, future)
	if err := w.p.Approve(ctx, pl.ID, pl.Fingerprint); !errors.Is(err, ledger.ErrStaleApproval) {
		t.Fatalf("changed source approved: %v", err)
	}
	stale(pl, "source changed")

	// An approval is single-use.
	pl = approved(model.PlanRequest{Sources: []model.SourceSpec{src(card, "A")}, Destinations: []model.DestinationSpec{dst(w.dir("S6"))}})
	if _, err := w.run.Create(ctx, pl.ID, pl.Fingerprint, "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.run.Create(ctx, pl.ID, pl.Fingerprint, "human"); !errors.Is(err, ledger.ErrStaleApproval) {
		t.Fatalf("approval reused: %v", err)
	}
}
