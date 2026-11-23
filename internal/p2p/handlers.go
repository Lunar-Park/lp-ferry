package p2p

import (
	"cmp"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/checksum"
	"github.com/Lunar-Park/lp-ferry/internal/config"
	"github.com/Lunar-Park/lp-ferry/internal/identity"
	"github.com/Lunar-Park/lp-ferry/internal/planner"
	"github.com/Lunar-Park/lp-ferry/internal/receipt"
	"github.com/Lunar-Park/lp-ferry/internal/storage"
	"github.com/Lunar-Park/lp-ferry/internal/verify"
)

func (n *Node) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/pair", n.handlePair)
	mux.HandleFunc("GET /v1/probe", n.handleProbe)
	mux.HandleFunc("GET /v1/roots", n.handleRoots)
	mux.HandleFunc("POST /v1/inventory", n.handleInventory)
	mux.HandleFunc("POST /v1/preflight", n.handlePreflight)
	mux.HandleFunc("POST /v1/list", n.handleList)
	mux.HandleFunc("POST /v1/grants", n.handleGrant)
	mux.HandleFunc("GET /v1/transfers/{tid}/items/{item}", n.handleItem)
	mux.HandleFunc("PUT /v1/transfers/{tid}/stream/{item}", n.handleStream)
	mux.HandleFunc("POST /v1/transfers/{tid}/land", n.handleLand)
	mux.HandleFunc("POST /v1/verify", n.handleVerify)
	mux.HandleFunc("POST /v1/transfers/{tid}/mkdir", n.handleMkdir)
	return n.auth(mux)
}

// auth admits pinned peers everywhere and an unpinned key only to the
// pairing endpoint while a pairing window is open.
func (n *Node) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, pub, err := PeerFromState(r.TLS)
		if err != nil {
			http.Error(w, "mutual TLS required", http.StatusForbidden)
			return
		}
		if !n.pinned(id, pub) {
			if r.URL.Path != "/v1/pair" || !n.pairingOpen() {
				http.Error(w, "unpaired peer", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

// errForbidden is a 403: outside allowed roots, or not granted.
var errForbidden = errors.New("forbidden")

func (n *Node) roots() []config.Root {
	if n.Roots == nil {
		return nil
	}
	return n.Roots()
}

// allowedRoot returns the configured root containing path (resolved),
// requiring rw when write is set.
func (n *Node) allowedRoot(path string, write bool) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %q is not absolute", errForbidden, path)
	}
	real := filepath.Clean(path)
	if r, err := filepath.EvalSymlinks(path); err == nil {
		real = r
	}
	for _, root := range n.roots() {
		rr := filepath.Clean(root.Path)
		if r, err := filepath.EvalSymlinks(root.Path); err == nil {
			rr = r
		}
		if real == rr || strings.HasPrefix(real, rr+string(filepath.Separator)) {
			if write && root.Access != config.AccessReadWrite {
				return "", fmt.Errorf("%w: %s is read-only on this machine", errForbidden, root.Path)
			}
			return real, nil
		}
	}
	return "", fmt.Errorf("%w: %s is outside this machine's allowed roots", errForbidden, path)
}

func httpErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, errForbidden), errors.Is(err, ErrGrantInvalid), errors.Is(err, ErrGrantExpired),
		errors.Is(err, ErrWrongPeer), errors.Is(err, ErrWrongItem), errors.Is(err, storage.ErrUnsafePath):
		code = http.StatusForbidden
	case errors.Is(err, os.ErrNotExist):
		code = http.StatusNotFound
	case errors.Is(err, storage.ErrSourceChanged), errors.Is(err, storage.ErrDestinationExists):
		code = http.StatusConflict
	}
	http.Error(w, err.Error(), code)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func (n *Node) handleProbe(w http.ResponseWriter, r *http.Request) {
	if b, _ := strconv.ParseInt(r.URL.Query().Get("bytes"), 10, 64); b > 0 {
		b = min(b, 8<<20)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(b, 10))
		_, _ = io.Copy(w, randomPayload(b))
		return
	}
	writeJSON(w, ProbeInfo{PeerID: n.Identity.ID, Name: n.Name, Capabilities: []string{"inventory", "preflight", "grants", "pull", "push", "land", "verify", "range"}})
}

// RootInfo is one exported root.
type RootInfo struct {
	Path      string `json:"path"`
	Access    string `json:"access"`
	FreeBytes int64  `json:"free_bytes"`
	Available bool   `json:"available"`
}

