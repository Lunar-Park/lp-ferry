package main

// The agent-CLI contract (P1.4 / Slice 2A.3): with --json every failure is one JSON object on
// stderr and a code that says what kind it was, `--actor` names who a call is from, and
// `send --json` without --yes answers with the plan (id, fingerprint, status) instead of prose.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lunar-Park/lp-ferry/internal/daemon"
)

// errorOf reads the one object --json promises on stderr.
func errorOf(t *testing.T, errOut string) (code, message string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(errOut), &body); err != nil {
		t.Fatalf("stderr is not the JSON error object: %q (%v)", errOut, err)
	}
	if body.Error.Code == "" || body.Error.Message == "" {
		t.Fatalf("the error object needs a code and a message: %q", errOut)
	}
	return body.Error.Code, body.Error.Message
}

func TestAgentCLIUsageErrorsAreJSONAndExitTwo(t *testing.T) {
	cases := [][]string{
		{"--json", "plan"},                             // a source is required
		{"--json", "approve", "pln_1"},                 // --fingerprint is required
		{"--json", "peer", "probe"},                    // an argument is required
		{"--json", "status", "--nonsense"},             // an unknown flag
		{"--json", "plan", "x", "--to", "y", "--role"}, // a flag with no value
	}
	for _, args := range cases {
		code, out, errOut := ferry(t, args...)
		if code != exitUsage {
			t.Fatalf("%v: exit %d, want %d (%s)", args, code, exitUsage, errOut)
		}
		if kind, msg := errorOf(t, errOut); kind != "usage" {
			t.Fatalf("%v: code %q, want usage (%s)", args, kind, msg)
		}
		if strings.TrimSpace(out) != "" {
			t.Fatalf("%v: nothing belongs on stdout: %q", args, out)
		}
	}

	// Without --json the sentence a person reads is unchanged, and no JSON is printed.
	code, _, errOut := ferry(t, "plan")
	if code != exitUsage || !strings.HasPrefix(errOut, "ferry: ") || strings.Contains(errOut, "{") {
		t.Fatalf("plain usage error: %d %q", code, errOut)
	}
}

