package planner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Lunar-Park/lp-ferry/internal/identity"
	"github.com/Lunar-Park/lp-ferry/internal/ledger"
	"github.com/Lunar-Park/lp-ferry/internal/metadata"
	"github.com/Lunar-Park/lp-ferry/internal/model"
	"github.com/Lunar-Park/lp-ferry/internal/preset"
	"github.com/Lunar-Park/lp-ferry/internal/storage"
)

// RemoteDest is what a destination peer reports at preflight.
type RemoteDest struct {
	Root            string `json:"root"`
	Exists          bool   `json:"exists"`
	Writable        bool   `json:"writable"`
	FreeBytes       int64  `json:"free_bytes"`
	PublishStrategy string `json:"publish_strategy"`
	IdentityJSON    string `json:"identity_json"`
	StorageClass    string `json:"storage_class"`
	Error           string `json:"error,omitempty"`
}

// Remote reaches paired peers for remote sources and destinations. A nil
// Remote means only local locations can be planned.
type Remote interface {
	Inventory(ctx context.Context, peerID, path, label string) (*Inventory, error)
	Preflight(ctx context.Context, peerID, root string) (*RemoteDest, error)
	// List returns fold(name) -> "kind\x00name" for a destination directory.
	List(ctx context.Context, peerID, root, dirRel string) (map[string]string, error)
}

// Planner builds, approves and revises plans.
type Planner struct {
	DB      *ledger.DB
	Presets *preset.Service
	Prober  identity.Prober
	Remote  Remote

	mu    sync.Mutex
	trees map[string]*treeNode
}

// ErrBlocked is returned by Approve for a plan with blocking findings.
var ErrBlocked = errors.New("plan has blocking findings")

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

func (p *Planner) prober() identity.Prober {
	if p.Prober != nil {
		return p.Prober
	}
	return identity.System()
}

// Plan builds a draft plan from a request and persists it.
func (p *Planner) Plan(ctx context.Context, req model.PlanRequest) (*model.Plan, error) {
	return p.build(ctx, req, "")
}

type destCtx struct {
	pd       model.PlanDestination
	root     *storage.Root
	peerID   string
	rootPath string
	preset   *preset.Revision
	policy   string
	findings []model.Finding
}

func local(peer string) bool { return peer == "" || peer == model.LocalPeer }

