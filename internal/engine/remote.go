package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"github.com/Lunar-Park/lp-ferry/internal/ledger"
	"github.com/Lunar-Park/lp-ferry/internal/model"
)

// ReadbackRemote is recorded for targets verified by the remote peer's own
// physical readback.
const ReadbackRemote = "remote_readback"

// RemoteVerdict is a peer's account of one item it received or pulled.
type RemoteVerdict struct {
	ItemID          int64
	Status          string // verified | failed | canceled
	SourceHash      string
	DestHash        string
	ReadbackMode    string
	PublishStrategy string
	MtimePreserved  bool
	Error           string
}

// RemotePush is one streaming write to a peer.
type RemotePush interface {
	io.Writer
	Finish(sourceHash string) (RemoteVerdict, error)
	Abort(err error)
}

// PullItem is one item a destination pulls from a source peer.
type PullItem struct {
	ItemID     int64
	SourceRoot string
	SourceRel  string
	DestRel    string
	Size       int64
	MtimeNS    int64
}

// PullHooks observe a pull. BeforePublish lets the engine record the
// source hash durably before the file becomes visible (crash window 2).
type PullHooks struct {
	Progress      func(itemID int64, n int64)
	BeforePublish func(itemID int64, sourceHash string) error
}

// Remote is how the engine reaches other machines. The p2p package
// implements it; the engine never speaks the wire protocol itself.
type Remote interface {
	// Push streams one item from a local source to a destination peer.
	Push(ctx context.Context, peer, transferID string, itemID int64, root, destRel string, size, mtimeNS int64) (RemotePush, error)
	// Pull fetches items from a source peer into a local root.
	Pull(ctx context.Context, sourcePeer, transferID, root string, items []PullItem, h PullHooks) ([]RemoteVerdict, error)
	// Land asks destPeer to pull items from sourcePeer directly.
	Land(ctx context.Context, destPeer, sourcePeer, transferID, root string, items []PullItem, h PullHooks) ([]RemoteVerdict, error)
	// Mkdir creates a (retained, empty) directory under a peer's root.
	Mkdir(ctx context.Context, peer, transferID, root, rel string) error
	// Check re-verifies files on a peer: destRel -> expected hash in,
	// destRel -> ok | missing | modified | unreadable out.
	Check(ctx context.Context, peer, root, algo string, files map[string]string) (map[string]string, error)
}

func isLocal(peer string) bool { return peer == "" || peer == model.LocalPeer }

