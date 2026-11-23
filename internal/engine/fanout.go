package engine

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"

	"github.com/Lunar-Park/lp-ferry/internal/checksum"
)

// SinkDepth is how many chunks may queue per sink: 4 × 2 MiB = 8 MiB of
// buffering per destination. A slow sink paces the reader through its full
// channel; it never makes the reader buffer the whole file.
const SinkDepth = 4

// FanoutResult is the outcome of one single-read, N-sink copy.
type FanoutResult struct {
	SourceHash string
	Bytes      int64
	// SinkErrs[i] is sink i's write error. A failed sink is detached; the
	// others carry on.
	SinkErrs []error
}

type sinkLane struct {
	w       io.Writer
	ch      chan *chunk
	failed  atomic.Bool
	err     error
	written int64
}

// Fanout reads src exactly once in ChunkSize chunks, hashes it, and
// streams every chunk to all sinks concurrently. Cancellation is checked
// before every read, so a 100 GB file stops within one chunk. The returned
// error is for the source side (read failure or cancellation), in which
// case every sink's output is incomplete.
func Fanout(ctx context.Context, src io.Reader, algo string, sinks []io.Writer, onWrite func(sink int, n int)) (FanoutResult, error) {
	h, err := checksum.New(algo)
	if err != nil {
		return FanoutResult{}, err
	}
	lanes := make([]*sinkLane, len(sinks))
	var wg sync.WaitGroup
	for i, w := range sinks {
		l := &sinkLane{w: w, ch: make(chan *chunk, SinkDepth)}
		lanes[i] = l
		wg.Add(1)
		go func(i int, l *sinkLane) {
			defer wg.Done()
			for c := range l.ch {
				if !l.failed.Load() {
					n, err := l.w.Write(c.bytes())
					l.written += int64(n)
					if err == nil && n != c.n {
						err = io.ErrShortWrite
					}
					if err != nil {
						l.err = err
						l.failed.Store(true)
					} else if onWrite != nil {
						onWrite(i, n)
					}
				}
				c.release()
			}
		}(i, l)
	}

	var total int64
	var srcErr error
	for {
		if err := ctx.Err(); err != nil {
			srcErr = err
			break
		}
		c := getChunk()
		n, rerr := io.ReadFull(src, *c.buf)
		if n > 0 {
			c.n = n
			_, _ = h.Write(c.bytes())
			total += int64(n)
			live := 0
			for _, l := range lanes {
				if !l.failed.Load() {
					live++
				}
			}
			if live == 0 {
				bufPool.Put(c.buf)
				// Every sink failed; keep reading only to report an honest
				// error rather than a hash of a partial source.
				srcErr = errors.New("every destination failed")
				break
			}
			c.refs.Store(int32(live))
			sent := 0
			for _, l := range lanes {
				if sent == live || ctx.Err() != nil {
					break
				}
				if l.failed.Load() {
					continue
				}
				select {
				case l.ch <- c:
					sent++
				case <-ctx.Done():
				}
			}
			// A sink that failed after counting, or a cancellation mid-send,
			// leaves references nobody will take.
			for ; sent < live; sent++ {
				c.release()
			}
		} else {
			bufPool.Put(c.buf)
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			srcErr = rerr
			break
		}
	}
	for _, l := range lanes {
		close(l.ch)
	}
	wg.Wait()
	res := FanoutResult{Bytes: total, SinkErrs: make([]error, len(lanes))}
	for i, l := range lanes {
		res.SinkErrs[i] = l.err
	}
	if srcErr != nil {
		return res, srcErr
	}
	res.SourceHash = checksum.Hex(h)
	return res, nil
}
