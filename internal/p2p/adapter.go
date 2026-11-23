package p2p

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/Lunar-Park/lp-ferry/internal/checksum"
	"github.com/Lunar-Park/lp-ferry/internal/engine"
	"github.com/Lunar-Park/lp-ferry/internal/planner"
	"github.com/Lunar-Park/lp-ferry/internal/receipt"
	"github.com/Lunar-Park/lp-ferry/internal/verify"
)

// Remote adapts a Node to the planner's and the engine's view of other
// machines.
type Remote struct{ N *Node }

var (
	_ planner.Remote = Remote{}
	_ engine.Remote  = Remote{}
)

func (r Remote) client(ctx context.Context, peer string) (*Client, error) {
	return r.N.Client(ctx, peer)
}

// Inventory scans a folder on a source peer.
func (r Remote) Inventory(ctx context.Context, peer, path, label string) (*planner.Inventory, error) {
	c, err := r.client(ctx, peer)
	if err != nil {
		return nil, err
	}
	var inv planner.Inventory
	if err := c.JSON(ctx, http.MethodPost, "/v1/inventory", inventoryReq{Path: path, Label: label}, &inv); err != nil {
		return nil, err
	}
	inv.PeerID = peer
	return &inv, nil
}

// Preflight validates a destination root on a peer.
func (r Remote) Preflight(ctx context.Context, peer, root string) (*planner.RemoteDest, error) {
	c, err := r.client(ctx, peer)
	if err != nil {
		return nil, err
	}
	var out planner.RemoteDest
	return &out, c.JSON(ctx, http.MethodPost, "/v1/preflight", rootReq{Root: root}, &out)
}

// List reads one destination directory on a peer for conflict planning.
func (r Remote) List(ctx context.Context, peer, root, dir string) (map[string]string, error) {
	c, err := r.client(ctx, peer)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	return out, c.JSON(ctx, http.MethodPost, "/v1/list", rootReq{Root: root, Dir: dir}, &out)
}

type push struct{ p *Pusher }

func (p push) Write(b []byte) (int, error) { return p.p.Write(b) }
func (p push) Abort(err error)             { p.p.Abort(err) }
func (p push) Finish(sum string) (engine.RemoteVerdict, error) {
	res, err := p.p.Finish(sum)
	return engine.RemoteVerdict{Status: res.Status, SourceHash: sum, DestHash: res.DestHash, ReadbackMode: res.ReadbackMode,
		PublishStrategy: res.PublishStrategy, MtimePreserved: res.MtimePreserved, Error: res.Error}, err
}

// Push streams one item to a destination peer.
func (r Remote) Push(ctx context.Context, peer, tid string, itemID int64, root, destRel string, size, mtimeNS int64, algo string) (engine.RemotePush, error) {
	c, err := r.client(ctx, peer)
	if err != nil {
		return nil, err
	}
	if _, ok := c.Radar.Active(); !ok {
		if _, err := c.Radar.Select(ctx); err != nil {
			return nil, err
		}
	}
	p, err := c.push(ctx, tid, strconv.FormatInt(itemID, 10), root, destRel, size, mtimeNS, algo)
	if err != nil {
		return nil, err
	}
	return push{p}, nil
}

// requestGrant asks the source peer to authorize grantee for items.
func (r Remote) requestGrant(ctx context.Context, src *Client, tid, grantee string, items []engine.PullItem) (string, error) {
	req := GrantRequest{TransferID: tid, Grantee: grantee, Items: map[string]GrantItem{}}
	for _, it := range items {
		req.Items[strconv.FormatInt(it.ItemID, 10)] = GrantItem{Root: it.SourceRoot, Rel: it.SourceRel, Size: it.Size, MtimeNS: it.MtimeNS}
	}
	var out map[string]string
	if err := src.JSON(ctx, http.MethodPost, "/v1/grants", req, &out); err != nil {
		return "", err
	}
	if out["grant"] == "" {
		return "", errors.New("source peer issued no grant")
	}
	return out["grant"], nil
}

func landItems(items []engine.PullItem) []LandItem {
	out := make([]LandItem, 0, len(items))
	for _, it := range items {
		algo := it.ChecksumAlgo
		// Legacy landing peers strictly decode JSON and implicitly use SHA-256.
		if algo == checksum.SHA256 {
			algo = ""
		}
		out = append(out, LandItem{ChecksumAlgo: algo, ItemID: strconv.FormatInt(it.ItemID, 10), DestRel: it.DestRel, Size: it.Size, MtimeNS: it.MtimeNS})
	}
	return out
}

