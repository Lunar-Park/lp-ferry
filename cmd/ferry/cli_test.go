package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/config"
	"github.com/Lunar-Park/lp-ferry/internal/daemon"
)

// liveDaemon starts a real daemon on a real socket; the CLI is then run
// exactly as a user would, with an explicit (empty) config file.
func liveDaemon(t *testing.T) (sock, cfgPath, base string) {
	t.Helper()
	base = t.TempDir()
	sockDir, err := os.MkdirTemp("/tmp", "fy")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock = filepath.Join(sockDir, "ferry.sock")
	cfgPath = filepath.Join(base, "ferry.toml")
	if err := os.WriteFile(cfgPath, []byte("[daemon]\nchecksum_algo = \"sha256\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := config.Paths{Home: base, Socket: sock, DB: filepath.Join(base, "ferry.db"), ReceiptsDir: filepath.Join(base, "receipts"),
		IdentityKey: filepath.Join(base, "identity.key"), Config: cfgPath}
	d, err := daemon.New(context.Background(), daemon.Options{Config: config.Default(), Paths: paths, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = d.Serve(context.Background()) }()
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(d.Close)
	return sock, cfgPath, base
}

func TestCLIAgainstDaemon(t *testing.T) {
	sock, cfg, base := liveDaemon(t)
	cli := func(args ...string) (int, string, string) {
		return ferry(t, append([]string{"--socket", sock, "--config", cfg}, args...)...)
	}
	card := filepath.Join(base, "CARD")
	_ = os.MkdirAll(filepath.Join(card, "CLIP"), 0o755)
	_ = os.WriteFile(filepath.Join(card, "CLIP", "A001.MOV"), []byte(strings.Repeat("x", 1<<20)), 0o644)
	ssd, ssd2 := filepath.Join(base, "SSD"), filepath.Join(base, "SSD2")
	_ = os.MkdirAll(ssd, 0o755)
	_ = os.MkdirAll(ssd2, 0o755)

	if code, out, errOut := cli("status", "--json"); code != 0 || !strings.Contains(out, `"protocol"`) {
		t.Fatalf("status: %d %s %s", code, out, errOut)
	}
	code, out, errOut := cli("plan", card, "--to", ssd, "--json")
	if code != 0 {
		t.Fatalf("plan: %s %s", out, errOut)
	}
	var plan daemon.PlanSummary
	if err := json.Unmarshal([]byte(out), &plan); err != nil || plan.Fingerprint == "" {
		t.Fatalf("%v %s", err, out)
	}
	if code, _, _ := cli("approve", plan.ID, "--fingerprint", "not-it"); code == 0 {
		t.Fatal("approved with a wrong fingerprint")
	}
	if code, out, errOut := cli("approve", plan.ID, "--fingerprint", plan.Fingerprint); code != 0 {
		t.Fatalf("approve: %s %s", out, errOut)
	}
	code, out, errOut = cli("send", card, "--to", ssd2, "--yes")
	if code != 0 || !strings.Contains(out, "completed") {
		t.Fatalf("send: %d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(ssd2, "Sources", "CARD", "CLIP", "A001.MOV")); err != nil {
		t.Fatal(err)
	}
	_, out, _ = cli("receipt", "list", "--json")
	var receipts []map[string]any
	_ = json.Unmarshal([]byte(out), &receipts)
	if len(receipts) != 1 {
		t.Fatalf("receipts: %s", out)
	}
	if code, out, errOut := cli("verify", "--transfer", receipts[0]["transfer_id"].(string)); code != 0 {
		t.Fatalf("verify via daemon: %d %s %s", code, out, errOut)
	}
	if code, out, _ := cli("preset", "preview", "sort-by-category", "a/b.mov", "--label", "X", "--json"); code != 0 || !strings.Contains(out, "Video/X/a/b.mov") {
		t.Fatalf("preview: %s", out)
	}
	if code, out, errOut := cli("doctor"); code != 0 || !strings.Contains(out, "database") {
		t.Fatalf("doctor: %d %s %s", code, out, errOut)
	}
	// A dead socket is reported plainly with a non-zero exit.
	if code, _, errOut := ferry(t, "--socket", filepath.Join(base, "nope.sock"), "--config", cfg, "status"); code == 0 || !strings.Contains(errOut, "not reachable") {
		t.Fatalf("dead socket: %d %s", code, errOut)
	}
}

func TestDoctorWithoutDaemon(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "NAS")
	_ = os.MkdirAll(root, 0o755)
	cfg := filepath.Join(base, "ferry.toml")
	write := func(body string) {
		if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("[[roots]]\npath = \"" + root + "\"\naccess = \"rw\"\n")
	dead := filepath.Join(base, "no.sock")
	code, out, errOut := ferry(t, "--socket", dead, "--config", cfg, "doctor")
	if code != 0 || !strings.Contains(out, "rw, publish hardlink") || !strings.Contains(errOut, "local checks only") {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	write("[[roots]]\npath = \"" + filepath.Join(base, "missing") + "\"\naccess = \"rw\"\n")
	if code, out, _ := ferry(t, "--socket", dead, "--config", cfg, "doctor"); code == 0 || !strings.Contains(out, "FAIL") {
		t.Fatalf("missing root passed doctor: %d %s", code, out)
	}
	write("[daemon]\nlisten_prot = 1\n")
	if code, _, errOut := ferry(t, "--socket", dead, "--config", cfg, "doctor"); code == 0 || !strings.Contains(errOut, "listen_prot") {
		t.Fatalf("unknown key not named: %d %s", code, errOut)
	}
}
