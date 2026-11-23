package engine

import (
	"sync"
	"sync/atomic"
)

// ChunkSize is the unit of every read, hash and write. Go hands out large
// allocations page-aligned, which is what direct and uncached I/O want.
const ChunkSize = 2 << 20

var bufPool = sync.Pool{New: func() any { b := make([]byte, ChunkSize); return &b }}

// chunk is one pooled buffer shared read-only by every sink it was sent to.
// The last sink to release it returns it to the pool.
type chunk struct {
	buf  *[]byte
	n    int
	refs atomic.Int32
}

func getChunk() *chunk { return &chunk{buf: bufPool.Get().(*[]byte)} }

func (c *chunk) bytes() []byte { return (*c.buf)[:c.n] }

func (c *chunk) release() {
	if c.refs.Add(-1) == 0 {
		bufPool.Put(c.buf)
		c.buf = nil
	}
}