func (n *Node) handleRoots(w http.ResponseWriter, _ *http.Request) {
	out := []RootInfo{}
	for _, r := range n.roots() {
		ri := RootInfo{Path: r.Path, Access: r.Access}
		if free, err := storage.FreeBytes(r.Path); err == nil {
			ri.FreeBytes, ri.Available = free, true
		}
		out = append(out, ri)
	}
	writeJSON(w, out)
}

type inventoryReq struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

func (n *Node) handleInventory(w http.ResponseWriter, r *http.Request) {
	var req inventoryReq
	if !decode(w, r, &req) {
		return
	}
	real, err := n.allowedRoot(req.Path, false)
	if err != nil {
		httpErr(w, err)
		return
	}
	inv, err := planner.Scan(r.Context(), real, req.Label)
	if err != nil {
		httpErr(w, err)
		return
	}
	inv.PeerID = n.Identity.ID
	writeJSON(w, inv)
}

type rootReq struct {
	Root string `json:"root"`
	Dir  string `json:"dir,omitempty"`
}

func (n *Node) handlePreflight(w http.ResponseWriter, r *http.Request) {
	var req rootReq
	if !decode(w, r, &req) {
		return
	}
	out := planner.RemoteDest{Root: req.Root}
	real, err := n.allowedRoot(req.Root, true)
	if err != nil {
		out.Error = err.Error()
		writeJSON(w, out)
		return
	}
	root, err := storage.OpenRoot(real)
	if err != nil {
		out.Error = err.Error()
		writeJSON(w, out)
		return
	}
	out.Root, out.Exists = root.Real, true
	if info, _, err := identity.Snapshot(r.Context(), identity.System(), root.Real); err == nil {
		b, _ := json.Marshal(info)
		out.IdentityJSON, out.StorageClass = string(b), string(info.Class)
	}
	out.FreeBytes, _ = storage.FreeBytes(root.Real)
	dir, err := storage.OpenDir(root, nil, false)
	if err == nil {
		var s storage.Strategy
		s, err = storage.ProbeStrategy(dir)
		_ = dir.Close()
		out.PublishStrategy = string(s)
	}
	if err != nil {
		out.Error = err.Error()
	} else {
		out.Writable = true
	}
	writeJSON(w, out)
}

func (n *Node) handleList(w http.ResponseWriter, r *http.Request) {
	var req rootReq
	if !decode(w, r, &req) {
		return
	}
	real, err := n.allowedRoot(req.Root, false)
	if err != nil {
		httpErr(w, err)
		return
	}
	out := map[string]string{}
	root, err := storage.OpenRoot(real)
	if err == nil {
		var parts []string
		if req.Dir != "" {
			parts = strings.Split(req.Dir, "/")
		}
		if d, err := storage.OpenDir(root, parts, false); err == nil {
			entries, _ := os.ReadDir(d.DirPath())
			_ = d.Close()
			for _, e := range entries {
				kind := planner.TypeOther
				switch t := e.Type(); {
				case t.IsRegular():
					kind = planner.TypeFile
				case t.IsDir():
					kind = planner.TypeDir
				case t&os.ModeSymlink != 0:
					kind = planner.TypeSymlink
				}
				out[strings.ToLower(e.Name())] = kind + "\x00" + e.Name()
			}
		} else if errors.Is(err, storage.ErrUnsafePath) {
			// Report the unsafe component as a symlink so the planner blocks.
			out["\x00unsafe"] = planner.TypeSymlink + "\x00" + req.Dir
		}
	}
	writeJSON(w, out)
}

// GrantRequest asks a source peer to authorize a grantee for items.
type GrantRequest struct {
	TransferID string               `json:"transfer_id"`
	Grantee    string               `json:"grantee"`
	TTLSeconds int                  `json:"ttl_seconds"`
	Items      map[string]GrantItem `json:"items"`
}

func (n *Node) handleGrant(w http.ResponseWriter, r *http.Request) {
	var req GrantRequest
	if !decode(w, r, &req) {
		return
	}
	tok, err := n.IssueGrant(req)
	if err != nil {
		httpErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"grant": tok})
}