func (x *run) finishRemote(k *sink, sourceHash string, size int64) {
	it, s := k.it, k.s
	it.Status, it.SourceHash, it.BytesCopied = model.ItemPublishing, sourceHash, k.written
	if err := x.write(func(tx *sql.Tx) error { return ledger.UpdateItem(tx, it) }); err != nil {
		k.push.Abort(err)
		return
	}
	if x.crash(PointBeforePublish, it) {
		return
	}
	v, err := k.push.Finish(sourceHash)
	k.push = nil
	if x.crashed.Load() {
		return
	}
	if err == nil && (v.Status != "verified" || v.DestHash != sourceHash) {
		err = fmt.Errorf("%s: %s", firstNonEmpty(v.Status, "failed"), firstNonEmpty(v.Error, "remote readback did not match"))
	}
	if err != nil {
		x.settleFailure(it, nil, model.ItemFailed, fmt.Errorf("peer %s: %w", s.g.PeerID, err))
		return
	}
	x.tele.Verified(s.g.ID, size)
	if x.crash(PointPublished, it) {
		return
	}
	mp := v.MtimePreserved
	it.Status, it.DestHash, it.ReadbackMode, it.PublishStrategy, it.MtimePreserved, it.Error = model.ItemVerified, v.DestHash,
		firstNonEmpty(v.ReadbackMode, ReadbackRemote), v.PublishStrategy, &mp, ""
	_ = x.commitItem(it, s, size, true)
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

// pullUnit moves one remote-source item to each of its targets: a local
// target pulls it here; a remote target is told to pull it directly from
// the source peer, so this machine carries no bytes.
func (x *run) pullUnit(u *unit) {
	if x.r.Remote == nil {
		for _, it := range u.items {
			x.settleFailure(it, nil, model.ItemFailed, errors.New("remote source but no P2P transport"))
		}
		return
	}
	for _, it := range u.items {
		if x.ctx.Err() != nil || x.crashed.Load() {
			return
		}
		s := x.byID[it.TargetID]
		if err := s.haltErr(); err != nil {
			x.settleFailure(it, nil, model.ItemFailed, fmt.Errorf("target halted: %w", err))
			continue
		}
		it.Status, it.Error, it.Warning, it.PartPath = model.ItemCopying, "", "", ""
		it.FinalPath = s.g.PeerID + ":" + s.g.RootPath + "/" + it.Entry.DestRelPath
		it.PublishStrategy = s.g.PublishStrategy
		it.Attempts++
		if err := x.write(func(tx *sql.Tx) error { return ledger.UpdateItem(tx, it) }); err != nil {
			return
		}
		x.tele.Phase(s.g.ID, it.Entry.DestRelPath, PhaseStreaming)
		items := []PullItem{{ItemID: it.ID, SourceRoot: it.Entry.SourceRoot, SourceRel: it.Entry.SourceRel,
			DestRel: it.Entry.DestRelPath, Size: it.Entry.Size, MtimeNS: it.Entry.MtimeNS}}
		hooks := PullHooks{
			Progress: func(_ int64, n int64) { x.tele.Wrote(s.g.ID, n) },
			BeforePublish: func(_ int64, sum string) error {
				it.Status, it.SourceHash = model.ItemPublishing, sum
				if err := x.write(func(tx *sql.Tx) error { return ledger.UpdateItem(tx, it) }); err != nil {
					return err
				}
				if x.crash(PointBeforePublish, it) {
					return ErrSimulatedCrash
				}
				return nil
			},
		}
		var vs []RemoteVerdict
		var err error
		if s.root == nil {
			vs, err = x.r.Remote.Land(x.ctx, s.g.PeerID, it.Entry.SourcePeer, x.t.ID, s.g.RootPath, items, hooks)
		} else {
			vs, err = x.r.Remote.Pull(x.ctx, it.Entry.SourcePeer, x.t.ID, s.root.Real, items, hooks)
		}
		if x.crashed.Load() {
			return
		}
		if err == nil && len(vs) != 1 {
			err = fmt.Errorf("expected one result, got %d", len(vs))
		}
		if err != nil {
			status := model.ItemFailed
			if x.ctx.Err() != nil {
				status = model.ItemCanceled
			}
			x.settleFailure(it, nil, status, err)
			continue
		}
		v := vs[0]
		if v.Status != "verified" || v.DestHash == "" || v.DestHash != v.SourceHash {
			status := model.ItemFailed
			if v.Status == "canceled" {
				status = model.ItemCanceled
			}
			x.settleFailure(it, nil, status, fmt.Errorf("%s", firstNonEmpty(v.Error, "pull did not verify")))
			continue
		}
		if x.crash(PointPublished, it) {
			return
		}
		x.tele.Verified(s.g.ID, it.Entry.Size)
		mp := v.MtimePreserved
		it.Status, it.SourceHash, it.DestHash, it.ReadbackMode, it.PublishStrategy, it.MtimePreserved, it.Error =
			model.ItemVerified, v.SourceHash, v.DestHash, v.ReadbackMode, v.PublishStrategy, &mp, ""
		it.BytesCopied = it.Entry.Size
		_ = x.commitItem(it, s, it.Entry.Size, true)
	}
}

// reconcileRemote settles a remote target's in-flight item on resume.
func (x *run) reconcileRemote(s *tstate, it *model.TransferItem) error {
	switch it.Status {
	case model.ItemCopying:
		// The peer discards a broken push's temp itself, and keeps a broken
		// pull's temp for the resumed pull to continue from.
		x.rearm(it)
	case model.ItemPublishing:
		if x.r.Remote == nil {
			return errors.New("remote target but no P2P transport")
		}
		states, err := x.r.Remote.Check(x.ctx, s.g.PeerID, s.g.RootPath, x.t.ChecksumAlgo, map[string]string{it.Entry.DestRelPath: it.SourceHash})
		if err != nil {
			return fmt.Errorf("checking %s on %s: %w", it.Entry.DestRelPath, s.g.PeerID, err)
		}
		switch states[it.Entry.DestRelPath] {
		case "ok":
			it.Status, it.DestHash, it.ReadbackMode, it.Error = model.ItemVerified, it.SourceHash, ReadbackRemote, ""
			it.Warning = "verified on the peer after an interruption; not re-sent"
		case "missing":
			x.rearm(it)
		default:
			it.Status = model.ItemFailed
			it.Error = "the peer's copy no longer matches its source after an interruption; refusing to overwrite — replan"
		}
	case model.ItemFailed, model.ItemCanceled:
		x.rearm(it)
	}
	return nil
}