func (p *Planner) build(ctx context.Context, req model.PlanRequest, derivedFrom string) (*model.Plan, error) {
	if len(req.Sources) == 0 || len(req.Destinations) == 0 {
		return nil, errors.New("a plan needs at least one source and one destination")
	}
	if req.Actor == "" {
		req.Actor = "human"
	}
	if req.ChecksumAlgo == "" {
		req.ChecksumAlgo = "sha256"
	}
	if err := p.Presets.EnsureBuiltins(ctx); err != nil {
		return nil, err
	}
	reqJSON, _ := json.Marshal(req)
	plan := &model.Plan{ID: newID("pln_"), Actor: req.Actor, ChecksumAlgo: req.ChecksumAlgo, Status: model.PlanDraft,
		RequestJSON: string(reqJSON), DerivedFrom: derivedFrom}

	// 1. Inventory every source completely.
	var invs []*Inventory
	for _, s := range req.Sources {
		var inv *Inventory
		var err error
		if local(s.PeerID) {
			inv, err = Scan(ctx, s.Path, s.Label)
		} else if p.Remote != nil {
			inv, err = p.Remote.Inventory(ctx, s.PeerID, s.Path, s.Label)
		} else {
			err = fmt.Errorf("source on peer %s: remote planning is not available", s.PeerID)
		}
		if err != nil {
			return nil, fmt.Errorf("scanning source %s: %w", s.Path, err)
		}
		inv.ID = newID("inv_")
		invs = append(invs, inv)
	}
	if err := checkOverlap(req, invs); err != nil {
		return nil, err
	}

	// 2. Camera roles: per-source overrides, then persistent bindings.
	bindings, err := ledger.CameraRoles(ctx, p.DB.Read(), metadata.CameraKey)
	if err != nil {
		return nil, err
	}
	cameraKeys := map[string]bool{}
	roleOf := func(inv *Inventory, m metadata.Media) string {
		if k := m.CameraKey(); k != "" {
			cameraKeys[k] = true
		}
		if r := req.CameraRoles[inv.Label]; r != "" {
			return r
		}
		return bindings[m.CameraKey()]
	}

	// 3. Each destination: resolve, route, place, size.
	for di, ds := range req.Destinations {
		dc, err := p.resolveDest(ctx, req, plan.ID, di, ds)
		if err != nil {
			return nil, err
		}
		var items []*item
		for si, inv := range invs {
			items = append(items, p.route(dc, req, si, inv, roleOf)...)
		}
		items = pruneImplied(items)
		p.place(ctx, dc, items)
		var required int64
		for _, it := range items {
			if it.entry.Action == model.ActionCopy {
				required += it.entry.Size
			}
		}
		dc.pd.RequiredBytes = required
		if dc.root != nil || !local(dc.peerID) {
			if need := required + dc.pd.ReserveBytes; need > dc.pd.FreeBytes {
				dc.findings = append(dc.findings, model.Finding{Severity: model.SeverityBlocking, Kind: "insufficient_space", PlanDestID: dc.pd.ID,
					Message: fmt.Sprintf("%s needs %d bytes (+%d reserve) but has %d free", dc.pd.Label, required, dc.pd.ReserveBytes, dc.pd.FreeBytes)})
			}
		}
		plan.Destinations = append(plan.Destinations, dc.pd)
		plan.Findings = append(plan.Findings, dc.findings...)
		for _, it := range items {
			plan.Entries = append(plan.Entries, it.entry)
			if it.entry.Warning != "" && it.entry.Action != model.ActionExclude {
				plan.Findings = append(plan.Findings, model.Finding{Severity: model.SeverityWarning, Kind: "routing", PlanDestID: dc.pd.ID,
					SourceRel: it.inv.Label + ":" + it.scan.Rel, DestRel: it.entry.DestRelPath, Message: it.entry.Warning})
			}
		}
	}
	keys := make([]string, 0, len(cameraKeys))
	for k := range cameraKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	plan.Fingerprint = fingerprint(plan, invs, req, keys)

	err = p.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, inv := range invs {
			if err := insertInventory(tx, inv); err != nil {
				return err
			}
		}
		if err := ledger.InsertPlan(tx, plan); err != nil {
			return err
		}
		return ledger.RecordCameraKeys(tx, plan.ID, keys)
	})
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func insertInventory(tx *sql.Tx, inv *Inventory) error {
	rows := make([]ledger.InventoryRow, 0, len(inv.Entries))
	for _, e := range inv.Entries {
		st := "ok"
		switch {
		case e.Err != "":
			st = "error"
		case e.Artifact:
			st = "artifact"
		}
		rows = append(rows, ledger.InventoryRow{Rel: e.Rel, Type: e.Type, Status: st, Error: e.Err, Size: e.Size, MtimeNS: e.MtimeNS,
			MetadataJSON: inv.Media[e.Rel].JSON()})
	}
	card := ""
	if inv.Card != nil {
		b, _ := json.Marshal(inv.Card)
		card = string(b)
	}
	return ledger.InsertInventory(tx, inv.ID, inv.PeerID, inv.Root, inv.Label, inv.ManifestHash, card, inv.Files, inv.Bytes, inv.Errors, rows)
}

func within(p, root string) bool {
	return p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/")
}

// checkOverlap refuses destination-inside-source, source-inside-
// destination, equal paths and nested sources. It compares directories by
// device and inode, walking up to the nearest existing ancestor, so
// symlinks, firmlinks and not-yet-created folders cannot hide an overlap.
func checkOverlap(req model.PlanRequest, invs []*Inventory) error {
	var srcs []string
	for i, inv := range invs {
		if local(req.Sources[i].PeerID) {
			srcs = append(srcs, inv.Root)
		}
	}
	for i := range srcs {
		for j := range srcs {
			if i != j && insideByInode(srcs[i], srcs[j]) {
				return fmt.Errorf("%w: source %s is inside source %s; choose one non-overlapping selection", errSourceOverlap, srcs[i], srcs[j])
			}
		}
	}
	for _, d := range req.Destinations {
		if !local(d.PeerID) || d.Path == "" {
			continue
		}
		for _, s := range srcs {
			if insideByInode(d.Path, s) || insideByInode(s, d.Path) {
				return fmt.Errorf("%w: destination %s and source %s overlap", errSourceOverlap, d.Path, s)
			}
		}
	}
	return nil
}