// IssueGrant validates every item against allowed roots and the current
// file, then signs a grant. Items are checked now so a grant never names a
// file outside a root or one that already changed.
func (n *Node) IssueGrant(req GrantRequest) (string, error) {
	if req.TransferID == "" || req.Grantee == "" || len(req.Items) == 0 {
		return "", errors.New("grant needs a transfer, a grantee and items")
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl <= 0 || ttl > 24*time.Hour {
		ttl = 6 * time.Hour
	}
	for id, it := range req.Items {
		real, err := n.allowedRoot(it.Root, false)
		if err != nil {
			return "", fmt.Errorf("item %s: %w", id, err)
		}
		f, _, err := openSource(real, it)
		if err != nil {
			return "", fmt.Errorf("item %s: %w", id, err)
		}
		_ = f.Close()
		it.Root = real
		req.Items[id] = it
	}
	return SignGrant(n.Identity, Grant{TransferID: req.TransferID, Grantee: req.Grantee, ExpiresAt: time.Now().Add(ttl), Items: req.Items})
}

// openSource opens a granted file under its root without following links
// and checks it is still the file that was granted.
func openSource(root string, it GrantItem) (*os.File, storage.FileInfo, error) {
	sroot, err := storage.OpenRoot(root)
	if err != nil {
		return nil, storage.FileInfo{}, err
	}
	dir, leaf, err := storage.OpenFileIn(sroot, it.Rel, false)
	if err != nil {
		return nil, storage.FileInfo{}, err
	}
	f, err := dir.OpenRead(leaf)
	_ = dir.Close()
	if err != nil {
		return nil, storage.FileInfo{}, err
	}
	fi, err := storage.Fstat(f)
	if err != nil {
		_ = f.Close()
		return nil, fi, err
	}
	if fi.Size != it.Size || fi.MtimeNS != it.MtimeNS {
		_ = f.Close()
		return nil, fi, fmt.Errorf("%w: %s (size %d->%d)", storage.ErrSourceChanged, it.Rel, it.Size, fi.Size)
	}
	return f, fi, nil
}

// handleItem streams a granted file. Every request is checked: mTLS peer
// == grantee, signature is ours, unexpired, item granted, path inside an
// allowed root (re-checked with lstat containment). Range "bytes=N-"
// resumes; the trailer carries the SHA-256 of the whole file.
func (n *Node) handleItem(w http.ResponseWriter, r *http.Request) {
	g, err := VerifyGrant(n.Identity, r.Header.Get("X-Ferry-Grant"), PeerOf(r), time.Now())
	if err != nil {
		httpErr(w, err)
		return
	}
	if g.TransferID != r.PathValue("tid") {
		httpErr(w, fmt.Errorf("%w: grant is for transfer %s", ErrGrantInvalid, g.TransferID))
		return
	}
	it, err := g.Lookup(r.PathValue("item"))
	if err != nil {
		httpErr(w, err)
		return
	}
	real, err := n.allowedRoot(it.Root, false)
	if err != nil {
		httpErr(w, err)
		return
	}
	f, before, err := openSource(real, it)
	if err != nil {
		httpErr(w, err)
		return
	}
	defer func() { _ = f.Close() }()
	var offset int64
	if rg := r.Header.Get("Range"); rg != "" {
		v, ok := strings.CutPrefix(rg, "bytes=")
		v, ok2 := strings.CutSuffix(v, "-")
		off, err := strconv.ParseInt(v, 10, 64)
		if !ok || !ok2 || err != nil || off < 0 || off > before.Size {
			http.Error(w, "unsatisfiable range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		offset = off
	}
	storage.AdviseSource(f)
	algo := cmp.Or(r.Header.Get("X-Ferry-Checksum"), checksum.SHA256)
	h, err := checksum.New(algo)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// The prefix the receiver already holds is hashed here, locally, so the
	// trailer is always the hash of the whole file.
	if offset > 0 {
		if _, err := io.CopyN(h, f, offset); err != nil {
			httpErr(w, err)
			return
		}
	}
	w.Header().Set("Trailer", hashTrailer(algo)+", X-Ferry-Error")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(before.Size-offset, 10))
	if offset > 0 {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, before.Size-1, before.Size))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	buf := make([]byte, 2<<20)
	if _, err := io.CopyBuffer(io.MultiWriter(w, h), io.LimitReader(f, before.Size-offset), buf); err != nil {
		return // the client went away; nothing was published anywhere
	}
	after, err := storage.Fstat(f)
	if err != nil || after.Size != before.Size || after.MtimeNS != before.MtimeNS || after.Ino != before.Ino {
		w.Header().Set("X-Ferry-Error", "source changed during send")
		return
	}
	w.Header().Set(hashTrailer(algo), hex.EncodeToString(h.Sum(nil)))
}

// PushResult is a destination's verdict on a pushed item.
type PushResult struct {
	Status          string `json:"status"` // verified | failed
	DestHash        string `json:"dest_hash,omitempty"`
	ReadbackMode    string `json:"readback_mode,omitempty"`
	PublishStrategy string `json:"publish_strategy,omitempty"`
	MtimePreserved  bool   `json:"mtime_preserved"`
	Error           string `json:"error,omitempty"`
}

func jobPart(leaf, tid, item string) string {
	if len(leaf) > 150 {
		leaf = leaf[:150]
	}
	return "." + leaf + "." + tid + "-" + item + storage.TempSuffix
}

// handleStream receives a pushed file into an allowed rw root with the
// same discipline as a local copy: job-owned temp, fsync, physical
// readback compared with the sender's trailer hash, binding recheck,
// no-clobber publication.
func (n *Node) handleStream(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tid, item := r.PathValue("tid"), r.PathValue("item")
	size, _ := strconv.ParseInt(q.Get("size"), 10, 64)
	mtime, _ := strconv.ParseInt(q.Get("mtime_ns"), 10, 64)
	real, err := n.allowedRoot(q.Get("root"), true)
	if err != nil {
		httpErr(w, err)
		return
	}
	algo := cmp.Or(r.Header.Get("X-Ferry-Checksum"), checksum.SHA256)
	if !checksum.Valid(algo) {
		http.Error(w, "unsupported checksum", http.StatusBadRequest)
		return
	}
	res := n.receive(r.Context(), r.Body, func() string { return r.Trailer.Get(hashTrailer(algo)) }, real, q.Get("rel"), tid, item, size, mtime, 0, algo)
	writeJSON(w, res)
}

// receive writes body into a job-owned temp under root/rel and publishes
// it if the readback matches wantHash(). resumeFrom > 0 appends to an
// existing job-owned temp instead of creating it.
func (n *Node) receive(ctx context.Context, body io.Reader, wantHash func() string, rootPath, rel, tid, item string, size, mtime, resumeFrom int64, algo string) PushResult {
	fail := func(err error) PushResult { return PushResult{Status: "failed", Error: err.Error()} }
	root, err := storage.OpenRoot(rootPath)
	if err != nil {
		return fail(err)
	}
	dir, leaf, err := storage.OpenFileIn(root, rel, true)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.CheckFree(leaf); err != nil {
		return fail(fmt.Errorf("%w; never overwritten", err))
	}
	part := jobPart(leaf, tid, item)
	if resumeFrom == 0 {
		_ = dir.Remove(part) // only ever this transfer item's own temp
	}
	f, err := dir.OpenAppend(part)
	if err != nil {
		return fail(err)
	}
	cleanup := func() { _ = f.Close(); _ = dir.Remove(part) }
	nw, err := io.CopyBuffer(f, body, make([]byte, 2<<20))
	if err != nil {
		cleanup()
		return fail(fmt.Errorf("stream interrupted after %d bytes: %w", nw, err))
	}
	if resumeFrom+nw != size {
		cleanup()
		return fail(fmt.Errorf("received %d of %d bytes", resumeFrom+nw, size))
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return fail(err)
	}
	_ = f.Close()
	return n.finishPart(ctx, dir, part, leaf, wantHash(), mtime, algo)
}

// verifyPart hashes a temp by physical readback.
func verifyPart(ctx context.Context, dir *storage.Dir, part, algo string) (string, string, error) {
	rb, mode, err := dir.OpenReadback(part)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = rb.Close() }()
	h, err := checksum.New(algo)
	if err != nil {
		return "", "", err
	}
	if _, err := io.CopyBuffer(h, ctxReader{ctx, rb}, make([]byte, 2<<20)); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(h.Sum(nil)), mode, nil
}

