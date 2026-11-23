// Package receipt builds, digests and exports portable ferry-receipt-v2
// JSON. A receipt is per-target and per-file truth: it never rounds a
// partial transfer up to success.
package receipt

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/model"
)

// Schema identifiers.
const (
	Schema        = "ferry-receipt-v2"
	SchemaVersion = 2
)

// File actions as written into receipts.
const (
	ActionCopied           = "copied"
	ActionSkippedIdentical = "skipped_identical"
	ActionExcluded         = "excluded"
	ActionFailed           = "failed"
	ActionCanceled         = "canceled"
	ActionNotRun           = "not_run"
	ActionDir              = "dir"
)

// Receipt is the portable proof of one transfer outcome.
type Receipt struct {
	Schema          string   `json:"schema"`
	SchemaVersion   int      `json:"schema_version"`
	ReceiptID       string   `json:"receipt_id"`
	TransferID      string   `json:"transfer_id"`
	PlanID          string   `json:"plan_id"`
	PlanFingerprint string   `json:"plan_fingerprint"`
	CreatedAt       string   `json:"created_at"`
	Actor           string   `json:"actor"`
	Status          string   `json:"status"`
	ChecksumAlgo    string   `json:"checksum_algo"`
	Preset          *Preset  `json:"preset,omitempty"`
	Targets         []Target `json:"targets"`
	Files           []File   `json:"files"`
	Findings        []string `json:"findings,omitempty"`
	Digest          string   `json:"receipt_digest"`
}

// Preset identifies the organization preset revision used (Phase 2).
type Preset struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int    `json:"revision"`
	Hash     string `json:"content_hash"`
}

// Target is one destination's outcome.
type Target struct {
	TargetID        string          `json:"target_id"`
	Label           string          `json:"label"`
	PeerID          string          `json:"peer_id"`
	RootPath        string          `json:"root_path"`
	VolumeIdentity  json.RawMessage `json:"volume_identity,omitempty"`
	Transport       string          `json:"transport"`
	PublishStrategy string          `json:"publish_strategy"`
	ReadbackMode    string          `json:"readback_mode,omitempty"`
	Status          string          `json:"status"`
	Error           string          `json:"error,omitempty"`
	StartedAt       string          `json:"started_at,omitempty"`
	EndedAt         string          `json:"ended_at,omitempty"`
	DurationMS      int64           `json:"duration_ms"`
	BytesWritten    int64           `json:"bytes_written"`
	ThroughputBPS   float64         `json:"throughput_bps"`
	FilesDone       int64           `json:"files_done"`
	FilesFailed     int64           `json:"files_failed"`
}