// insideByInode reports whether path a is (or will be, once created)
// inside directory b.
func insideByInode(a, b string) bool {
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	for p := filepath.Clean(a); ; p = filepath.Dir(p) {
		if ai, err := os.Stat(p); err == nil && os.SameFile(ai, bi) {
			return true
		}
		if parent := filepath.Dir(p); parent == p {
			return false
		}
	}
}

func (p *Planner) resolveDest(ctx context.Context, req model.PlanRequest, planID string, di int, ds model.DestinationSpec) (*destCtx, error) {
	dc := &destCtx{peerID: ds.PeerID, rootPath: ds.Path}
	if dc.peerID == "" {
		dc.peerID = model.LocalPeer
	}
	pd := model.PlanDestination{ID: fmt.Sprintf("%s_d%d", planID, di+1), PeerID: dc.peerID, Label: ds.Label, StorageClass: string(identity.ClassUnknown)}
	presetID, presetRev := req.PresetID, req.PresetRev
	policy := req.ConflictPolicy
	block := func(kind, msg string) {
		dc.findings = append(dc.findings, model.Finding{Severity: model.SeverityBlocking, Kind: kind, PlanDestID: pd.ID, Message: msg})
	}
	if ds.DestinationID != "" {
		saved, err := ledger.GetDestination(ctx, p.DB.Read(), ds.DestinationID)
		if err != nil {
			return nil, err
		}
		pd.DestinationID, pd.ReserveBytes = saved.ID, saved.ReserveBytes
		if pd.Label == "" {
			pd.Label = saved.Name
		}
		if presetID == "" {
			presetID, presetRev = saved.PinnedPresetID, saved.PinnedPresetRev
		}
		if policy == "" {
			policy = saved.DefaultConflict
		}
		dc.peerID, pd.PeerID = saved.PeerID, saved.PeerID
		if local(saved.PeerID) {
			obs, _ := identity.Observe(ctx, p.prober())
			res := identity.Resolve(identity.Saved{
				Identity: identity.VolumeIdentity{Kind: saved.IdentityKind, Value: saved.IdentityValue, Confidence: saved.IdentityConfidence},
				SubPath:  saved.RelSubpath, LastBinding: saved.RootPath}, obs)
			switch {
			case res.State == identity.Available:
				dc.rootPath = res.Binding
			case res.State == identity.NeedsConfirmation && ds.ConfirmBinding && len(res.Candidates) == 1:
				dc.rootPath = res.Candidates[0]
				dc.findings = append(dc.findings, model.Finding{Severity: model.SeverityWarning, Kind: "binding_confirmed", PlanDestID: pd.ID,
					Message: "binding confirmed explicitly for this plan: " + res.Reason})
			default:
				dc.rootPath = ""
				block("destination_"+res.State, fmt.Sprintf("saved destination %s is %s: %s", saved.Name, res.State, res.Reason))
			}
		} else {
			dc.rootPath = saved.RootPath
		}
	}
	if presetID == "" {
		presetID = preset.BuiltinPreserve
	}
	rev, err := p.Presets.Get(ctx, presetID, presetRev)
	if err != nil {
		return nil, err
	}
	dc.preset = rev
	if policy == "" {
		policy = rev.Preset.ConflictPolicy
	}
	dc.policy = policy
	pd.PresetID, pd.PresetRev, pd.PresetHash, pd.ConflictPolicy = rev.PresetID, rev.Revision, rev.Hash, policy
	pd.RootPath = dc.rootPath
	if pd.Label == "" {
		pd.Label = filepath.Base(dc.rootPath)
	}

	switch {
	case dc.rootPath == "":
	case !local(dc.peerID):
		if p.Remote == nil {
			return nil, fmt.Errorf("destination on peer %s: remote planning is not available", dc.peerID)
		}
		rd, err := p.Remote.Preflight(ctx, dc.peerID, dc.rootPath)
		if err != nil {
			return nil, fmt.Errorf("preflight on %s: %w", dc.peerID, err)
		}
		pd.FreeBytes, pd.PublishStrategy, pd.IdentityJSON, pd.StorageClass = rd.FreeBytes, rd.PublishStrategy, rd.IdentityJSON, rd.StorageClass
		if rd.Error != "" || !rd.Exists || !rd.Writable {
			block("destination_unusable", fmt.Sprintf("%s on %s: %s", dc.rootPath, dc.peerID, firstNonEmpty(rd.Error, "not an existing writable folder")))
		}
	default:
		root, err := storage.OpenRoot(dc.rootPath)
		if err != nil {
			block("destination_missing", fmt.Sprintf("destination folder %s is not usable: %v (Ferry does not create a destination folder on its own)", dc.rootPath, err))
			break
		}
		dc.root = root
		pd.RootPath = root.Real
		if info, _, err := identity.Snapshot(ctx, p.prober(), root.Real); err == nil {
			b, _ := json.Marshal(info)
			pd.IdentityJSON, pd.StorageClass = string(b), string(info.Class)
		}
		if free, err := storage.FreeBytes(root.Real); err == nil {
			pd.FreeBytes = free
		} else {
			block("unknown_free_space", err.Error())
		}
		dir, err := storage.OpenDir(root, nil, false)
		if err == nil {
			var s storage.Strategy
			s, err = storage.ProbeStrategy(dir)
			_ = dir.Close()
			pd.PublishStrategy = string(s)
		}
		if err != nil {
			block("publication_unsupported", err.Error())
		}
	}
	dc.pd = pd
	return dc, nil
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

// route evaluates the preset for one source on one destination.
func (p *Planner) route(dc *destCtx, req model.PlanRequest, si int, inv *Inventory, roleOf func(*Inventory, metadata.Media) string) []*item {
	userExcl := map[string]string{}
	for _, x := range req.Exclude {
		if x.SourceLabel == inv.Label {
			userExcl[strings.Trim(x.Rel, "/")] = firstNonEmpty(x.Reason, "excluded by reviewer")
		}
	}
	userExcluded := func(rel string) (string, bool) {
		for r := rel; r != "." && r != ""; r = path.Dir(r) {
			if reason, ok := userExcl[r]; ok {
				return reason, true
			}
		}
		return "", false
	}
	scans := map[string]ScanEntry{}
	var entries []preset.Entry
	var pre []*item
	artifactDirs := []string{}
	for _, e := range inv.Entries {
		scans[e.Rel] = e
		base := model.PlanEntry{PlanDestID: dc.pd.ID, SourceRoot: inv.Root, SourceRel: e.Rel, SourcePath: inv.Root + "/" + e.Rel,
			SourceLabel: inv.Label, SourcePeer: inv.PeerID, Size: e.Size, MtimeNS: e.MtimeNS, Kind: e.Type}
		if e.Type != TypeDir {
			base.Kind = "file"
		}
		inArtifact := false
		for _, d := range artifactDirs {
			if within(e.Rel, d) {
				inArtifact = true
			}
		}
		if reason, ok := userExcluded(e.Rel); ok {
			base.Action, base.ExclusionReason, base.ExcludedByUser = model.ActionExclude, reason, true
			pre = append(pre, &item{src: si, inv: inv, scan: e, entry: base})
			continue
		}
		if e.Artifact || inArtifact {
			if e.Type == TypeDir {
				artifactDirs = append(artifactDirs, e.Rel)
			}
			base.Action, base.ExclusionReason, base.MatchedRule = model.ActionExclude, "system artifact", "scanner:system_artifact"
			pre = append(pre, &item{src: si, inv: inv, scan: e, entry: base})
			continue
		}
		m := inv.Media[e.Rel]
		entries = append(entries, preset.Entry{Rel: e.Rel, IsDir: e.Type == TypeDir, Size: e.Size, MtimeNS: e.MtimeNS,
			Media: m, CameraRole: roleOf(inv, m), Companion: m.CompanionOf != ""})
	}
	out := pre
	for _, r := range preset.Evaluate(dc.preset.Preset, inv.Label, entries) {
		e := scans[r.Rel]
		m := inv.Media[r.Rel]
		en := model.PlanEntry{PlanDestID: dc.pd.ID, SourceRoot: inv.Root, SourceRel: e.Rel, SourcePath: inv.Root + "/" + e.Rel,
			SourceLabel: inv.Label, SourcePeer: inv.PeerID, Size: e.Size, MtimeNS: e.MtimeNS, Kind: "file", DestRelPath: r.DestRel,
			MatchedRule: r.Rule, GroupID: r.GroupID, Warning: r.Warning, MetadataJSON: m.JSON(), CameraRole: roleOf(inv, m)}
		if e.Type == TypeDir {
			en.Kind = TypeDir
		}
		it := &item{src: si, inv: inv, scan: e, r: r, entry: en}
		switch r.Action {
		case preset.RouteExclude:
			en.Action, en.ExclusionReason, en.DestRelPath = model.ActionExclude, r.ExclusionReason, ""
			it.entry = en
		case preset.RouteNeedsReview:
			it.entry = en
			it.entry.Action = model.ActionNeedsReview
			dc.findings = append(dc.findings, model.Finding{Severity: model.SeverityBlocking, Kind: "routing_failed", PlanDestID: dc.pd.ID,
				SourceRel: inv.Label + ":" + e.Rel, Message: r.Warning})
		case preset.RouteDir:
			it.entry.Action = model.ActionDir
		default:
			it.entry.Action = model.ActionCopy
		}
		// Objects Ferry cannot copy faithfully block until excluded.
		if it.entry.Action != model.ActionExclude {
			switch {
			case e.Err != "":
				it.entry.Action = model.ActionNeedsReview
				dc.findings = append(dc.findings, model.Finding{Severity: model.SeverityBlocking, Kind: "scan_error", PlanDestID: dc.pd.ID,
					SourceRel: inv.Label + ":" + e.Rel, Message: e.Err})
			case e.Type == TypeSymlink || e.Type == TypeOther:
				it.entry.Action = model.ActionNeedsReview
				it.entry.ConflictKind = "unsupported_object"
				dc.findings = append(dc.findings, model.Finding{Severity: model.SeverityBlocking, Kind: "unsupported_object", PlanDestID: dc.pd.ID,
					SourceRel: inv.Label + ":" + e.Rel, Message: e.Rel + " is a " + e.Type + "; Ferry does not follow or recreate it — exclude it or choose another source folder"})
			}
		}
		out = append(out, it)
	}
	return out
}

// pruneImplied drops directory entries a written descendant will create
// anyway; genuinely empty directories (and folders whose every child was
// excluded) keep their entry (spec §6.2).
func pruneImplied(items []*item) []*item {
	writes := map[string]bool{} // "src\x00rel" of every ancestor of a written entry
	mark := func(src int, rel string) {
		for d := path.Dir(rel); d != "." && d != "/"; d = path.Dir(d) {
			writes[fmt.Sprint(src)+"\x00"+d] = true
		}
	}
	var dirs []*item
	for _, it := range items {
		switch it.entry.Action {
		case model.ActionCopy, model.ActionSkipIdentical, model.ActionNeedsReview:
			if it.scan.Type != TypeDir {
				mark(it.src, it.scan.Rel)
			}
		case model.ActionDir:
			dirs = append(dirs, it)
		}
	}
	sort.SliceStable(dirs, func(i, j int) bool {
		return strings.Count(dirs[i].scan.Rel, "/") > strings.Count(dirs[j].scan.Rel, "/")
	})
	drop := map[*item]bool{}
	for _, d := range dirs {
		if writes[fmt.Sprint(d.src)+"\x00"+d.scan.Rel] {
			drop[d] = true
			continue
		}
		mark(d.src, d.scan.Rel)
	}
	out := items[:0]
	for _, it := range items {
		if !drop[it] {
			out = append(out, it)
		}
	}
	return out
}

// place runs the conflict matrix: groups first (whole subtrees), then
// ordinary files in (source, path) order, then retained directories.
func (p *Planner) place(ctx context.Context, dc *destCtx, items []*item) {
	a := newAllocator(ctx, dc.root, dc.policy, "sha256", dc.pd.ID)
	if dc.root == nil && !local(dc.peerID) && p.Remote != nil {
		peer, root := dc.peerID, dc.rootPath
		a.lister = func(dirRel string) map[string]string {
			l, err := p.Remote.List(ctx, peer, root, dirRel)
			if err != nil {
				return map[string]string{}
			}
			return l
		}
		a.remote = true
	}
	groups := map[string][]*item{}
	var gkeys []string
	var files, dirs []*item
	for _, it := range items {
		switch {
		case it.entry.GroupID != "" && (it.entry.Action == model.ActionCopy || it.entry.Action == model.ActionDir || it.entry.Action == model.ActionExclude):
			k := fmt.Sprintf("%06d\x00%s", it.src, it.entry.GroupID)
			if _, ok := groups[k]; !ok {
				gkeys = append(gkeys, k)
			}
			groups[k] = append(groups[k], it)
		case it.entry.Action == model.ActionCopy:
			files = append(files, it)
		case it.entry.Action == model.ActionDir:
			dirs = append(dirs, it)
		}
	}
	sort.Strings(gkeys)
	for _, k := range gkeys {
		members := groups[k]
		sort.Slice(members, func(i, j int) bool { return members[i].scan.Rel < members[j].scan.Rel })
		live := false
		for _, m := range members {
			if m.entry.Action != model.ActionExclude {
				live = true
			}
		}
		if live {
			a.placeGroup(members)
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].src != files[j].src {
			return files[i].src < files[j].src
		}
		return files[i].scan.Rel < files[j].scan.Rel
	})
	for _, it := range files {
		a.placeFile(it)
	}
	for _, it := range dirs {
		a.placeDir(it)
	}
	dc.findings = append(dc.findings, a.findings...)
}

