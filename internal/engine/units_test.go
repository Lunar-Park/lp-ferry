package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/identity"
)

func TestEMAAndSeed(t *testing.T) {
	t0 := time.Unix(1000, 0)
	e := EMA{Seed: 500}
	e.Add(t0, 0)
	if e.Rate() != 500 {
		t.Fatalf("before real data the seed applies: %v", e.Rate())
	}
	e.Add(t0.Add(500*time.Millisecond), 100)
	e.Add(t0.Add(time.Second), 0) // closes bucket 1: 100 B/s
	if e.Rate() != 500 {
		t.Fatalf("one second of data still uses the seed: %v", e.Rate())
	}
	e.Add(t0.Add(1500*time.Millisecond), 200)
	e.Add(t0.Add(2*time.Second), 0) // bucket 2: 200 B/s -> 0.25*200 + 0.75*100
	if got := e.Rate(); got != 125 {
		t.Fatalf("EMA after two buckets = %v, want 125", got)
	}
	e.Add(t0.Add(4*time.Second), 0) // two idle seconds decay toward 0
	if got := e.Rate(); got >= 125*0.75 {
		t.Fatalf("a stall must lower the rate: %v", got)
	}
}

func TestETAIsTwoPhase(t *testing.T) {
	// 100 MB left to write at 50 MB/s plus 200 MB left to verify at 100 MB/s.
	if got := ETA(100e6, 200e6, 50e6, 100e6); got != 4 {
		t.Fatalf("ETA %v, want 4", got)
	}
	if ETA(1, 1, 1e9, 1e9) <= 0 {
		t.Fatal("remaining bytes must never show as done")
	}
	if ETA(0, 0, 0, 0) != 0 {
		t.Fatal("done is zero")
	}
	if ETA(1, 1, 0, 1) != -1 {
		t.Fatal("unknown rate must report unknown, not a guess")
	}
}

func TestTelemetrySnapshotAndCoalescing(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	tm := NewTelemetry("xfr_1", "agent:test", clock)
	tm.AddTarget(TargetProgress{TargetID: "a", Label: "SSD", TotalBytes: 10e9, TotalFiles: 2}, identity.ClassSSD)
	tm.AddTarget(TargetProgress{TargetID: "b", Label: "NAS", TotalBytes: 10e9, TotalFiles: 2}, identity.ClassNetwork)
	tm.Wrote("a", 10e9)
	tm.Verified("a", 5e9)
	tm.Phase("a", "clip.mov", PhaseVerifying)
	ev := tm.Snapshot()
	if ev.Actor != "agent:test" || ev.TotalBytes != 20e9 || ev.Targets[0].Phase != PhaseVerifying || ev.Targets[0].CurrentFile != "clip.mov" {
		t.Fatalf("%+v", ev)
	}
	if ev.OverallFraction != 15.0/40.0 {
		t.Fatalf("fraction %v", ev.OverallFraction)
	}
	// The slower target (network, nothing written) sets the transfer ETA.
	if ev.ETASeconds != ev.Targets[1].ETASeconds || ev.Targets[1].ETASeconds <= ev.Targets[0].ETASeconds {
		t.Fatalf("ETA %+v", ev)
	}

	// Real ticker: hammering counters must not produce one event per write.
	live := NewTelemetry("xfr_2", "human", nil)
	live.AddTarget(TargetProgress{TargetID: "a", TotalBytes: 1 << 40}, identity.ClassSSD)
	stop := make(chan struct{})
	var events atomic.Int32
	done := make(chan struct{})
	go func() { live.Emit(stop, func(Progress) { events.Add(1) }); close(done) }()
	deadline := time.Now().Add(600 * time.Millisecond)
	writes := 0
	for time.Now().Before(deadline) {
		live.Wrote("a", 4096)
		writes++
	}
	close(stop)
	<-done
	n := int(events.Load())
	if n < 3 || n > 6 {
		t.Fatalf("%d events in 600ms for %d writes; want ~4 (150ms coalescing + final)", n, writes)
	}
}

func TestLanes(t *testing.T) {
	if l := LanesFor(identity.ClassRemovable); l.Large != 1 {
		t.Fatalf("card reader must stream one large file at a time: %+v", l)
	}
	if l := LanesFor(identity.ClassSSD); l.Large < 2 || l.Small < 8 {
		t.Fatalf("NVMe needs depth: %+v", l)
	}
	if l := LanesForAll(identity.ClassSSD, identity.ClassHDD); l != LanesFor(identity.ClassHDD) {
		t.Fatalf("slowest device bounds concurrency: %+v", l)
	}
	if !IsLarge(32<<20) || IsLarge(32<<20-1) {
		t.Fatal("large threshold")
	}
}

type slowWriter struct {
	buf   bytes.Buffer
	delay time.Duration
}

func (s *slowWriter) Write(p []byte) (int, error) {
	time.Sleep(s.delay)
	return s.buf.Write(p)
}

// countingReader reports how far ahead of the slowest sink the reader got.
type countingReader struct {
	r    io.Reader
	read atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read.Add(int64(n))
	return n, err
}

func TestFanoutBackpressureBoundsMemory(t *testing.T) {
	data := randomBytes(40 * ChunkSize)
	src := &countingReader{r: bytes.NewReader(data)}
	fast := &bytes.Buffer{}
	slow := &slowWriter{delay: 3 * time.Millisecond}
	var maxLead int64
	res, err := Fanout(context.Background(), src, "sha256", []io.Writer{fast, slow}, func(i, n int) {
		if i == 1 {
			lead := src.read.Load() - int64(slow.buf.Len())
			if lead > maxLead {
				maxLead = lead
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if res.SourceHash != hex.EncodeToString(sum[:]) || !bytes.Equal(fast.Bytes(), data) || !bytes.Equal(slow.buf.Bytes(), data) {
		t.Fatal("fan-out output wrong")
	}
	// Queue depth (4) + one chunk being written + one being read.
	if limit := int64(SinkDepth+2) * ChunkSize; maxLead > limit {
		t.Fatalf("reader ran %d bytes ahead of the slow sink (limit %d)", maxLead, limit)
	}
}

func TestFanoutFailedSinkDetaches(t *testing.T) {
	data := randomBytes(8 * ChunkSize)
	good := &bytes.Buffer{}
	bad := writerFunc(func(p []byte) (int, error) { return 0, errors.New("disk gone") })
	res, err := Fanout(context.Background(), bytes.NewReader(data), "sha256", []io.Writer{good, bad}, nil)
	if err != nil || res.SinkErrs[1] == nil || res.SinkErrs[0] != nil || !bytes.Equal(good.Bytes(), data) {
		t.Fatalf("%v %+v", err, res.SinkErrs)
	}
	// Every sink failing is a fan-out error, not a hash of a partial read.
	_, err = Fanout(context.Background(), bytes.NewReader(data), "sha256", []io.Writer{bad}, nil)
	if err == nil {
		t.Fatal("all sinks failed but fan-out reported success")
	}
}

func TestFanoutCancelStopsWithinAChunk(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var chunks atomic.Int32
	w := writerFunc(func(p []byte) (int, error) {
		if chunks.Add(1) == 3 {
			cancel()
		}
		return len(p), nil
	})
	r := io.LimitReader(zeroReader{}, 1<<40) // "a 1 TB file"
	start := time.Now()
	_, err := Fanout(ctx, r, "sha256", []io.Writer{w}, nil)
	if !errors.Is(err, context.Canceled) || time.Since(start) > 2*time.Second {
		t.Fatalf("%v after %s", err, time.Since(start))
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
