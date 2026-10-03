// Package logs holds captured process output. It knows nothing about
// processes, so it can later be persisted or streamed without changes.
package logs

import (
	"sync"
	"time"
)

type Stream string

const (
	Stdout Stream = "stdout"
	Stderr Stream = "stderr"
)

// DefaultSize is the number of lines kept when no size is given.
const DefaultSize = 5000

// Entry is one captured line. Seq increases monotonically per buffer, so a
// consumer can poll Since(lastSeq) and later stream the same way.
type Entry struct {
	Seq    uint64
	Time   time.Time
	Stream Stream
	Line   string
}

// Buffer is a bounded, concurrency-safe ring of lines. The oldest lines are
// dropped when it is full.
type Buffer struct {
	mu      sync.Mutex
	ring    []Entry
	head    int
	size    int
	nextSeq uint64
	dropped uint64
}

func NewBuffer(max int) *Buffer {
	if max <= 0 {
		max = DefaultSize
	}
	return &Buffer{ring: make([]Entry, max), nextSeq: 1}
}

func (b *Buffer) Append(stream Stream, line string) Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := Entry{Seq: b.nextSeq, Time: time.Now(), Stream: stream, Line: line}
	b.nextSeq++
	if b.size < len(b.ring) {
		b.ring[(b.head+b.size)%len(b.ring)] = e
		b.size++
	} else {
		b.ring[b.head] = e
		b.head = (b.head + 1) % len(b.ring)
		b.dropped++
	}
	return e
}

// Snapshot returns all retained entries, oldest first.
func (b *Buffer) Snapshot() []Entry { return b.Since(0) }

// Since returns retained entries with Seq greater than seq, oldest first.
func (b *Buffer) Since(seq uint64) []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Entry
	for i := 0; i < b.size; i++ {
		if e := b.ring[(b.head+i)%len(b.ring)]; e.Seq > seq {
			out = append(out, e)
		}
	}
	return out
}

// Dropped is the number of lines evicted so far.
func (b *Buffer) Dropped() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size
}
