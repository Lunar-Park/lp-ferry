// Package config loads ferry.toml strictly.
//
// Strict means: any key Ferry does not know fails the load and names the
// key. A typo in a safety setting (say `alow_unsaved_destinations`) must
// never silently fall back to a default, because the default is what the
// operator was trying to change.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Checksum algorithms Ferry records in receipts. SHA-256 is the canonical
// identity; BLAKE3 is an opt-in fast mode. The algorithm is always written
// into the receipt so a verifier never has to guess.
const (
	ChecksumSHA256 = "sha256"
	ChecksumBLAKE3 = "blake3"
)

// Root access levels.
const (
	AccessRead      = "ro"
	AccessReadWrite = "rw"
)

// DefaultListenPort is the P2P listener port when none is configured.
const DefaultListenPort = 9410

// DefaultAutoApproveMaxBytes is 500 GiB.
const DefaultAutoApproveMaxBytes int64 = 500 << 30

// Config is the whole of ferry.toml.
type Config struct {
	Daemon      Daemon      `toml:"daemon"`
	Roots       []Root      `toml:"roots"`
	AgentPolicy AgentPolicy `toml:"agent_policy"`
}

// Daemon holds daemon-wide settings. Empty paths mean "use the platform
// default", resolved by the caller (see Paths).
type Daemon struct {
	ListenPort   int    `toml:"listen_port"`
	ChecksumAlgo string `toml:"checksum_algo"`
	Socket       string `toml:"socket"`
	DB           string `toml:"db"`
	ReceiptsDir  string `toml:"receipts_dir"`
}

// Root is one allowed root this machine exports to paired peers and agents.
type Root struct {
	Path   string `toml:"path"`
	Access string `toml:"access"`
}

// AgentPolicy bounds what an agent may do without a human.
type AgentPolicy struct {
	AutoApproveSavedDestinations bool  `toml:"auto_approve_saved_destinations"`
	AutoApproveMaxBytes          int64 `toml:"auto_approve_max_bytes"`
	AllowUnsavedDestinations     bool  `toml:"allow_unsaved_destinations"`
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{
		Daemon: Daemon{
			ListenPort:   DefaultListenPort,
			ChecksumAlgo: ChecksumSHA256,
		},
		AgentPolicy: AgentPolicy{
			AutoApproveSavedDestinations: false,
			AutoApproveMaxBytes:          DefaultAutoApproveMaxBytes,
			AllowUnsavedDestinations:     false,
		},
	}
}

// UnknownKeysError reports every key in the file Ferry does not understand.
type UnknownKeysError struct {
	Path string
	Keys []string
}

func (e *UnknownKeysError) Error() string {
	return fmt.Sprintf("%s: unknown config key(s): %s", e.Path, strings.Join(e.Keys, ", "))
}

// ErrNotFound is returned by LoadFile when the file does not exist.
var ErrNotFound = errors.New("config file not found")

// Parse decodes and validates TOML text. name is used in error messages.
func Parse(name, text string) (Config, error) {
	cfg := Default()
	md, err := toml.Decode(text, &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", name, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		return Config{}, &UnknownKeysError{Path: name, Keys: keys}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", name, err)
	}
	return cfg, nil
}

// LoadFile reads and parses path. A missing file returns ErrNotFound so the
// caller decides whether absence is fine (default location) or an error
// (an explicitly named --config).
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("%s: %w", path, ErrNotFound)
		}
		return Config{}, err
	}
	return Parse(path, string(data))
}

// Load resolves the effective config: an explicit path must exist; the
// default path may be absent, in which case defaults apply.
func Load(path string, explicit bool) (Config, error) {
	cfg, err := LoadFile(path)
	if err == nil {
		return cfg, nil
	}
	if !explicit && errors.Is(err, ErrNotFound) {
		return Default(), nil
	}
	return Config{}, err
}

