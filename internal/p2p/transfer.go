package p2p

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Lunar-Park/lp-ferry/internal/checksum"
	"github.com/Lunar-Park/lp-ferry/internal/storage"
)

// LandItem is one file to pull.
type LandItem struct {
	ChecksumAlgo string `json:"checksum_algo,omitempty"`
	ItemID       string `json:"item_id"`
	DestRel      string `json:"dest_rel"`
	Size         int64  `json:"size"`
	MtimeNS      int64  `json:"mtime_ns"`
}

// LandResult is the outcome of pulling one item.
type LandResult struct {
	ItemID          string `json:"item_id"`
	Status          string `json:"status"` // verified | failed | canceled
	SourceHash      string `json:"source_hash,omitempty"`
	DestHash        string `json:"dest_hash,omitempty"`
	ReadbackMode    string `json:"readback_mode,omitempty"`
	PublishStrategy string `json:"publish_strategy,omitempty"`
	MtimePreserved  bool   `json:"mtime_preserved"`
	Error           string `json:"error,omitempty"`
	Resumes         int    `json:"resumes"`
	FirstOffset     int64  `json:"first_offset"`
	ResumeOffsets   []int64
}

// LandHooks observe a landing job.
type LandHooks struct {
	Progress      func(itemID string, bytes int64)
	BeforePublish func(itemID, sourceHash string) error
}

// maxResumes bounds how many times one item reconnects.
const maxResumes = 8

// Land pulls items from src into root, directly, resuming each from its
// job-owned temp at the exact byte offset after a dropped connection
// (failing over to the next path when Path Radar has one) and verifying
// the whole file against the source's selected checksum before publication.
func (n *Node) Land(ctx context.Context, src *Client, transferID, grant, root string, items []LandItem, h LandHooks) []LandResult {
	out := make([]LandResult, 0, len(items))
	for _, it := range items {
		out = append(out, n.landOne(ctx, src, transferID, grant, root, it, h))
	}
	return out
}

func (n *Node) landOne(ctx context.Context, src *Client, tid, grant, rootPath string, it LandItem, h LandHooks) LandResult {
	it.ChecksumAlgo = cmp.Or(it.ChecksumAlgo, checksum.SHA256)
	res := LandResult{ItemID: it.ItemID, Status: "failed"}
	fail := func(err error) LandResult { res.Error = err.Error(); return res }
	if !checksum.Valid(it.ChecksumAlgo) {
		return fail(fmt.Errorf("unsupported checksum %q", it.ChecksumAlgo))
	}
	root, err := storage.OpenRoot(rootPath)
	if err != nil {
		return fail(err)
	}
	dir, leaf, err := storage.OpenFileIn(root, it.DestRel, true)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.CheckFree(leaf); err != nil {
		return fail(fmt.Errorf("%w; never overwritten", err))
	}
	part := jobPart(leaf, tid, it.ItemID)
	var offset int64
	if fi, err := dir.Lstat(part); err == nil {
		if fi.IsRegular() && fi.Size <= it.Size {
			offset = fi.Size // our own earlier attempt: resume, don't restart
		} else {
			_ = dir.Remove(part)
		}
	}
	res.FirstOffset = offset
	restarted := false
	for {
		sum, err := n.pull(ctx, src, tid, grant, dir, part, it, &offset, &res, h)
		if err != nil {
			if ctx.Err() != nil {
				res.Status = "canceled" // the temp stays: a later resume continues it
				return fail(err)
			}
			_ = dir.Remove(part)
			return fail(err)
		}
		got, mode, err := verifyPart(ctx, dir, part, it.ChecksumAlgo)
		if err != nil {
			_ = dir.Remove(part)
			return fail(err)
		}
		if got != sum {
			_ = dir.Remove(part)
			if res.Resumes > 0 && !restarted {
				// The kept prefix did not match the source after all: restart
				// the file from zero once; never publish a spliced file.
				restarted, offset = true, 0
				continue
			}
			return fail(fmt.Errorf("%w: source %s, readback %s; not published", storage.ErrChecksumMismatch, sum, got))
		}
		res.SourceHash, res.ReadbackMode = sum, mode
		if h.BeforePublish != nil {
			if err := h.BeforePublish(it.ItemID, sum); err != nil {
				return fail(err)
			}
		}
		pr := publishPart(dir, part, leaf, it.MtimeNS)
		if pr.Status != "verified" {
			return fail(errors.New(pr.Error))
		}
		res.Status, res.DestHash, res.PublishStrategy, res.MtimePreserved = "verified", got, pr.PublishStrategy, pr.MtimePreserved
		return res
	}
}

