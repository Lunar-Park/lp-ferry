package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func mustRoot(t *testing.T, path string) *Root {
	t.Helper()
	r, err := OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestValidateRel(t *testing.T) {
	good := map[string][]string{
		"a/b.mov":    {"a", "b.mov"},
		"./a//b.mov": {"a", "b.mov"},
		"clip.MP4":   {"clip.MP4"},
		"a b/ü.wav":  {"a b", "ü.wav"},
	}
	for in, want := range good {
		got, err := ValidateRel(in)
		if err != nil || len(got) != len(want) {
			t.Fatalf("%q: %v %v", in, got, err)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%q: %v", in, got)
			}
		}
	}
	for _, bad := range []string{"", "/abs", `\unc`, "C:x", "a/../b", "..", "a\x00b", `a\b`, ".", "x/y.ferry-part"} {
		if _, err := ValidateRel(bad); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("%q accepted: %v", bad, err)
		}
	}
}

// T1.3 — a symlink at the leaf or at any ancestor is refused via lstat and
// nothing is written anywhere.
func TestT1_3_SymlinkLeafAndAncestorBlocked(t *testing.T) {
	base := t.TempDir()
	dest := filepath.Join(base, "dest")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{dest, outside} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Ancestor: dest/shoot -> ../outside
	if err := os.Symlink(outside, filepath.Join(dest, "shoot")); err != nil {
		t.Fatal(err)
	}
	// Deeper ancestor: dest/real/day1 -> outside
	if err := os.MkdirAll(filepath.Join(dest, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dest, "real", "day1")); err != nil {
		t.Fatal(err)
	}
	// Leaf: dest/leaf.mov -> outside/victim.mov
	victim := filepath.Join(outside, "victim.mov")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dest, "leaf.mov")); err != nil {
		t.Fatal(err)
	}
	root := mustRoot(t, dest)

	for _, rel := range []string{"shoot/a.mov", "shoot/new/deeper/a.mov", "real/day1/a.mov"} {
		d, _, err := OpenFileIn(root, rel, true)
		if !errors.Is(err, ErrUnsafePath) {
			if d != nil {
				_ = d.Close()
			}
			t.Fatalf("%s: want ErrUnsafePath, got %v", rel, err)
		}
	}
	d, leaf, err := OpenFileIn(root, "leaf.mov", true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.CheckFree(leaf); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("leaf symlink: %v", err)
	}
	if _, err := d.OpenRead(leaf); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("reading through a leaf symlink: %v", err)
	}
	if _, err := d.CreateExcl(leaf); err == nil {
		t.Fatal("create over symlink succeeded")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 1 {
		t.Fatalf("outside was written to: %v", entries)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatal("victim modified")
	}
}