// publishPart rechecks the binding, preserves mtime and publishes without
// replacing anything. On failure the temp (and any own reservation) is
// removed.
func publishPart(dir *storage.Dir, part, leaf string, mtime int64) PushResult {
	fail := func(err error) PushResult {
		_ = dir.Remove(part)
		return PushResult{Status: "failed", Error: err.Error()}
	}
	if err := dir.CheckBinding(); err != nil {
		return fail(err)
	}
	preserved := dir.Chtimes(part, mtime, mtime) == nil
	strategy, err := storage.ProbeStrategy(dir)
	if err != nil {
		return fail(err)
	}
	var res *storage.Reservation
	if strategy == storage.StrategyReserveRename {
		if res, err = storage.Reserve(dir, leaf); err != nil {
			return fail(err)
		}
	}
	if err := storage.Publish(dir, part, leaf, strategy, res); err != nil {
		if res != nil && storage.OwnsReservation(dir, leaf, res.Ino) {
			_ = dir.Remove(leaf)
		}
		return fail(err)
	}
	return PushResult{Status: "verified", PublishStrategy: string(strategy), MtimePreserved: preserved}
}

// finishPart: physical readback, compare with the sender's hash, publish.
func (n *Node) finishPart(ctx context.Context, dir *storage.Dir, part, leaf, want string, mtime int64, algo string) PushResult {
	fail := func(err error) PushResult {
		_ = dir.Remove(part)
		return PushResult{Status: "failed", Error: err.Error()}
	}
	if want == "" {
		return fail(errors.New("sender sent no checksum; not published"))
	}
	got, mode, err := verifyPart(ctx, dir, part, algo)
	if err != nil {
		return fail(err)
	}
	if got != want {
		return fail(fmt.Errorf("%w: sender %s, readback %s; not published", storage.ErrChecksumMismatch, want, got))
	}
	r := publishPart(dir, part, leaf, mtime)
	r.DestHash, r.ReadbackMode = got, mode
	return r
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

// LandRequest asks this node to pull items from a source peer.
type LandRequest struct {
	SourcePeer string     `json:"source_peer"`
	Grant      string     `json:"grant"`
	Root       string     `json:"root"`
	Items      []LandItem `json:"items"`
}

// LandEvent is one NDJSON line of a landing job.
type LandEvent struct {
	Event      string      `json:"event"` // progress | publishing | result
	ItemID     string      `json:"item_id"`
	Bytes      int64       `json:"bytes,omitempty"`
	SourceHash string      `json:"source_hash,omitempty"`
	Result     *LandResult `json:"result,omitempty"`
}

func (n *Node) handleLand(w http.ResponseWriter, r *http.Request) {
	var req LandRequest
	if !decode(w, r, &req) {
		return
	}
	real, err := n.allowedRoot(req.Root, true)
	if err != nil {
		httpErr(w, err)
		return
	}
	src, err := n.Client(r.Context(), req.SourcePeer)
	if err != nil {
		httpErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	fl, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	send := func(ev LandEvent) {
		_ = enc.Encode(ev)
		if fl != nil {
			fl.Flush()
		}
	}
	tid := r.PathValue("tid")
	results := n.Land(r.Context(), src, tid, req.Grant, real, req.Items, LandHooks{
		Progress: func(id string, b int64) { send(LandEvent{Event: "progress", ItemID: id, Bytes: b}) },
		BeforePublish: func(id, sum string) error {
			send(LandEvent{Event: "publishing", ItemID: id, SourceHash: sum})
			return nil
		},
	})
	for i := range results {
		send(LandEvent{Event: "result", ItemID: results[i].ItemID, Result: &results[i]})
	}
}

// VerifyRequest asks this node to re-verify landed files in an allowed root.
type VerifyRequest struct {
	Receipt  json.RawMessage `json:"receipt"`
	TargetID string          `json:"target_id"`
	Root     string          `json:"root"`
}

func (n *Node) handleVerify(w http.ResponseWriter, r *http.Request) {
	var req VerifyRequest
	if !decode(w, r, &req) {
		return
	}
	real, err := n.allowedRoot(req.Root, false)
	if err != nil {
		httpErr(w, err)
		return
	}
	var rc receipt.Receipt
	if err := json.Unmarshal(req.Receipt, &rc); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rep, err := verify.Verify(r.Context(), &rc, verify.Options{TargetID: req.TargetID, Root: real})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, rep)
}

func (n *Node) handleMkdir(w http.ResponseWriter, r *http.Request) {
	var req rootReq
	if !decode(w, r, &req) {
		return
	}
	real, err := n.allowedRoot(req.Root, true)
	if err != nil {
		httpErr(w, err)
		return
	}
	parts, err := storage.ValidateRel(req.Dir)
	if err != nil {
		httpErr(w, err)
		return
	}
	root, err := storage.OpenRoot(real)
	if err != nil {
		httpErr(w, err)
		return
	}
	d, err := storage.OpenDir(root, parts, true)
	if err != nil {
		httpErr(w, err)
		return
	}
	_ = d.Close()
	writeJSON(w, map[string]string{"status": "ok"})
}
