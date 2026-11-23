package receipt

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/model"
)

func sample() *Receipt {
	plan := &model.Plan{ID: "pln_1", Fingerprint: "fp"}
	tr := &model.Transfer{ID: "xfr_1", Actor: "agent:test", Status: model.TransferPartial, ChecksumAlgo: "sha256"}
	targets := []model.TransferTarget{{ID: "tgt_a", Label: "SSD", RootPath: "/d", Status: model.TransferPartial}}
	items := []model.TransferItem{
		{TargetID: "tgt_a", Status: model.ItemVerified, SourceHash: "aa", DestHash: "aa", ChecksumAlgo: "sha256", Entry: model.PlanEntry{DestRelPath: "b.mov", Action: model.ActionCopy, Size: 2}},
		{TargetID: "tgt_a", Status: model.ItemFailed, Error: "ENOSPC", Entry: model.PlanEntry{DestRelPath: "a.mov", Action: model.ActionCopy}},
		{TargetID: "tgt_a", Status: model.ItemExcluded, Entry: model.PlanEntry{DestRelPath: ".DS_Store", Action: model.ActionExclude, ExclusionReason: "system"}},
	}
	return Build("rcp_test", plan, tr, targets, items, nil, time.Unix(0, 0))
}

func TestBuildIsPerFileTruth(t *testing.T) {
	r := sample()
	got := map[string]string{}
	for _, f := range r.Files {
		got[f.DestRelPath] = f.Action + ":" + f.Reason
	}
	if got["b.mov"] != "copied:" || got["a.mov"] != "failed:ENOSPC" || got[".DS_Store"] != "excluded:system" {
		t.Fatalf("%v", got)
	}
	if r.Status != model.TransferPartial || r.Actor != "agent:test" {
		t.Fatalf("%+v", r)
	}
}

func TestDigestDetectsTampering(t *testing.T) {
	r := sample()
	body, _ := r.Marshal()
	if _, err := Parse(body); err != nil {
		t.Fatal(err)
	}
	r.Files[2].DestHash = "bb" // b.mov sorts last
	body, _ = r.Marshal()
	if _, err := Parse(body); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered receipt parsed: %v", err)
	}
	if _, err := Parse([]byte(`{"schema":"ferry-receipt-v2","schema_version":2,"surprise":1}`)); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown field accepted")
	}
}

func TestExportIdempotentAndNoClobber(t *testing.T) {
	dir := t.TempDir()
	r := sample()
	p1, err := Export(dir, r)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Export(dir, r)
	if err != nil || p1 != p2 {
		t.Fatalf("re-export of identical bytes: %v", err)
	}
	if err := os.WriteFile(p1, []byte("someone else's"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(dir, r); err == nil {
		t.Fatal("export overwrote a different file")
	}
	if b, _ := os.ReadFile(p1); string(b) != "someone else's" {
		t.Fatal("clobbered")
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*.ferry-part")); len(m) != 0 {
		t.Fatalf("temp left: %v", m)
	}
}