func TestOpenDirCreatesAndRefusesFileAncestor(t *testing.T) {
	dest := t.TempDir()
	root := mustRoot(t, dest)
	d, leaf, err := OpenFileIn(root, "A/B/c.mov", true)
	if err != nil {
		t.Fatal(err)
	}
	if leaf != "c.mov" || d.DirPath() != filepath.Join(root.Real, "A", "B") {
		t.Fatalf("%s %s", d.DirPath(), leaf)
	}
	_ = d.Close()
	if err := os.WriteFile(filepath.Join(dest, "F"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenFileIn(root, "F/x.mov", true); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("file ancestor: %v", err)
	}
	if _, _, err := OpenFileIn(root, "missing/x.mov", false); err == nil {
		t.Fatal("non-create open of a missing dir succeeded")
	}
}

func writePart(t *testing.T, d *Dir, name, body string) {
	t.Helper()
	f, err := d.CreateExcl(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// T1.2 — a file created at the destination after planning is never
// overwritten, under every publish strategy.
func TestT1_2_PublishNeverClobbers(t *testing.T) {
	for _, s := range []Strategy{StrategyHardlink, StrategyRenameNoReplace, StrategyReserveRename} {
		t.Run(string(s), func(t *testing.T) {
			dest := t.TempDir()
			root := mustRoot(t, dest)
			d, err := OpenDir(root, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()

			// Clean publish works.
			var res *Reservation
			if s == StrategyReserveRename {
				if res, err = Reserve(d, "ok.mov"); err != nil {
					t.Fatal(err)
				}
			}
			writePart(t, d, "ok.mov"+TempSuffix, "verified")
			if err := Publish(d, "ok.mov"+TempSuffix, "ok.mov", s, res); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(filepath.Join(dest, "ok.mov")); string(b) != "verified" {
				t.Fatalf("published %q", b)
			}
			if _, err := os.Lstat(filepath.Join(dest, "ok.mov"+TempSuffix)); !os.IsNotExist(err) {
				t.Fatal("temp left behind after publish")
			}

			// Race: someone else writes the final name after planning.
			final := filepath.Join(dest, "race.mov")
			if s == StrategyReserveRename {
				// The reservation itself is the guard: it fails up front.
				if err := os.WriteFile(final, []byte("theirs"), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := Reserve(d, "race.mov"); !errors.Is(err, ErrDestinationExists) {
					t.Fatalf("reserve over existing: %v", err)
				}
				// And a reservation swapped out from under us is detected.
				_ = os.Remove(final)
				r2, err := Reserve(d, "race.mov")
				if err != nil {
					t.Fatal(err)
				}
				_ = os.Remove(final)
				if err := os.WriteFile(final, []byte("theirs"), 0o644); err != nil {
					t.Fatal(err)
				}
				writePart(t, d, "race.mov"+TempSuffix, "mine")
				if err := Publish(d, "race.mov"+TempSuffix, "race.mov", s, r2); !errors.Is(err, ErrDestinationExists) {
					t.Fatalf("publish over swapped reservation: %v", err)
				}
			} else {
				writePart(t, d, "race.mov"+TempSuffix, "mine")
				if err := os.WriteFile(final, []byte("theirs"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := Publish(d, "race.mov"+TempSuffix, "race.mov", s, nil); !errors.Is(err, ErrDestinationExists) {
					t.Fatalf("want ErrDestinationExists, got %v", err)
				}
			}
			if b, _ := os.ReadFile(final); string(b) != "theirs" {
				t.Fatalf("existing file changed to %q", b)
			}
		})
	}
}

func TestProbeStrategyLeavesNothing(t *testing.T) {
	dest := t.TempDir()
	d, err := OpenDir(mustRoot(t, dest), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s, err := ProbeStrategy(d)
	if err != nil {
		t.Fatal(err)
	}
	if s != StrategyHardlink {
		t.Fatalf("local temp filesystem should support hard links, got %s", s)
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Fatalf("probe left %v", entries)
	}
}

func TestProbeStrategyReadOnlyRootFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dest := t.TempDir()
	if err := os.Chmod(dest, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dest, 0o755) })
	d, err := OpenDir(mustRoot(t, dest), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := ProbeStrategy(d); err == nil {
		t.Fatal("probe on a read-only root must fail loudly")
	}
}

// T1.4 (storage half) — the root path surviving while the directory behind
// it changes is a binding change.
func TestT1_4_BindingChangeDetected(t *testing.T) {
	base := t.TempDir()
	dest := filepath.Join(base, "NAS")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	root := mustRoot(t, dest)
	d, err := OpenDir(root, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.CheckBinding(); err != nil {
		t.Fatal(err)
	}
	// The "share" goes away and its mount directory is left behind.
	if err := os.Rename(dest, filepath.Join(base, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := d.CheckBinding(); !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("want ErrBindingChanged, got %v", err)
	}
	if _, err := OpenDir(root, []string{"x"}, true); !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("OpenDir after swap: %v", err)
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Fatalf("wrote into the leftover mount dir: %v", entries)
	}
}

func TestReadbackMode(t *testing.T) {
	dest := t.TempDir()
	d, err := OpenDir(mustRoot(t, dest), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	writePart(t, d, "f", "abc")
	f, mode, err := d.OpenReadback("f")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := map[string]string{"darwin": "f_nocache", "linux": "fadvise_dontneed"}[runtime.GOOS]
	if mode != want {
		t.Fatalf("readback mode %q, want %q", mode, want)
	}
	b, _ := io.ReadAll(f)
	if string(b) != "abc" {
		t.Fatalf("%q", b)
	}
}

func TestChtimesAndFstat(t *testing.T) {
	dest := t.TempDir()
	d, err := OpenDir(mustRoot(t, dest), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	writePart(t, d, "f", "abc")
	const mt = int64(1_700_000_000_123_456_789)
	if err := d.Chtimes("f", mt, mt); err != nil {
		t.Fatal(err)
	}
	fi, err := d.Lstat("f")
	if err != nil {
		t.Fatal(err)
	}
	if fi.MtimeNS/1000 != mt/1000 || fi.Size != 3 || !fi.IsRegular() {
		t.Fatalf("%+v", fi)
	}
}
