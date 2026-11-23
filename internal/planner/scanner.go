// Package planner turns a PlanRequest into an immutable, fingerprinted plan:
// full inventory, metadata, preset routing, the conflict matrix, capacity,
// and a lazily queried Before → After tree.
package planner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/Lunar-Park/lp-ferry/internal/metadata"
)

// Entry types.
const (
	TypeFile    = "file"
	TypeDir     = "dir"
	TypeSymlink = "symlink"
	TypeOther   = "other"
)

// ScanEntry is one inventoried object. Nothing is capped or skipped
// silently: unreadable paths, symlinks and special files are entries.
type ScanEntry struct {
	Rel      string `json:"rel"`
	Type     string `json:"type"`
	Size     int64  `json:"size"`
	MtimeNS  int64  `json:"mtime_ns"`
	Err      string `json:"error,omitempty"`
	Artifact bool   `json:"system_artifact,omitempty"`
}

// Inventory is a full scan of one source.
type Inventory struct {
	ID           string                    `json:"id"`
	PeerID       string                    `json:"peer_id"`
	Root         string                    `json:"root"`
	Label        string                    `json:"label"`
	Entries      []ScanEntry               `json:"entries"`
	Files        int64                     `json:"files"`
	Bytes        int64                     `json:"bytes"`
	Errors       int64                     `json:"errors"`
	ManifestHash string                    `json:"manifest_hash"`
	Card         *metadata.CardHint        `json:"card,omitempty"`
	Media        map[string]metadata.Media `json:"media,omitempty"`
}

// SystemArtifact reports OS/index junk by name: dot-files (.DS_Store,
// AppleDouble ._ sidecars, .Spotlight-V100, .Trashes) and volume litter.
func SystemArtifact(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "$RECYCLE.BIN", "System Volume Information", "LOST.DIR", "Thumbs.db", "desktop.ini":
		return true
	}
	return false
}

// Scan inventories root completely. It never follows symlinks. A system
// artifact directory is recorded once and not descended into.
func Scan(ctx context.Context, root, label string) (*Inventory, error) {
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("source %s is not a directory", root)
	}
	if label == "" {
		label = filepath.Base(real)
	}
	inv := &Inventory{PeerID: "local", Root: real, Label: label}
	err = filepath.WalkDir(real, func(p string, d fs.DirEntry, werr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == real {
			if werr != nil {
				return werr
			}
			return nil
		}
		rel, _ := filepath.Rel(real, p)
		rel = filepath.ToSlash(rel)
		e := ScanEntry{Rel: rel, Artifact: SystemArtifact(path.Base(rel))}
		if werr != nil {
			e.Type, e.Err = TypeOther, werr.Error()
			if d != nil && d.IsDir() {
				e.Type = TypeDir
			}
			inv.Entries = append(inv.Entries, e)
			inv.Errors++
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			e.Type, e.Err = TypeOther, err.Error()
			inv.Entries = append(inv.Entries, e)
			inv.Errors++
			return nil
		}
		e.MtimeNS = info.ModTime().UnixNano()
		switch m := info.Mode(); {
		case m.IsRegular():
			e.Type, e.Size = TypeFile, info.Size()
			if !e.Artifact {
				// Readability is part of the inventory: an unreadable file
				// is a finding now, not a failed copy later.
				if fd, err := unix.Open(p, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0); err != nil {
					e.Err = (&os.PathError{Op: "open", Path: p, Err: err}).Error()
					inv.Errors++
				} else {
					_ = unix.Close(fd)
					inv.Files++
					inv.Bytes += e.Size
				}
			}
		case m.IsDir():
			e.Type = TypeDir
		case m&fs.ModeSymlink != 0:
			e.Type = TypeSymlink
		default:
			e.Type = TypeOther
		}
		inv.Entries = append(inv.Entries, e)
		if e.Type == TypeDir && e.Artifact {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(inv.Entries, func(i, j int) bool { return inv.Entries[i].Rel < inv.Entries[j].Rel })
	inv.ManifestHash = ManifestHash(inv.Entries)
	var rels []string
	for _, e := range inv.Entries {
		if e.Type == TypeFile && !e.Artifact && e.Err == "" {
			rels = append(rels, e.Rel)
		}
	}
	inv.Media, _ = metadata.Probe(real, rels)
	inv.Card = metadata.DetectCard(rels, inv.Media)
	return inv, nil
}

// ManifestHash is SHA-256 over the sorted inventory.
func ManifestHash(es []ScanEntry) string {
	h := sha256.New()
	for _, e := range es {
		fmt.Fprintf(h, "%s\x00%s\x00%d\x00%d\x00%s\n", e.Rel, e.Type, e.Size, e.MtimeNS, e.Err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
