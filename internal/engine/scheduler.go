package engine

import "github.com/Lunar-Park/lp-ferry/internal/identity"

// LargeFileBytes splits the large-stream lane from the small-file lane.
const LargeFileBytes = 32 << 20

// Lanes is how many large and small files may be in flight at once.
type Lanes struct {
	Large int
	Small int
}

// LanesFor picks concurrency for one storage class. One static worker
// count is wrong for every drive: a card reader or spinning disk wants one
// sequential large stream, NVMe and 10GbE want several to fill queue depth
// and TCP windows, and tiny sidecars want many workers to hide
// open/fsync/link latency.
func LanesFor(c identity.StorageClass) Lanes {
	switch c {
	case identity.ClassHDD, identity.ClassRemovable:
		return Lanes{Large: 1, Small: 4}
	case identity.ClassSSD:
		return Lanes{Large: 3, Small: 12}
	case identity.ClassNetwork:
		return Lanes{Large: 2, Small: 8}
	default:
		return Lanes{Large: 2, Small: 8}
	}
}

// LanesForAll is the most conservative lane count across every device the
// copy touches (the source and each destination).
func LanesForAll(classes ...identity.StorageClass) Lanes {
	out := Lanes{Large: 1 << 30, Small: 1 << 30}
	for _, c := range classes {
		l := LanesFor(c)
		out.Large = min(out.Large, l.Large)
		out.Small = min(out.Small, l.Small)
	}
	if len(classes) == 0 {
		return LanesFor(identity.ClassUnknown)
	}
	return out
}

// IsLarge reports whether size belongs in the large lane.
func IsLarge(size int64) bool { return size >= LargeFileBytes }
