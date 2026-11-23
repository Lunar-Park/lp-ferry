// Package verify independently re-proves a receipt against the disk: every
// landed file is read back with the page cache bypassed and re-hashed.
package verify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/Lunar-Park/lp-ferry/internal/checksum"
	"github.com/Lunar-Park/lp-ferry/internal/ledger"
	"github.com/Lunar-Park/lp-ferry/internal/receipt"
	"github.com/Lunar-Park/lp-ferry/internal/storage"
)

// Exit codes for `ferry verify`, cron-friendly and priority ordered: a
// verification that could not read everything (4) outranks every finding,
// because it proves nothing about what it could not read. Then missing (1)
// > modified (2) > added (3). The report carries the full counts.
const (
	ExitClean      = 0
	ExitMissing    = 1
	ExitModified   = 2
	ExitAdded      = 3
	ExitUnreadable = 4
)

// Options narrow a verification.
type Options struct {
	TargetID string // verify only this target ("" = every target)
	Root     string // override the target's root path (requires one target)
}

// Problem is a file that could not be checked.
type Problem struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// TargetReport is one destination's verdict.
type TargetReport struct {
	TargetID   string    `json:"target_id"`
	Label      string    `json:"label"`
	Root       string    `json:"root"`
	Checked    int       `json:"checked"`
	Missing    []string  `json:"missing"`
	Modified   []string  `json:"modified"`
	Added      []string  `json:"added"`
	Unreadable []Problem `json:"unreadable"`
}

// Report is a whole verification.
type Report struct {
	ReceiptID    string         `json:"receipt_id"`
	TransferID   string         `json:"transfer_id"`
	ChecksumAlgo string         `json:"checksum_algo"`
	ReadbackMode string         `json:"readback_mode"`
	Targets      []TargetReport `json:"targets"`
	Checked      int            `json:"checked"`
	Missing      int            `json:"missing"`
	Modified     int            `json:"modified"`
	Added        int            `json:"added"`
	Unreadable   int            `json:"unreadable"`
	Clean        bool           `json:"clean"`
	ExitCode     int            `json:"exit_code"`
}

// SystemArtifact reports OS/index junk that is invisible to verification:
// dot-files (.DS_Store, AppleDouble ._ sidecars, Ferry's own temps) and
// the usual volume litter.
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

