package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Lunar-Park/lp-ferry/internal/engine"
	"github.com/Lunar-Park/lp-ferry/internal/ledger"
	"github.com/Lunar-Park/lp-ferry/internal/model"
	"github.com/Lunar-Park/lp-ferry/internal/verify"
)

// transferForVerify runs a real verified transfer and returns the exported
// receipt path and the destination root.
func transferForVerify(t *testing.T) (string, string, *ledger.DB) {
	t.Helper()
	base := t.TempDir()
	src := filepath.Join(base, "CARD")
	dest := filepath.Join(base, "SSD")
	files := map[string]int{"CLIP/A001.MOV": 3<<20 + 11, "CLIP/A002.MOV": 4096, "AUDIO/T01.WAV": 777}
	for rel, n := range files {
		p := filepath.Join(src, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		b := make([]byte, n)
		_, _ = rand.Read(b)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := ledger.Open(filepath.Join(base, "ferry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	p := &model.Plan{ID: "pln_verify", Fingerprint: "fp", Actor: "human", ChecksumAlgo: "sha256",
		Destinations: []model.PlanDestination{{ID: "pd_ssd", Label: "SSD", RootPath: dest, PeerID: "local"}}}
	for rel := range files {
		fi, _ := os.Stat(filepath.Join(src, rel))
		p.Entries = append(p.Entries, model.PlanEntry{PlanDestID: "pd_ssd", SourceRoot: src, SourceRel: rel,
			SourcePath: filepath.Join(src, rel), DestRelPath: rel, Action: model.ActionCopy, Size: fi.Size(), MtimeNS: fi.ModTime().UnixNano()})
	}
	ctx := context.Background()
	if err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := ledger.InsertPlan(tx, p); err != nil {
			return err
		}
		return ledger.ApprovePlan(tx, p.ID, p.Fingerprint)
	}); err != nil {
		t.Fatal(err)
	}
	r := &engine.Runner{DB: db, ReceiptsDir: filepath.Join(base, "receipts")}
	id, err := r.Create(ctx, p.ID, p.Fingerprint, "human")
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(ctx, id)
	if err != nil || res.Status != model.TransferCompleted || res.ReceiptPath == "" {
		t.Fatalf("%+v %v", res, err)
	}
	return res.ReceiptPath, dest, db
}

func ferry(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// T1.8 — readback is non-buffered and `ferry verify` catches a 1-byte
// bitflip, a missing file and an added file with exit codes 0/1/2/3/4.
func TestT1_8_VerifyExitCodes(t *testing.T) {
	rcpt, dest, db := transferForVerify(t)

	code, out, errOut := ferry(t, "verify", "--receipt", rcpt, "--json")
	if code != 0 {
		t.Fatalf("clean verify exited %d: %s %s", code, out, errOut)
	}
	var rep verify.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"darwin": "f_nocache", "linux": "fadvise_dontneed"}[runtime.GOOS]
	if rep.ReadbackMode != want || rep.Checked != 3 || !rep.Clean {
		t.Fatalf("%+v", rep)
	}
	if err := verify.Record(context.Background(), db, &rep); err != nil {
		t.Fatal(err)
	}

	// 3: a new file in a folder the transfer wrote. Dot-files are ignored.
	_ = os.WriteFile(filepath.Join(dest, "CLIP", ".DS_Store"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dest, "CLIP", "A003.MOV"), []byte("new"), 0o644)
	if code, out, _ := ferry(t, "verify", "--receipt", rcpt); code != verify.ExitAdded || !strings.Contains(out, "added     CLIP/A003.MOV") {
		t.Fatalf("added: exit %d\n%s", code, out)
	}

	// 2: a single flipped bit in the middle of a 3 MiB clip outranks added.
	clip := filepath.Join(dest, "CLIP", "A001.MOV")
	f, err := os.OpenFile(clip, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 1)
	_, _ = f.ReadAt(b, 1<<20)
	b[0] ^= 0x01
	_, _ = f.WriteAt(b, 1<<20)
	_ = f.Close()
	if code, out, _ := ferry(t, "verify", "--receipt", rcpt); code != verify.ExitModified || !strings.Contains(out, "modified  CLIP/A001.MOV") {
		t.Fatalf("bitflip: exit %d\n%s", code, out)
	}

	// 1: a missing file outranks modified and added.
	if err := os.Remove(filepath.Join(dest, "AUDIO", "T01.WAV")); err != nil {
		t.Fatal(err)
	}
	code, out, _ = ferry(t, "verify", "--receipt", rcpt, "--json")
	if code != verify.ExitMissing {
		t.Fatalf("missing: exit %d\n%s", code, out)
	}
	_ = json.Unmarshal([]byte(out), &rep)
	if rep.Missing != 1 || rep.Modified != 1 || rep.Added != 1 {
		t.Fatalf("full counts must be reported: %+v", rep)
	}

	// 4: an unreadable file means the check is incomplete.
	if os.Geteuid() != 0 {
		if err := os.Chmod(filepath.Join(dest, "CLIP", "A002.MOV"), 0o000); err != nil {
			t.Fatal(err)
		}
		if code, _, _ := ferry(t, "verify", "--receipt", rcpt); code != verify.ExitUnreadable {
			t.Fatalf("unreadable: exit %d", code)
		}
		_ = os.Chmod(filepath.Join(dest, "CLIP", "A002.MOV"), 0o644)
	}

	// 4: an edited receipt fails its digest.
	body, _ := os.ReadFile(rcpt)
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	raw["files"].([]any)[0].(map[string]any)["dest_hash"] = strings.Repeat("0", 64)
	edited, _ := json.Marshal(raw)
	tampered := filepath.Join(t.TempDir(), "tampered.json")
	_ = os.WriteFile(tampered, edited, 0o600)
	if code, _, errOut := ferry(t, "verify", "--receipt", tampered); code != verify.ExitUnreadable || !strings.Contains(errOut, "digest mismatch") {
		t.Fatalf("tampered receipt: exit %d %s", code, errOut)
	}

	// 4: an unreachable root.
	if code, _, _ := ferry(t, "verify", "--receipt", rcpt, "--root", filepath.Join(dest, "nope")); code != verify.ExitUnreadable {
		t.Fatalf("unreachable root: exit %d", code)
	}
	var n int
	_ = db.Read().QueryRow(`SELECT COUNT(*) FROM verifications`).Scan(&n)
	if n != 1 {
		t.Fatalf("verification not recorded: %d", n)
	}
}

func TestVersionJSON(t *testing.T) {
	code, out, _ := ferry(t, "version", "--json")
	if code != 0 || !strings.Contains(out, `"version"`) {
		t.Fatalf("%d %s", code, out)
	}
}