// File is one item on one target.
type File struct {
	TargetID       string `json:"target_id"`
	SourcePath     string `json:"source_path"`
	DestRelPath    string `json:"dest_rel_path"`
	Action         string `json:"action"`
	Size           int64  `json:"size"`
	SourceHash     string `json:"source_hash,omitempty"`
	DestHash       string `json:"dest_hash,omitempty"`
	ChecksumAlgo   string `json:"checksum_algo,omitempty"`
	ReadbackMode   string `json:"readback_mode,omitempty"`
	MatchedRule    string `json:"matched_rule,omitempty"`
	CameraRole     string `json:"camera_role,omitempty"`
	MtimePreserved *bool  `json:"mtime_preserved,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Warning        string `json:"warning,omitempty"`
}

// NewID returns a fresh receipt ID.
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "rcp_" + hex.EncodeToString(b[:])
}

func fileAction(it model.TransferItem) string {
	switch it.Status {
	case model.ItemVerified:
		if it.Entry.Action == model.ActionDir {
			return ActionDir
		}
		return ActionCopied
	case model.ItemSkippedIdentical:
		return ActionSkippedIdentical
	case model.ItemExcluded:
		return ActionExcluded
	case model.ItemFailed:
		return ActionFailed
	case model.ItemCanceled:
		return ActionCanceled
	default:
		return ActionNotRun
	}
}

// Build assembles a receipt from ledger state.
func Build(id string, plan *model.Plan, t *model.Transfer, targets []model.TransferTarget, items []model.TransferItem, findings []string, now time.Time) *Receipt {
	r := &Receipt{
		Schema: Schema, SchemaVersion: SchemaVersion, ReceiptID: id,
		TransferID: t.ID, PlanID: plan.ID, PlanFingerprint: plan.Fingerprint,
		CreatedAt: now.UTC().Format(time.RFC3339Nano), Actor: t.Actor, Status: t.Status,
		ChecksumAlgo: t.ChecksumAlgo, Findings: findings,
	}
	identity := map[string]string{}
	for _, d := range plan.Destinations {
		identity[d.ID] = d.IdentityJSON
	}
	for _, g := range targets {
		tg := Target{
			TargetID: g.ID, Label: g.Label, PeerID: g.PeerID, RootPath: g.RootPath, Transport: g.TransportKind,
			PublishStrategy: g.PublishStrategy, ReadbackMode: g.ReadbackMode, Status: g.Status, Error: g.Error,
			StartedAt: g.StartedAt, EndedAt: g.EndedAt, BytesWritten: g.BytesWritten, FilesDone: g.FilesDone, FilesFailed: g.FilesFailed,
		}
		if js := identity[g.PlanDestID]; js != "" && json.Valid([]byte(js)) {
			tg.VolumeIdentity = json.RawMessage(js)
		}
		s, e1 := time.Parse(time.RFC3339Nano, g.StartedAt)
		e, e2 := time.Parse(time.RFC3339Nano, g.EndedAt)
		if e1 == nil && e2 == nil && e.After(s) {
			tg.DurationMS = e.Sub(s).Milliseconds()
			tg.ThroughputBPS = float64(g.BytesWritten) / e.Sub(s).Seconds()
		}
		r.Targets = append(r.Targets, tg)
	}
	for _, it := range items {
		f := File{
			TargetID: it.TargetID, SourcePath: it.Entry.SourcePath, DestRelPath: it.Entry.DestRelPath,
			Action: fileAction(it), Size: it.Entry.Size, MatchedRule: it.Entry.MatchedRule, CameraRole: it.Entry.CameraRole,
			Warning: it.Warning,
		}
		switch f.Action {
		case ActionCopied, ActionSkippedIdentical:
			f.SourceHash, f.DestHash, f.ChecksumAlgo = it.SourceHash, it.DestHash, it.ChecksumAlgo
			f.ReadbackMode, f.MtimePreserved = it.ReadbackMode, it.MtimePreserved
		case ActionExcluded:
			f.Reason = it.Entry.ExclusionReason
		case ActionFailed, ActionCanceled, ActionNotRun:
			f.Reason = it.Error
		}
		r.Files = append(r.Files, f)
	}
	sort.SliceStable(r.Files, func(i, j int) bool {
		if r.Files[i].TargetID != r.Files[j].TargetID {
			return r.Files[i].TargetID < r.Files[j].TargetID
		}
		return r.Files[i].DestRelPath < r.Files[j].DestRelPath
	})
	r.Digest = ComputeDigest(r)
	return r
}

// ComputeDigest is SHA-256 over the sorted (target, dest_rel, dest_hash)
// triples of every file that landed, NUL-separated. Editing, removing or
// adding a landed file's line changes it.
func ComputeDigest(r *Receipt) string {
	var lines []string
	for _, f := range r.Files {
		if f.DestHash == "" {
			continue
		}
		lines = append(lines, f.TargetID+"\x00"+f.DestRelPath+"\x00"+f.DestHash)
	}
	sort.Strings(lines)
	h := sha256.New()
	_, _ = h.Write([]byte(r.ChecksumAlgo + "\n"))
	for _, l := range lines {
		_, _ = h.Write([]byte(l))
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Marshal renders canonical, indented JSON.
func (r *Receipt) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// ErrInvalid marks a receipt that is malformed or fails its digest.
var ErrInvalid = errors.New("invalid receipt")

// Parse decodes and validates a receipt, including its digest.
func Parse(data []byte) (*Receipt, error) {
	var r Receipt
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if r.Schema != Schema || r.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: schema %q v%d", ErrInvalid, r.Schema, r.SchemaVersion)
	}
	if got := ComputeDigest(&r); got != r.Digest {
		return nil, fmt.Errorf("%w: digest mismatch (recorded %s, computed %s); the receipt was edited", ErrInvalid, r.Digest, got)
	}
	return &r, nil
}

// Export writes r to dir/<receipt_id>.json: temp file, fsync, then a
// no-clobber hard link. Re-exporting identical bytes is a no-op; a
// different file already at that name is an error, never overwritten.
func Export(dir string, r *Receipt) (string, error) {
	body, err := r.Marshal()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	final := filepath.Join(dir, r.ReceiptID+".json")
	if existing, err := os.ReadFile(final); err == nil {
		if bytes.Equal(existing, body) {
			return final, nil
		}
		return "", fmt.Errorf("%s already exists with different content", final)
	}
	tmp, err := os.CreateTemp(dir, "."+r.ReceiptID+".*.ferry-part")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Link(tmpName, final); err != nil {
		return "", err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return final, nil
}
