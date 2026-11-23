package engine

import (
	"math"
	"sync"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/identity"
)

// EMAAlpha weights each new one-second bucket.
const EMAAlpha = 0.25

// ProgressInterval coalesces progress events: never one per chunk.
const ProgressInterval = 150 * time.Millisecond

// seedSeconds of real data must accumulate before the EMA replaces the seed.
const seedSeconds = 2

// EMA is an exponential moving average of throughput over 1-second buckets,
// seeded from the storage class until real data takes over.
type EMA struct {
	Seed        float64
	value       float64
	buckets     int
	bucketStart time.Time
	bucketBytes int64
}

// Add records n bytes at now, closing any whole seconds that have passed.
// Elapsed seconds with no bytes count as zero throughput: a stall shows up
// in the ETA instead of being averaged away.
func (e *EMA) Add(now time.Time, n int64) {
	if e.bucketStart.IsZero() {
		e.bucketStart = now
	}
	for now.Sub(e.bucketStart) >= time.Second {
		bps := float64(e.bucketBytes)
		if e.buckets == 0 {
			e.value = bps
		} else {
			e.value = EMAAlpha*bps + (1-EMAAlpha)*e.value
		}
		e.buckets++
		e.bucketBytes = 0
		e.bucketStart = e.bucketStart.Add(time.Second)
	}
	e.bucketBytes += n
}

// Rate is bytes/second: the seed until seedSeconds of real data exist.
func (e *EMA) Rate() float64 {
	if e.buckets < seedSeconds && e.Seed > 0 {
		return e.Seed
	}
	return e.value
}

// SeedRates returns (write, verify) seeds for a storage class.
func SeedRates(c identity.StorageClass) (float64, float64) {
	const mb = 1e6
	switch c {
	case identity.ClassSSD:
		return 1500 * mb, 2000 * mb
	case identity.ClassHDD:
		return 150 * mb, 160 * mb
	case identity.ClassRemovable:
		return 80 * mb, 90 * mb
	case identity.ClassNetwork:
		return 110 * mb, 110 * mb
	default:
		return 200 * mb, 250 * mb
	}
}

// ETA is honest two-phase arithmetic: what is left to write at the write
// rate plus what is left to verify at the verify rate. Every byte is
// written once and read back once. -1 means unknown.
func ETA(remainingWrite, remainingVerify int64, writeBPS, verifyBPS float64) float64 {
	if remainingWrite <= 0 && remainingVerify <= 0 {
		return 0
	}
	if (remainingWrite > 0 && writeBPS <= 0) || (remainingVerify > 0 && verifyBPS <= 0) {
		return -1
	}
	eta := 0.0
	if remainingWrite > 0 {
		eta += float64(remainingWrite) / writeBPS
	}
	if remainingVerify > 0 {
		eta += float64(remainingVerify) / verifyBPS
	}
	// Round up: remaining work never displays as "done".
	return math.Ceil(eta*10) / 10
}

// Phases a target reports.
const (
	PhaseHashing    = "hashing"
	PhaseStreaming  = "streaming"
	PhaseVerifying  = "verifying"
	PhasePublishing = "publishing"
	PhaseIdle       = "idle"
	PhaseDone       = "done"
)

// TargetProgress is one destination in a progress event.
type TargetProgress struct {
	TargetID       string  `json:"target_id"`
	Label          string  `json:"label"`
	PeerID         string  `json:"peer_id"`
	TransportBadge string  `json:"transport_badge"`
	CurrentFile    string  `json:"current_file,omitempty"`
	Phase          string  `json:"phase"`
	BytesWritten   int64   `json:"bytes_written"`
	BytesVerified  int64   `json:"bytes_verified"`
	TotalBytes     int64   `json:"total_bytes"`
	WriteBPS       float64 `json:"write_bps"`
	VerifyBPS      float64 `json:"verify_bps"`
	ETASeconds     float64 `json:"eta_seconds"`
	DoneFiles      int64   `json:"done_files"`
	FailedFiles    int64   `json:"failed_files"`
	TotalFiles     int64   `json:"total_files"`
}

// Progress is the transfer.progress event payload.
type Progress struct {
	TransferID      string           `json:"transfer_id"`
	Actor           string           `json:"actor"`
	OverallFraction float64          `json:"overall_fraction"`
	BytesWritten    int64            `json:"bytes_written"`
	BytesVerified   int64            `json:"bytes_verified"`
	TotalBytes      int64            `json:"total_bytes"`
	ETASeconds      float64          `json:"eta_seconds"`
	Targets         []TargetProgress `json:"targets"`
}

type targetTele struct {
	p       TargetProgress
	write   EMA
	verify  EMA
	lastW   int64
	lastV   int64
	current map[string]string // in-flight file -> phase
}

