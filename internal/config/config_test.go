package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const example = `
[daemon]
listen_port = 9410
checksum_algo = "sha256"

[[roots]]
path = "/Volumes/Shuttle"
access = "rw"

[[roots]]
path = "/Volumes/CameraCards"
access = "ro"

[agent_policy]
auto_approve_saved_destinations = true
auto_approve_max_bytes = 536870912000
allow_unsaved_destinations = false
`

func TestParseExample(t *testing.T) {
	cfg, err := Parse("ferry.toml", example)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Daemon.ListenPort != 9410 || cfg.Daemon.ChecksumAlgo != "sha256" {
		t.Fatalf("daemon = %+v", cfg.Daemon)
	}
	if len(cfg.Roots) != 2 || cfg.Roots[1].Access != "ro" {
		t.Fatalf("roots = %+v", cfg.Roots)
	}
	if !cfg.AgentPolicy.AutoApproveSavedDestinations || cfg.AgentPolicy.AutoApproveMaxBytes != 536870912000 {
		t.Fatalf("policy = %+v", cfg.AgentPolicy)
	}
}

func TestDefaultsWhenEmpty(t *testing.T) {
	cfg, err := Parse("ferry.toml", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Daemon != Default().Daemon || cfg.AgentPolicy != Default().AgentPolicy || len(cfg.Roots) != 0 {
		t.Fatalf("got %+v", cfg)
	}
	if cfg.AgentPolicy.AllowUnsavedDestinations || cfg.AgentPolicy.AutoApproveSavedDestinations {
		t.Fatal("agent policy defaults must be closed")
	}
}

func TestUnknownKeysNamed(t *testing.T) {
	cases := map[string]string{
		"top-level":     "colour = \"blue\"\n",
		"daemon typo":   "[daemon]\nlisten_prot = 1\n",
		"policy typo":   "[agent_policy]\nalow_unsaved_destinations = true\n",
		"root typo":     "[[roots]]\npath = \"/a\"\naccess = \"rw\"\nmode = \"x\"\n",
		"unknown table": "[proxy]\nheight = 1080\n",
	}
	want := map[string]string{
		"top-level":     "colour",
		"daemon typo":   "daemon.listen_prot",
		"policy typo":   "agent_policy.alow_unsaved_destinations",
		"root typo":     "roots.mode",
		"unknown table": "proxy",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse("ferry.toml", text)
			var uk *UnknownKeysError
			if !errors.As(err, &uk) {
				t.Fatalf("want UnknownKeysError, got %v", err)
			}
			if !strings.Contains(err.Error(), want[name]) {
				t.Fatalf("error %q does not name %q", err, want[name])
			}
		})
	}
}

func TestValidation(t *testing.T) {
	bad := map[string]string{
		"port":          "[daemon]\nlisten_port = 70000\n",
		"algo":          "[daemon]\nchecksum_algo = \"md5\"\n",
		"relative root": "[[roots]]\npath = \"rel\"\naccess = \"rw\"\n",
		"fs root":       "[[roots]]\npath = \"/\"\naccess = \"rw\"\n",
		"access":        "[[roots]]\npath = \"/a\"\naccess = \"write\"\n",
		"dup root":      "[[roots]]\npath = \"/a\"\naccess = \"rw\"\n[[roots]]\npath = \"/a/\"\naccess = \"ro\"\n",
		"neg bytes":     "[agent_policy]\nauto_approve_max_bytes = -1\n",
		"rel socket":    "[daemon]\nsocket = \"ferry.sock\"\n",
		"wrong type":    "[daemon]\nlisten_port = \"9410\"\n",
	}
	for name, text := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse("ferry.toml", text); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLoadMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.toml")
	if _, err := Load(path, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("explicit missing: %v", err)
	}
	cfg, err := Load(path, false)
	if err != nil || cfg.Daemon.ListenPort != DefaultListenPort {
		t.Fatalf("default missing: %v %+v", err, cfg)
	}
	if err := os.WriteFile(path, []byte("bogus = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, false); err == nil {
		t.Fatal("a present but invalid default config must still fail")
	}
}

func TestPaths(t *testing.T) {
	d := DefaultPaths("/home/u", "linux")
	if d.DB != "/home/u/.local/share/ferry/ferry.db" || d.Socket != "/home/u/.ferry/ferry.sock" {
		t.Fatalf("%+v", d)
	}
	m := DefaultPaths("/Users/u", "darwin")
	if m.DB != "/Users/u/Library/Application Support/ferry/ferry.db" {
		t.Fatalf("%+v", m)
	}
	cfg := Default()
	cfg.Daemon.DB = "/x/cfg.db"
	p := ResolvePaths(d, cfg, func(k string) string {
		if k == "FERRY_DB" {
			return "/x/env.db"
		}
		return ""
	})
	if p.DB != "/x/env.db" {
		t.Fatalf("FERRY_DB must win: %s", p.DB)
	}
}

func TestRootFor(t *testing.T) {
	cfg, err := Parse("t", "[[roots]]\npath=\"/v\"\naccess=\"ro\"\n[[roots]]\npath=\"/v/in\"\naccess=\"rw\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := cfg.RootFor("/v/in/x"); !ok || r.Access != "rw" {
		t.Fatalf("longest root should win: %+v %v", r, ok)
	}
	if _, ok := cfg.RootFor("/vx/in"); ok {
		t.Fatal("prefix without separator must not match")
	}
	if _, ok := cfg.RootFor("/v/../etc"); ok {
		t.Fatal("cleaned path escaping root must not match")
	}
}