func hooksFor(h engine.PullHooks) LandHooks {
	id := func(s string) int64 { v, _ := strconv.ParseInt(s, 10, 64); return v }
	return LandHooks{
		Progress: func(item string, n int64) {
			if h.Progress != nil {
				h.Progress(id(item), n)
			}
		},
		BeforePublish: func(item, sum string) error {
			if h.BeforePublish != nil {
				return h.BeforePublish(id(item), sum)
			}
			return nil
		},
	}
}

func verdicts(rs []LandResult) []engine.RemoteVerdict {
	out := make([]engine.RemoteVerdict, 0, len(rs))
	for _, r := range rs {
		id, _ := strconv.ParseInt(r.ItemID, 10, 64)
		out = append(out, engine.RemoteVerdict{ItemID: id, Status: r.Status, SourceHash: r.SourceHash, DestHash: r.DestHash,
			ReadbackMode: r.ReadbackMode, PublishStrategy: r.PublishStrategy, MtimePreserved: r.MtimePreserved, Error: r.Error})
	}
	return out
}

// Pull fetches items from a source peer into a local root.
func (r Remote) Pull(ctx context.Context, sourcePeer, tid, root string, items []engine.PullItem, h engine.PullHooks) ([]engine.RemoteVerdict, error) {
	src, err := r.client(ctx, sourcePeer)
	if err != nil {
		return nil, err
	}
	grant, err := r.requestGrant(ctx, src, tid, r.N.Identity.ID, items)
	if err != nil {
		return nil, err
	}
	return verdicts(r.N.Land(ctx, src, tid, grant, root, landItems(items), hooksFor(h))), nil
}

// Land has destPeer pull directly from sourcePeer.
func (r Remote) Land(ctx context.Context, destPeer, sourcePeer, tid, root string, items []engine.PullItem, h engine.PullHooks) ([]engine.RemoteVerdict, error) {
	src, err := r.client(ctx, sourcePeer)
	if err != nil {
		return nil, err
	}
	grant, err := r.requestGrant(ctx, src, tid, destPeer, items)
	if err != nil {
		return nil, err
	}
	dst, err := r.client(ctx, destPeer)
	if err != nil {
		return nil, err
	}
	rs, err := dst.LandRemote(ctx, tid, LandRequest{SourcePeer: sourcePeer, Grant: grant, Root: root, Items: landItems(items)}, hooksFor(h))
	return verdicts(rs), err
}

// Mkdir creates a retained empty directory on a peer.
func (r Remote) Mkdir(ctx context.Context, peer, tid, root, rel string) error {
	c, err := r.client(ctx, peer)
	if err != nil {
		return err
	}
	return c.JSON(ctx, http.MethodPost, "/v1/transfers/"+tid+"/mkdir", rootReq{Root: root, Dir: rel}, nil)
}

// VerifyRemote re-verifies a receipt target on the peer that holds it,
// with that peer's own physical readback.
func (r Remote) VerifyRemote(ctx context.Context, peer string, rc *receipt.Receipt, targetID, root string) (*verify.Report, error) {
	c, err := r.client(ctx, peer)
	if err != nil {
		return nil, err
	}
	body, err := rc.Marshal()
	if err != nil {
		return nil, err
	}
	var rep verify.Report
	return &rep, c.JSON(ctx, http.MethodPost, "/v1/verify", VerifyRequest{Receipt: body, TargetID: targetID, Root: root}, &rep)
}

// Check re-verifies specific files on a peer.
func (r Remote) Check(ctx context.Context, peer, root, algo string, files map[string]string) (map[string]string, error) {
	rc := &receipt.Receipt{Schema: receipt.Schema, SchemaVersion: receipt.SchemaVersion, ChecksumAlgo: algo,
		Targets: []receipt.Target{{TargetID: "t", RootPath: root}}}
	for rel, sum := range files {
		rc.Files = append(rc.Files, receipt.File{TargetID: "t", DestRelPath: rel, DestHash: sum, Action: receipt.ActionCopied, ChecksumAlgo: algo})
	}
	rep, err := r.VerifyRemote(ctx, peer, rc, "t", root)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for rel := range files {
		out[rel] = "ok"
	}
	for _, t := range rep.Targets {
		for _, p := range t.Missing {
			out[p] = "missing"
		}
		for _, p := range t.Modified {
			out[p] = "modified"
		}
		for _, p := range t.Unreadable {
			out[p.Path] = "unreadable"
		}
	}
	return out, nil
}