// Verify checks rc against disk.
func Verify(ctx context.Context, rc *receipt.Receipt, o Options) (*Report, error) {
	if !checksum.Valid(rc.ChecksumAlgo) {
		return nil, fmt.Errorf("%w: unsupported checksum algorithm %q", receipt.ErrInvalid, rc.ChecksumAlgo)
	}
	var targets []receipt.Target
	for _, t := range rc.Targets {
		if o.TargetID == "" || t.TargetID == o.TargetID {
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("receipt %s has no target %q", rc.ReceiptID, o.TargetID)
	}
	if o.Root != "" && len(targets) != 1 {
		return nil, fmt.Errorf("--root needs exactly one target; this receipt has %d (choose one with --target)", len(targets))
	}
	rep := &Report{ReceiptID: rc.ReceiptID, TransferID: rc.TransferID, ChecksumAlgo: rc.ChecksumAlgo, ReadbackMode: storage.ReadbackMode}
	for _, t := range targets {
		root := t.RootPath
		if o.Root != "" {
			root = o.Root
		}
		tr, err := verifyTarget(ctx, rc, t, root)
		if err != nil {
			return nil, err
		}
		rep.Targets = append(rep.Targets, *tr)
		rep.Checked += tr.Checked
		rep.Missing += len(tr.Missing)
		rep.Modified += len(tr.Modified)
		rep.Added += len(tr.Added)
		rep.Unreadable += len(tr.Unreadable)
	}
	switch {
	case rep.Unreadable > 0:
		rep.ExitCode = ExitUnreadable
	case rep.Missing > 0:
		rep.ExitCode = ExitMissing
	case rep.Modified > 0:
		rep.ExitCode = ExitModified
	case rep.Added > 0:
		rep.ExitCode = ExitAdded
	}
	rep.Clean = rep.ExitCode == ExitClean
	return rep, nil
}

func verifyTarget(ctx context.Context, rc *receipt.Receipt, t receipt.Target, rootPath string) (*TargetReport, error) {
	tr := &TargetReport{TargetID: t.TargetID, Label: t.Label, Root: rootPath, Missing: []string{}, Modified: []string{}, Added: []string{}, Unreadable: []Problem{}}
	root, err := storage.OpenRoot(rootPath)
	if err != nil {
		tr.Unreadable = append(tr.Unreadable, Problem{Path: rootPath, Error: "destination root unreachable: " + err.Error()})
		return tr, nil
	}
	known := map[string]bool{}
	dirs := map[string]bool{}
	for _, f := range rc.Files {
		if f.TargetID != t.TargetID || f.DestHash == "" {
			continue
		}
		known[norm.NFC.String(f.DestRelPath)] = true
		dirs[path.Dir(f.DestRelPath)] = true
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tr.Checked++
		state, perr := checkFile(ctx, root, f, rc.ChecksumAlgo)
		switch state {
		case "missing":
			tr.Missing = append(tr.Missing, f.DestRelPath)
		case "modified":
			tr.Modified = append(tr.Modified, f.DestRelPath)
		case "unreadable":
			tr.Unreadable = append(tr.Unreadable, Problem{Path: f.DestRelPath, Error: perr.Error()})
		}
	}
	// Added: anything new in a folder this receipt wrote into. Folders the
	// receipt never touched may legitimately hold other transfers' files.
	for d := range dirs {
		var parts []string
		if d != "." {
			parts = strings.Split(d, "/")
		}
		dir, err := storage.OpenDir(root, parts, false)
		if err != nil {
			continue // the folder itself is gone; its files are already missing
		}
		entries, err := os.ReadDir(dir.DirPath())
		_ = dir.Close()
		if err != nil {
			tr.Unreadable = append(tr.Unreadable, Problem{Path: d, Error: err.Error()})
			continue
		}
		for _, e := range entries {
			if SystemArtifact(e.Name()) || e.IsDir() {
				continue
			}
			rel := e.Name()
			if d != "." {
				rel = d + "/" + e.Name()
			}
			if !known[norm.NFC.String(rel)] {
				tr.Added = append(tr.Added, rel)
			}
		}
	}
	sort.Strings(tr.Missing)
	sort.Strings(tr.Modified)
	sort.Strings(tr.Added)
	return tr, nil
}

func checkFile(ctx context.Context, root *storage.Root, f receipt.File, algo string) (string, error) {
	dir, leaf, err := storage.OpenFileIn(root, f.DestRelPath, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "missing", nil
		}
		if errors.Is(err, storage.ErrUnsafePath) {
			return "modified", nil // an ancestor became a link or a file
		}
		return "unreadable", err
	}
	defer func() { _ = dir.Close() }()
	rb, _, err := dir.OpenReadback(leaf)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "missing", nil
	case errors.Is(err, storage.ErrUnsafePath), errors.Is(err, storage.ErrNotRegular):
		return "modified", nil
	case err != nil:
		return "unreadable", err
	}
	defer func() { _ = rb.Close() }()
	h, err := checksum.New(algo)
	if err != nil {
		return "unreadable", err
	}
	if _, err := io.Copy(h, ctxReader{ctx, rb}); err != nil {
		return "unreadable", err
	}
	if checksum.Hex(h) != f.DestHash {
		return "modified", nil
	}
	return "ok", nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// Record stores a report in the ledger's verifications table.
func Record(ctx context.Context, db *ledger.DB, rep *Report) error {
	return db.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, t := range rep.Targets {
			detail, err := json.Marshal(t)
			if err != nil {
				return err
			}
			if err := ledger.InsertVerification(tx, &ledger.Verification{
				ReceiptID: rep.ReceiptID, TransferID: rep.TransferID, PeerID: "local", Folder: filepath.Clean(t.Root),
				Checked: t.Checked, Missing: len(t.Missing), Modified: len(t.Modified), Added: len(t.Added),
				Unreadable: len(t.Unreadable), Clean: len(t.Missing)+len(t.Modified)+len(t.Added)+len(t.Unreadable) == 0,
				ReadbackMode: rep.ReadbackMode, DetailJSON: string(detail),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}