type fpEntry struct {
	Dest, Label, Src, Action, DestRel, Rule, Group, Conflict, Expected, Excl string
	Size, Mtime                                                              int64
}

// fingerprint hashes everything approval vouches for: source snapshots,
// destination bindings, preset revisions, policies, every mapping and
// action, camera roles and the bindings they came from, and exclusions.
func fingerprint(plan *model.Plan, invs []*Inventory, req model.PlanRequest, cameraKeys []string) string {
	type fpSource struct{ Peer, Root, Label, Manifest string }
	type fpDest struct {
		ID, Dest, Peer, Root, Identity, Preset, Hash, Policy, Strategy string
		Rev                                                            int
		Reserve                                                        int64
	}
	doc := struct {
		Algo    string
		Sources []fpSource
		Dests   []fpDest
		Entries []fpEntry
		Roles   map[string]string
		Cameras []string
		Exclude []model.UserExclusion
	}{Algo: plan.ChecksumAlgo, Roles: req.CameraRoles, Cameras: cameraKeys, Exclude: req.Exclude}
	for _, inv := range invs {
		doc.Sources = append(doc.Sources, fpSource{inv.PeerID, inv.Root, inv.Label, inv.ManifestHash})
	}
	for i, d := range plan.Destinations {
		doc.Dests = append(doc.Dests, fpDest{fmt.Sprint(i), d.DestinationID, d.PeerID, d.RootPath, d.IdentityJSON, d.PresetID,
			d.PresetHash, d.ConflictPolicy, d.PublishStrategy, d.PresetRev, d.ReserveBytes})
	}
	destIdx := map[string]string{}
	for i, d := range plan.Destinations {
		destIdx[d.ID] = fmt.Sprint(i)
	}
	for _, e := range plan.Entries {
		doc.Entries = append(doc.Entries, fpEntry{destIdx[e.PlanDestID], e.SourceLabel, e.SourceRel, e.Action, e.DestRelPath, e.MatchedRule,
			e.GroupID, e.ConflictKind, e.ExpectedHash, e.ExclusionReason, e.Size, e.MtimeNS})
	}
	sort.Slice(doc.Entries, func(i, j int) bool {
		a, b := doc.Entries[i], doc.Entries[j]
		if a.Dest != b.Dest {
			return a.Dest < b.Dest
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.Src < b.Src
	})
	b, _ := json.Marshal(doc)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// Approve approves a draft plan under exactly the fingerprint reviewed,
// after a fresh preflight: changed sources or newly occupied destination
// paths invalidate the plan instead of being approved over.
func (p *Planner) Approve(ctx context.Context, planID, fp string) error {
	plan, err := ledger.GetPlan(ctx, p.DB.Read(), planID)
	if err != nil {
		return err
	}
	if (plan.Status != model.PlanDraft && plan.Status != model.PlanAwaiting) || plan.Fingerprint != fp {
		return fmt.Errorf("%w: plan %s is %s (fingerprint %s)", ledger.ErrStaleApproval, planID, plan.Status, plan.Fingerprint)
	}
	if b := plan.Blocking(); len(b) > 0 {
		msgs := make([]string, 0, len(b))
		for _, f := range b {
			msgs = append(msgs, f.Kind+": "+f.Message)
		}
		return fmt.Errorf("%w: %s", ErrBlocked, strings.Join(msgs, "; "))
	}
	for _, e := range plan.Entries {
		if e.Action == model.ActionNeedsReview {
			return fmt.Errorf("%w: %s:%s needs review (%s)", ErrBlocked, e.SourceLabel, e.SourceRel, e.ConflictKind)
		}
	}
	if reason := p.preflight(plan); reason != "" {
		_ = p.DB.WriteTx(ctx, func(tx *sql.Tx) error { return ledger.InvalidatePlan(tx, planID, reason, "") })
		return fmt.Errorf("%w: %s; replan", ledger.ErrStaleApproval, reason)
	}
	return p.DB.WriteTx(ctx, func(tx *sql.Tx) error { return ledger.ApprovePlan(tx, planID, fp) })
}

func (p *Planner) preflight(plan *model.Plan) string {
	var req model.PlanRequest
	_ = json.Unmarshal([]byte(plan.RequestJSON), &req)
	localSource := map[string]bool{}
	for _, s := range req.Sources {
		if local(s.PeerID) {
			localSource[firstNonEmpty(s.Label, filepath.Base(s.Path))] = true
		}
	}
	roots := map[string]*storage.Root{}
	for _, d := range plan.Destinations {
		if local(d.PeerID) && d.RootPath != "" {
			r, err := storage.OpenRoot(d.RootPath)
			if err != nil {
				return "destination " + d.Label + " is no longer usable: " + err.Error()
			}
			roots[d.ID] = r
		}
	}
	checked := map[string]bool{}
	for _, e := range plan.Entries {
		if e.Action != model.ActionCopy && e.Action != model.ActionSkipIdentical {
			continue
		}
		if localSource[e.SourceLabel] && !checked[e.SourcePath] {
			checked[e.SourcePath] = true
			fi, err := os.Lstat(e.SourcePath)
			if err != nil || fi.Size() != e.Size || fi.ModTime().UnixNano() != e.MtimeNS {
				return "source " + e.SourceLabel + ":" + e.SourceRel + " changed since planning"
			}
		}
		if root := roots[e.PlanDestID]; root != nil && e.Action == model.ActionCopy {
			dir, leaf, err := storage.OpenFileIn(root, e.DestRelPath, false)
			if err == nil {
				ferr := dir.CheckFree(leaf)
				_ = dir.Close()
				if ferr != nil {
					return "destination path " + e.DestRelPath + " is no longer free: " + ferr.Error()
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return "destination path " + e.DestRelPath + ": " + err.Error()
			}
		}
	}
	return ""
}

// Revision changes what a reviewer may adjust after preview.
type Revision struct {
	CameraRoles    map[string]string     `json:"camera_roles,omitempty"`
	Exclude        []model.UserExclusion `json:"exclude,omitempty"`
	ConflictPolicy string                `json:"conflict_policy,omitempty"`
	SourceLabels   map[int]string        `json:"source_labels,omitempty"`
}

// Revise builds a successor plan with the reviewer's changes and
// invalidates the original, so two approvable plans never coexist.
func (p *Planner) Revise(ctx context.Context, planID string, rv Revision) (*model.Plan, error) {
	old, err := ledger.GetPlan(ctx, p.DB.Read(), planID)
	if err != nil {
		return nil, err
	}
	if old.Status != model.PlanDraft && old.Status != model.PlanApproved && old.Status != model.PlanAwaiting {
		return nil, fmt.Errorf("plan %s is %s and cannot be revised", planID, old.Status)
	}
	var req model.PlanRequest
	if err := json.Unmarshal([]byte(old.RequestJSON), &req); err != nil {
		return nil, err
	}
	if len(rv.CameraRoles) > 0 && req.CameraRoles == nil {
		req.CameraRoles = map[string]string{}
	}
	for k, v := range rv.CameraRoles {
		if v == "" {
			delete(req.CameraRoles, k)
		} else {
			req.CameraRoles[k] = v
		}
	}
	req.Exclude = append(req.Exclude, rv.Exclude...)
	if rv.ConflictPolicy != "" {
		req.ConflictPolicy = rv.ConflictPolicy
	}
	for i, l := range rv.SourceLabels {
		if i >= 0 && i < len(req.Sources) {
			req.Sources[i].Label = l
		}
	}
	next, err := p.build(ctx, req, planID)
	if err != nil {
		return nil, err
	}
	err = p.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		return ledger.InvalidatePlan(tx, planID, "superseded by "+next.ID, next.ID)
	})
	return next, err
}

// EntriesPage is one page of plan entries.
type EntriesPage struct {
	Entries    []model.PlanEntry `json:"entries"`
	Total      int64             `json:"total"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// Entry page limits.
const (
	DefaultPageSize = 200
	MaxPageSize     = 1000
)

// Entries pages a plan's entries in stable order; no limit truncates the
// plan itself, only the page.
func (p *Planner) Entries(ctx context.Context, planID, destID, cursor string, limit int) (*EntriesPage, error) {
	if limit <= 0 {
		limit = DefaultPageSize
	}
	limit = min(limit, MaxPageSize)
	var after int64
	if cursor != "" {
		if _, err := fmt.Sscan(cursor, &after); err != nil {
			return nil, fmt.Errorf("bad cursor %q", cursor)
		}
	}
	es, total, err := ledger.ListPlanEntries(ctx, p.DB.Read(), planID, destID, after, limit)
	if err != nil {
		return nil, err
	}
	page := &EntriesPage{Entries: es, Total: total}
	if len(es) == limit {
		page.NextCursor = fmt.Sprint(es[len(es)-1].ID)
	}
	return page, nil
}

// SaveDestinationRequest saves or edits a destination.
type SaveDestinationRequest struct {
	ID              string `json:"id,omitempty"`
	Name            string `json:"name"`
	Path            string `json:"path"`
	PresetID        string `json:"preset_id,omitempty"`
	PresetRev       int    `json:"preset_rev,omitempty"`
	DefaultConflict string `json:"default_conflict,omitempty"`
	ChecksumAlgo    string `json:"checksum_algo,omitempty"`
	ReserveBytes    int64  `json:"reserve_bytes,omitempty"`
	Archived        bool   `json:"archived,omitempty"`
}

// SaveDestination records a destination with the identity evidence of the
// storage it lives on. Editing one invalidates its pending plans.
func (p *Planner) SaveDestination(ctx context.Context, r SaveDestinationRequest) (*model.SavedDestination, error) {
	real, err := filepath.EvalSymlinks(r.Path)
	if err != nil {
		return nil, err
	}
	info, mp, err := identity.Snapshot(ctx, p.prober(), real)
	if err != nil {
		return nil, err
	}
	sub, ok := identity.RelWithin(mp, real)
	if !ok {
		return nil, fmt.Errorf("%s is not inside its reported mount point %s", real, mp)
	}
	kind := "volume_folder"
	switch {
	case info.Identity != nil && info.Identity.Kind == identity.KindServerShare:
		kind = "mounted_share_folder"
	case mp == "/":
		kind = "local_folder"
	}
	d := &model.SavedDestination{ID: r.ID, Name: r.Name, PeerID: model.LocalPeer, LocationKind: kind, RootPath: real,
		RelSubpath: sub, PinnedPresetID: r.PresetID, PinnedPresetRev: r.PresetRev,
		DefaultConflict: firstNonEmpty(r.DefaultConflict, preset.KeepBoth), ChecksumAlgo: firstNonEmpty(r.ChecksumAlgo, "sha256"),
		ReserveBytes: r.ReserveBytes, Archived: r.Archived}
	if info.Identity != nil {
		d.IdentityKind, d.IdentityValue, d.IdentityConfidence = info.Identity.Kind, info.Identity.Value, info.Identity.Confidence
	} else {
		pi := identity.PathOnly(mp)
		d.IdentityKind, d.IdentityValue, d.IdentityConfidence = pi.Kind, pi.Value, pi.Confidence
	}
	if d.ID == "" {
		d.ID = newID("dst_")
	}
	if d.Name == "" {
		d.Name = filepath.Base(real)
	}
	err = p.DB.WriteTx(ctx, func(tx *sql.Tx) error { _, err := ledger.SaveDestination(tx, d); return err })
	return d, err
}

// BindCamera sets a camera's default role, invalidating pending plans
// that were routed with its previous role.
func (p *Planner) BindCamera(ctx context.Context, make, model, serial, role string) error {
	return p.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		return ledger.BindCamera(tx, ledger.CameraBinding{Make: make, Model: model, Serial: serial, DefaultRole: role},
			metadata.CameraKey(make, model, serial))
	})
}
