package daemon

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/ipc"
)

// Bus fans events out to subscribers. Publishing never blocks: a
// subscriber that cannot keep up loses events (counted), never stalls a
// transfer.
type Bus struct {
	mu      sync.Mutex
	next    int
	subs    map[int]*subscriber
	Dropped atomic.Int64
}

type subscriber struct {
	topics []string
	ch     chan ipc.Event
}

func matches(patterns []string, topic string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if p == topic || p == "*" || (strings.HasSuffix(p, ".*") && strings.HasPrefix(topic, strings.TrimSuffix(p, "*"))) {
			return true
		}
	}
	return false
}

// Subscribe returns a channel of matching events and a cancel func.
// Patterns are exact topics, "prefix.*" or "*"; none means everything.
func (b *Bus) Subscribe(topics []string) (<-chan ipc.Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = map[int]*subscriber{}
	}
	id := b.next
	b.next++
	s := &subscriber{topics: topics, ch: make(chan ipc.Event, 512)}
	b.subs[id] = s
	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			b.mu.Unlock()
			close(s.ch)
		})
	}
}

// Publish delivers an event to every matching subscriber.
func (b *Bus) Publish(topic string, data any) {
	ev := ipc.Event{Topic: topic, Time: time.Now().UTC(), Data: data}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		if !matches(s.topics, topic) {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			b.Dropped.Add(1)
		}
	}
}