// Telemetry aggregates per-target counters and emits coalesced events.
type Telemetry struct {
	mu         sync.Mutex
	transferID string
	actor      string
	targets    []*targetTele
	index      map[string]*targetTele
	now        func() time.Time
}

// NewTelemetry builds telemetry for a transfer.
func NewTelemetry(transferID, actor string, now func() time.Time) *Telemetry {
	if now == nil {
		now = time.Now
	}
	return &Telemetry{transferID: transferID, actor: actor, index: map[string]*targetTele{}, now: now}
}

// AddTarget registers a destination with its totals and storage class.
func (t *Telemetry) AddTarget(p TargetProgress, class identity.StorageClass) {
	t.mu.Lock()
	defer t.mu.Unlock()
	w, v := SeedRates(class)
	tt := &targetTele{p: p, write: EMA{Seed: w}, verify: EMA{Seed: v}, current: map[string]string{}}
	tt.lastW, tt.lastV = p.BytesWritten, p.BytesVerified
	t.targets = append(t.targets, tt)
	t.index[p.TargetID] = tt
}

func (t *Telemetry) with(id string, fn func(*targetTele)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if tt := t.index[id]; tt != nil {
		fn(tt)
	}
}

// Wrote records n written bytes on a target.
func (t *Telemetry) Wrote(id string, n int64) {
	t.with(id, func(tt *targetTele) { tt.p.BytesWritten += n })
}

// Verified records n read-back bytes on a target.
func (t *Telemetry) Verified(id string, n int64) {
	t.with(id, func(tt *targetTele) { tt.p.BytesVerified += n })
}

// Phase marks file's phase on a target ("" clears it).
func (t *Telemetry) Phase(id, file, phase string) {
	t.with(id, func(tt *targetTele) {
		if phase == "" {
			delete(tt.current, file)
		} else {
			tt.current[file] = phase
		}
	})
}

// FileDone counts a settled file; failed counts a failure.
func (t *Telemetry) FileDone(id string, failed bool) {
	t.with(id, func(tt *targetTele) {
		if failed {
			tt.p.FailedFiles++
		} else {
			tt.p.DoneFiles++
		}
	})
}

// Rates returns a target's current (write, verify) EMA rates.
func (t *Telemetry) Rates(id string) (float64, float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if tt := t.index[id]; tt != nil {
		return tt.write.Rate(), tt.verify.Rate()
	}
	return 0, 0
}

// Snapshot feeds the EMAs and returns the current event.
func (t *Telemetry) Snapshot() Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	ev := Progress{TransferID: t.transferID, Actor: t.actor, ETASeconds: 0}
	var done, total int64
	for _, tt := range t.targets {
		tt.write.Add(now, tt.p.BytesWritten-tt.lastW)
		tt.verify.Add(now, tt.p.BytesVerified-tt.lastV)
		tt.lastW, tt.lastV = tt.p.BytesWritten, tt.p.BytesVerified
		p := tt.p
		p.WriteBPS, p.VerifyBPS = tt.write.Rate(), tt.verify.Rate()
		p.ETASeconds = ETA(p.TotalBytes-p.BytesWritten, p.TotalBytes-p.BytesVerified, p.WriteBPS, p.VerifyBPS)
		p.Phase = PhaseIdle
		if p.DoneFiles+p.FailedFiles >= p.TotalFiles && p.TotalFiles > 0 {
			p.Phase = PhaseDone
		}
		for f, ph := range tt.current {
			if p.CurrentFile == "" || f < p.CurrentFile {
				p.CurrentFile, p.Phase = f, ph
			}
		}
		ev.Targets = append(ev.Targets, p)
		ev.BytesWritten += p.BytesWritten
		ev.BytesVerified += p.BytesVerified
		ev.TotalBytes += p.TotalBytes
		// Overall progress counts writing and verifying as equal halves.
		done += p.BytesWritten + p.BytesVerified
		total += 2 * p.TotalBytes
		// The transfer finishes when its slowest target does.
		if p.ETASeconds < 0 || ev.ETASeconds < 0 {
			ev.ETASeconds = -1
		} else if p.ETASeconds > ev.ETASeconds {
			ev.ETASeconds = p.ETASeconds
		}
	}
	if total > 0 {
		ev.OverallFraction = math.Min(1, float64(done)/float64(total))
	} else {
		ev.OverallFraction = 1
	}
	return ev
}

// Emit calls fn with a snapshot every ProgressInterval until stop is
// closed, then once more with the final state.
func (t *Telemetry) Emit(stop <-chan struct{}, fn func(Progress)) {
	tick := time.NewTicker(ProgressInterval)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			fn(t.Snapshot())
			return
		case <-tick.C:
			fn(t.Snapshot())
		}
	}
}