// Validate checks values the type system cannot.
func (c Config) Validate() error {
	if c.Daemon.ListenPort < 1 || c.Daemon.ListenPort > 65535 {
		return fmt.Errorf("daemon.listen_port %d out of range 1..65535", c.Daemon.ListenPort)
	}
	switch c.Daemon.ChecksumAlgo {
	case ChecksumSHA256, ChecksumBLAKE3:
	default:
		return fmt.Errorf("daemon.checksum_algo %q: want %q or %q", c.Daemon.ChecksumAlgo, ChecksumSHA256, ChecksumBLAKE3)
	}
	for _, p := range []struct{ key, val string }{
		{"daemon.socket", c.Daemon.Socket},
		{"daemon.db", c.Daemon.DB},
		{"daemon.receipts_dir", c.Daemon.ReceiptsDir},
	} {
		if p.val != "" && !filepath.IsAbs(p.val) {
			return fmt.Errorf("%s %q must be an absolute path", p.key, p.val)
		}
	}
	seen := map[string]bool{}
	for i, r := range c.Roots {
		if r.Path == "" {
			return fmt.Errorf("roots[%d].path is required", i)
		}
		if !filepath.IsAbs(r.Path) {
			return fmt.Errorf("roots[%d].path %q must be absolute", i, r.Path)
		}
		if strings.ContainsRune(r.Path, 0) {
			return fmt.Errorf("roots[%d].path contains NUL", i)
		}
		clean := filepath.Clean(r.Path)
		if clean == "/" {
			return fmt.Errorf("roots[%d].path must not be the filesystem root", i)
		}
		if seen[clean] {
			return fmt.Errorf("roots[%d].path %q is listed twice", i, r.Path)
		}
		seen[clean] = true
		switch r.Access {
		case AccessRead, AccessReadWrite:
		default:
			return fmt.Errorf("roots[%d].access %q: want %q or %q", i, r.Access, AccessRead, AccessReadWrite)
		}
	}
	if c.AgentPolicy.AutoApproveMaxBytes < 0 {
		return fmt.Errorf("agent_policy.auto_approve_max_bytes must not be negative")
	}
	return nil
}

// Paths are the resolved on-disk locations the daemon uses.
type Paths struct {
	Home        string // ~/.ferry
	Config      string
	Socket      string
	DB          string
	ReceiptsDir string
	IdentityKey string
}

// DefaultPaths computes platform defaults for a user home directory and
// GOOS. FERRY_DB overrides the database path (see ResolvePaths).
func DefaultPaths(home, goos string) Paths {
	ferryHome := filepath.Join(home, ".ferry")
	db := filepath.Join(home, ".local", "share", "ferry", "ferry.db")
	if goos == "darwin" {
		db = filepath.Join(home, "Library", "Application Support", "ferry", "ferry.db")
	}
	return Paths{
		Home:        ferryHome,
		Config:      filepath.Join(ferryHome, "ferry.toml"),
		Socket:      filepath.Join(ferryHome, "ferry.sock"),
		DB:          db,
		ReceiptsDir: filepath.Join(ferryHome, "receipts"),
		IdentityKey: filepath.Join(ferryHome, "identity.key"),
	}
}

// ResolvePaths applies config overrides and the FERRY_DB environment
// variable on top of defaults. getenv is injected for tests.
func ResolvePaths(def Paths, c Config, getenv func(string) string) Paths {
	p := def
	if c.Daemon.Socket != "" {
		p.Socket = c.Daemon.Socket
	}
	if c.Daemon.DB != "" {
		p.DB = c.Daemon.DB
	}
	if v := getenv("FERRY_DB"); v != "" {
		p.DB = v
	}
	if c.Daemon.ReceiptsDir != "" {
		p.ReceiptsDir = c.Daemon.ReceiptsDir
	}
	return p
}

// RootFor returns the configured root containing path (lexically, after
// cleaning), or false. The longest matching root wins.
func (c Config) RootFor(path string) (Root, bool) {
	clean := filepath.Clean(path)
	var best Root
	found := false
	for _, r := range c.Roots {
		rp := filepath.Clean(r.Path)
		if clean == rp || strings.HasPrefix(clean, rp+string(filepath.Separator)) {
			if !found || len(rp) > len(filepath.Clean(best.Path)) {
				best, found = r, true
			}
		}
	}
	return best, found
}
