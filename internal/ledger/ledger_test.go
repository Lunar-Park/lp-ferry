package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Lunar-Park/lp-ferry/internal/model"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "state", "ferry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenAppliesSchemaAndPragmas(t *testing.T) {
	db := openTest(t)
	var mode string
	if err := db.Read().QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode %q %v", mode, err)
	}
	var fk int
	if err := db.Read().QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys %d %v", fk, err)
	}
	for _, table := range []string{"peers", "saved_destinations", "camera_bindings", "presets", "preset_revisions",
		"inventories", "inventory_entries", "plans", "plan_destinations", "plan_entries", "transfers",
		"transfer_targets", "transfer_items", "path_reservations", "receipts", "verifications"} {
		var n int
		if err := db.Read().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s missing", table)
		}
	}
	fi, err := os.Stat(db.Path())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("db perms %v %v", fi.Mode(), err)
	}
	// Re-open is idempotent.
	path := db.Path()
	_ = db.Close()
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = db2.Close()
}

func TestConcurrentWritersNeverBusy(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				err := db.WriteTx(ctx, func(tx *sql.Tx) error {
					_, err := tx.Exec(`INSERT INTO peers (id, name, pubkey_b64, paired_at) VALUES (?,?,?,?)`,
						fmt.Sprintf("p%d-%d", w, i), "n", "k", Now())
					return err
				})
				if err != nil {
					errs <- err
					return
				}
				var n int
				if err := db.Read().QueryRow(`SELECT COUNT(*) FROM peers`).Scan(&n); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var n int
	_ = db.Read().QueryRow(`SELECT COUNT(*) FROM peers`).Scan(&n)
	if n != 400 {
		t.Fatalf("rows %d", n)
	}
}

func TestWriteTxRollsBack(t *testing.T) {
	db := openTest(t)
	boom := errors.New("boom")
	err := db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO peers (id, name, pubkey_b64, paired_at) VALUES ('x','n','k','t')`); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	var n int
	_ = db.Read().QueryRow(`SELECT COUNT(*) FROM peers`).Scan(&n)
	if n != 0 {
		t.Fatal("rollback did not happen")
	}
}

func TestPlanApprovalAndReservations(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	p := &model.Plan{ID: "pln_1", Fingerprint: "fp1", Actor: "human", ChecksumAlgo: "sha256",
		Destinations: []model.PlanDestination{{ID: "pd_1", RootPath: "/d"}},
		Entries:      []model.PlanEntry{{PlanDestID: "pd_1", SourceRoot: "/s", SourceRel: "a", SourcePath: "/s/a", DestRelPath: "a", Action: "copy", Size: 3}},
	}
	if err := db.WriteTx(ctx, func(tx *sql.Tx) error { return InsertPlan(tx, p) }); err != nil {
		t.Fatal(err)
	}
	if p.Entries[0].ID == 0 {
		t.Fatal("entry id not assigned")
	}
	if err := db.WriteTx(ctx, func(tx *sql.Tx) error { return ApprovePlan(tx, "pln_1", "stale") }); !errors.Is(err, ErrStaleApproval) {
		t.Fatalf("stale fingerprint approved: %v", err)
	}
	if err := db.WriteTx(ctx, func(tx *sql.Tx) error { return ApprovePlan(tx, "pln_1", "fp1") }); err != nil {
		t.Fatal(err)
	}
	got, err := GetPlan(ctx, db.Read(), "pln_1")
	if err != nil || got.Status != model.PlanApproved || len(got.Entries) != 1 || len(got.Destinations) != 1 {
		t.Fatalf("%+v %v", got, err)
	}

	for _, id := range []string{"xfr_a", "xfr_b"} {
		tr := &model.Transfer{ID: id, PlanID: "pln_1", Status: model.TransferRunning, Actor: "human", ChecksumAlgo: "sha256"}
		if err := db.WriteTx(ctx, func(tx *sql.Tx) error { return InsertTransfer(tx, tr, nil, nil) }); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		return ReservePaths(tx, "xfr_a", [][2]string{{"k", "Shoot/Clip.MOV"}, {"k", "Shoot/café.wav"}})
	}); err != nil {
		t.Fatal(err)
	}
	// Case-only and NFD variants collide.
	for _, rel := range []string{"shoot/clip.mov", "Shoot/café.wav"} {
		err := db.WriteTx(ctx, func(tx *sql.Tx) error { return ReservePaths(tx, "xfr_b", [][2]string{{"k", "other"}, {"k", rel}}) })
		var rc *ReservationConflict
		if !errors.As(err, &rc) || rc.HolderID != "xfr_a" {
			t.Fatalf("%s: %v", rel, err)
		}
	}
	// The failed attempt reserved nothing (whole tx rolled back).
	var n int
	_ = db.Read().QueryRow(`SELECT COUNT(*) FROM path_reservations WHERE transfer_id='xfr_b'`).Scan(&n)
	if n != 0 {
		t.Fatalf("partial reservation leaked: %d", n)
	}
	if err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		return ReservePaths(tx, "xfr_b", [][2]string{{"other-dest", "Shoot/Clip.MOV"}})
	}); err != nil {
		t.Fatalf("different destination must not conflict: %v", err)
	}
	if err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := SetTransferStatus(tx, "xfr_a", model.TransferCompleted, "", true); err != nil {
			return err
		}
		n, err := ReleaseDeadReservations(tx, model.TransferRunning)
		if n != 2 {
			return fmt.Errorf("released %d", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