// pull fetches from *offset to EOF into part, reconnecting on network
// errors, and returns the source's whole-file checksum trailer.
func (n *Node) pull(ctx context.Context, src *Client, tid, grant string, dir *storage.Dir, part string, it LandItem, offset *int64, res *LandResult, h LandHooks) (string, error) {
	for attempt := 0; ; attempt++ {
		if *offset == 0 {
			_ = dir.Remove(part)
		}
		resp, err := src.fetch(ctx, tid, it.ItemID, grant, *offset, it.ChecksumAlgo)
		if err != nil {
			if !netError(err) || attempt >= maxResumes {
				return "", err
			}
			if ferr := src.Failover(ctx); ferr != nil {
				return "", fmt.Errorf("%w (after %w)", ferr, err)
			}
			continue
		}
		if *offset > 0 && resp.StatusCode != http.StatusPartialContent {
			// The source ignored the range: start over cleanly.
			_ = resp.Body.Close()
			*offset = 0
			continue
		}
		f, err := dir.OpenAppend(part)
		if err != nil {
			_ = resp.Body.Close()
			return "", err
		}
		nw, cerr := io.CopyBuffer(progressWriter{f, func(b int64) {
			if h.Progress != nil {
				h.Progress(it.ItemID, b)
			}
		}}, resp.Body, make([]byte, 2<<20))
		*offset += nw
		serr := f.Sync()
		_ = f.Close()
		trailer, terr := resp.Trailer.Get(hashTrailer(it.ChecksumAlgo)), resp.Trailer.Get("X-Ferry-Error")
		_ = resp.Body.Close()
		if serr != nil {
			return "", serr
		}
		if cerr == nil && terr != "" {
			return "", fmt.Errorf("%w: %s", storage.ErrSourceChanged, terr)
		}
		if cerr == nil && trailer == "" && it.ChecksumAlgo != checksum.SHA256 {
			return "", fmt.Errorf("source peer did not return a %s checksum", it.ChecksumAlgo)
		}
		if cerr == nil && trailer != "" && *offset == it.Size {
			return trailer, nil
		}
		if cerr == nil {
			cerr = fmt.Errorf("stream ended at %d of %d bytes without a checksum", *offset, it.Size)
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if attempt >= maxResumes {
			return "", cerr
		}
		// Connection dropped mid-file: keep the temp, fail over, resume at
		// the exact offset already on disk.
		res.Resumes++
		res.ResumeOffsets = append(res.ResumeOffsets, *offset)
		if ferr := src.Failover(ctx); ferr != nil {
			return "", fmt.Errorf("%w (after %w)", ferr, cerr)
		}
	}
}

type progressWriter struct {
	w  io.Writer
	fn func(int64)
}

func (p progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.fn(int64(n))
	return n, err
}

// Pusher streams one item to a destination peer. Write the bytes, then
// Finish with the source hash to receive the destination's verdict.
type Pusher struct {
	pw         *io.PipeWriter
	hashHeader string
	trailer    http.Header
	done       chan struct{}
	res        PushResult
	err        error
}

// Push opens a streaming PUT. The destination writes a job-owned temp,
// verifies it by physical readback against the hash sent as a trailer,
// and publishes without replacing anything.
func (c *Client) Push(ctx context.Context, tid, itemID, root, destRel string, size, mtimeNS int64) (*Pusher, error) {
	return c.push(ctx, tid, itemID, root, destRel, size, mtimeNS, checksum.SHA256)
}

func hashTrailer(algo string) string {
	return http.CanonicalHeaderKey("X-Ferry-" + algo)
}

func (c *Client) push(ctx context.Context, tid, itemID, root, destRel string, size, mtimeNS int64, algo string) (*Pusher, error) {
	pr, pw := io.Pipe()
	q := url.Values{}
	q.Set("root", root)
	q.Set("rel", destRel)
	q.Set("size", strconv.FormatInt(size, 10))
	q.Set("mtime_ns", strconv.FormatInt(mtimeNS, 10))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "https://ferry.peer/v1/transfers/"+tid+"/stream/"+itemID+"?"+q.Encode(), pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Ferry-Checksum", algo)
	req.ContentLength = -1
	p := &Pusher{pw: pw, hashHeader: hashTrailer(algo), trailer: http.Header{hashTrailer(algo): nil}, done: make(chan struct{})}
	req.Trailer = p.trailer
	go func() {
		defer close(p.done)
		resp, err := c.hc.Do(req)
		if err != nil {
			p.err = err
			_ = pr.CloseWithError(err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			p.err = &RemoteError{Status: resp.StatusCode, Msg: string(bytes.TrimSpace(msg))}
			_ = pr.CloseWithError(p.err)
			return
		}
		p.err = json.NewDecoder(resp.Body).Decode(&p.res)
	}()
	return p, nil
}

func (p *Pusher) Write(b []byte) (int, error) { return p.pw.Write(b) }

// Finish sends the trailer hash and waits for the verdict.
func (p *Pusher) Finish(sourceHash string) (PushResult, error) {
	p.trailer.Set(p.hashHeader, sourceHash)
	_ = p.pw.Close()
	<-p.done
	return p.res, p.err
}

// Abort breaks the stream; the destination discards its temp.
func (p *Pusher) Abort(err error) {
	_ = p.pw.CloseWithError(err)
	<-p.done
}

// LandRemote asks destPeer to pull items from sourcePeer (the coordinator
// carries no bytes) and relays its NDJSON events to h.
func (c *Client) LandRemote(ctx context.Context, tid string, req LandRequest, h LandHooks) ([]LandResult, error) {
	body, _ := json.Marshal(req)
	resp, err := c.Do(ctx, http.MethodPost, "/v1/transfers/"+tid+"/land", body, http.Header{"Content-Type": {"application/json"}})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out []LandResult
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var ev LandEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return out, err
		}
		switch ev.Event {
		case "progress":
			if h.Progress != nil {
				h.Progress(ev.ItemID, ev.Bytes)
			}
		case "publishing":
			if h.BeforePublish != nil {
				_ = h.BeforePublish(ev.ItemID, ev.SourceHash)
			}
		case "result":
			if ev.Result != nil {
				out = append(out, *ev.Result)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	if len(out) != len(req.Items) {
		return out, fmt.Errorf("landing on %s ended with %d of %d results", c.Peer, len(out), len(req.Items))
	}
	return out, nil
}