func TestAgentCLIUnreachableDaemonIsExitThree(t *testing.T) {
	base := t.TempDir()
	cfg := filepath.Join(base, "ferry.toml")
	if err := os.WriteFile(cfg, []byte("[daemon]\nchecksum_algo = \"sha256\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dead := filepath.Join(base, "no.sock")

	code, out, errOut := ferry(t, "--json", "--socket", dead, "--config", cfg, "status")
	if code != exitEnv {
		t.Fatalf("dead socket: exit %d, want %d (%s)", code, exitEnv, errOut)
	}
	kind, msg := errorOf(t, errOut)
	if kind != "unreachable" || !strings.Contains(msg, "not reachable") {
		t.Fatalf("dead socket: %q %q", kind, msg)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("stdout: %q", out)
	}

	// A config that cannot be read is this machine's problem too, not the command's.
	bad := filepath.Join(base, "bad.toml")
	if err := os.WriteFile(bad, []byte("listen_prot = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := ferry(t, "--json", "--socket", dead, "--config", bad, "status"); code != exitEnv {
		t.Fatalf("unreadable config: exit %d (%s)", code, errOut)
	} else if kind, _ := errorOf(t, errOut); kind != "config" {
		t.Fatalf("unreadable config: code %q, want config", kind)
	}
}

func TestAgentCLIRefusalIsExitOneAndJSON(t *testing.T) {
	sock, cfg, base := liveDaemon(t)
	cli := func(args ...string) (int, string, string) {
		return ferry(t, append([]string{"--socket", sock, "--config", cfg}, args...)...)
	}
	card, ssd := filepath.Join(base, "CARD"), filepath.Join(base, "SSD")
	_ = os.MkdirAll(card, 0o755)
	_ = os.MkdirAll(ssd, 0o755)
	_ = os.WriteFile(filepath.Join(card, "A.MOV"), []byte("x"), 0o644)

	code, out, errOut := cli("plan", card, "--to", ssd, "--json")
	if code != 0 {
		t.Fatalf("plan: %s %s", out, errOut)
	}
	var plan daemon.PlanSummary
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	code, _, errOut = cli("approve", plan.ID, "--fingerprint", "not-it", "--json")
	if code != exitRefused {
		t.Fatalf("wrong fingerprint: exit %d, want %d (%s)", code, exitRefused, errOut)
	}
	if kind, _ := errorOf(t, errOut); kind != "refused" {
		t.Fatalf("wrong fingerprint: code %q, want refused", kind)
	}
}

func TestAgentCLIActorSaysWhoTheWorkIsFrom(t *testing.T) {
	sock, cfg, base := liveDaemon(t)
	card, ssd := filepath.Join(base, "CARD"), filepath.Join(base, "SSD")
	_ = os.MkdirAll(card, 0o755)
	_ = os.MkdirAll(ssd, 0o755)
	_ = os.WriteFile(filepath.Join(card, "A.MOV"), []byte("x"), 0o644)
	planWith := func(args ...string) daemon.PlanSummary {
		t.Helper()
		code, out, errOut := ferry(t, append([]string{"--socket", sock, "--config", cfg, "plan", card, "--to", ssd, "--json"}, args...)...)
		if code != 0 {
			t.Fatalf("plan %v: %s %s", args, out, errOut)
		}
		var p daemon.PlanSummary
		if err := json.Unmarshal([]byte(out), &p); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		return p
	}

	if p := planWith(); p.Actor != "human" {
		t.Fatalf("no actor named: %q, want human", p.Actor)
	}
	if p := planWith("--actor", "claude"); p.Actor != "agent:claude" {
		t.Fatalf("--actor claude: %q, want agent:claude", p.Actor)
	}
	if p := planWith("--actor", "human"); p.Actor != "human" {
		t.Fatalf("--actor human: %q", p.Actor)
	}
	if p := planWith("--actor", "agent:codex"); p.Actor != "agent:codex" {
		t.Fatalf("an already-qualified name is kept: %q", p.Actor)
	}

	// An agent's shell sets FERRY_ACTOR once; PARK_ACTOR is the older name for the same thing.
	t.Setenv("FERRY_ACTOR", "janet")
	if p := planWith(); p.Actor != "agent:janet" {
		t.Fatalf("FERRY_ACTOR: %q", p.Actor)
	}
	if p := planWith("--actor", "miles"); p.Actor != "agent:miles" {
		t.Fatalf("the flag wins over the environment: %q", p.Actor)
	}
	t.Setenv("FERRY_ACTOR", "")
	t.Setenv("PARK_ACTOR", "steve")
	if p := planWith(); p.Actor != "agent:steve" {
		t.Fatalf("PARK_ACTOR: %q", p.Actor)
	}
}

func TestAgentCLISendJSONAnswersWithThePlan(t *testing.T) {
	sock, cfg, base := liveDaemon(t)
	cli := func(args ...string) (int, string, string) {
		return ferry(t, append([]string{"--socket", sock, "--config", cfg}, args...)...)
	}
	card, ssd := filepath.Join(base, "CARD"), filepath.Join(base, "SSD")
	_ = os.MkdirAll(filepath.Join(card, "CLIP"), 0o755)
	_ = os.MkdirAll(ssd, 0o755)
	_ = os.WriteFile(filepath.Join(card, "CLIP", "A.MOV"), []byte("x"), 0o644)

	// Without --yes the plan is the answer: one object, with the id an approve needs.
	code, out, errOut := cli("send", card, "--to", ssd, "--json")
	if code != 0 {
		t.Fatalf("send --json: exit %d %s %s", code, out, errOut)
	}
	var got struct {
		PlanID      string             `json:"plan_id"`
		Fingerprint string             `json:"fingerprint"`
		Status      string             `json:"status"`
		Blocking    int                `json:"blocking"`
		Summary     daemon.PlanSummary `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %v %q", err, out)
	}
	if got.PlanID == "" || got.Fingerprint == "" || got.Summary.ID != got.PlanID {
		t.Fatalf("%+v", got)
	}
	if got.Status != "awaiting_approval" || got.Blocking != 0 {
		t.Fatalf("status %q blocking %d", got.Status, got.Blocking)
	}
	if strings.Contains(out, "review, then run") {
		t.Fatalf("prose leaked into --json: %q", out)
	}

	// A plan that cannot run says so, and the refusal is a JSON error with exit 1. A symlink
	// in the source is a blocking finding Ferry will not guess about.
	if err := os.Symlink(filepath.Join(card, "CLIP", "A.MOV"), filepath.Join(card, "LINK")); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	code, out, errOut = cli("send", card, "--to", ssd, "--json")
	if code != exitRefused {
		t.Fatalf("blocked send: exit %d, want %d (%s %s)", code, exitRefused, out, errOut)
	}
	got = struct {
		PlanID      string             `json:"plan_id"`
		Fingerprint string             `json:"fingerprint"`
		Status      string             `json:"status"`
		Blocking    int                `json:"blocking"`
		Summary     daemon.PlanSummary `json:"summary"`
	}{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("the plan is still printed on stdout: %v %q", err, out)
	}
	if got.Status != "planned" || got.Blocking == 0 {
		t.Fatalf("status %q blocking %d", got.Status, got.Blocking)
	}
	if kind, _ := errorOf(t, errOut); kind != "plan_blocked" {
		t.Fatalf("blocked send: code %q, want plan_blocked", kind)
	}
	_ = os.Remove(filepath.Join(card, "LINK"))

	// A source that is not there at all never becomes a plan: that is a plain refusal.
	code, out, errOut = cli("send", filepath.Join(base, "NOPE"), "--to", ssd, "--json")
	if code != exitRefused || strings.TrimSpace(out) != "" {
		t.Fatalf("missing source: exit %d %q %q", code, out, errOut)
	}
	if kind, _ := errorOf(t, errOut); kind != "refused" {
		t.Fatalf("missing source: code %q, want refused", kind)
	}

	// And a run that happens still answers with the transfer alone (one object).
	code, out, errOut = cli("send", card, "--to", ssd, "--yes", "--json", "--wait=false")
	if code != 0 {
		t.Fatalf("send --yes: exit %d %s %s", code, out, errOut)
	}
	var started map[string]any
	if err := json.Unmarshal([]byte(out), &started); err != nil || started["transfer_id"] == nil {
		t.Fatalf("send --yes --json: %v %q", err, out)
	}

	// Without --json the prose and the hint are unchanged.
	code, out, _ = cli("send", card, "--to", ssd)
	if code != 0 || !strings.Contains(out, "fingerprint") || !strings.Contains(out, "review, then run: ferry approve") {
		t.Fatalf("plain send: %d %q", code, out)
	}
}
