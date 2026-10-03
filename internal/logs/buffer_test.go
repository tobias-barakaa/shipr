package logs

import (
	"fmt"
	"sync"
	"testing"
)

func TestAppendKeepsOrderAndSequence(t *testing.T) {
	b := NewBuffer(10)
	b.Append(Stdout, "a")
	b.Append(Stderr, "b")
	got := b.Snapshot()
	if len(got) != 2 || got[0].Line != "a" || got[1].Line != "b" {
		t.Fatalf("snapshot = %+v", got)
	}
	if got[0].Seq != 1 || got[1].Seq != 2 || got[1].Stream != Stderr {
		t.Errorf("entries = %+v", got)
	}
	if got[0].Time.IsZero() {
		t.Error("entry has no time")
	}
}

func TestRingDropsOldestLines(t *testing.T) {
	b := NewBuffer(3)
	for i := 1; i <= 5; i++ {
		b.Append(Stdout, fmt.Sprint(i))
	}
	got := b.Snapshot()
	if len(got) != 3 || got[0].Line != "3" || got[2].Line != "5" {
		t.Errorf("snapshot = %+v", got)
	}
	if b.Dropped() != 2 || b.Len() != 3 {
		t.Errorf("dropped=%d len=%d", b.Dropped(), b.Len())
	}
}

func TestSinceReturnsOnlyNewEntries(t *testing.T) {
	b := NewBuffer(10)
	b.Append(Stdout, "a")
	last := b.Append(Stdout, "b")
	b.Append(Stdout, "c")
	got := b.Since(last.Seq)
	if len(got) != 1 || got[0].Line != "c" {
		t.Errorf("Since = %+v", got)
	}
	if len(b.Since(99)) != 0 {
		t.Error("Since beyond the end should be empty")
	}
}

func TestDefaultSize(t *testing.T) {
	b := NewBuffer(0)
	for i := 0; i < DefaultSize+10; i++ {
		b.Append(Stdout, "x")
	}
	if b.Len() != DefaultSize {
		t.Errorf("Len = %d, want %d", b.Len(), DefaultSize)
	}
}

func TestConcurrentAppend(t *testing.T) {
	b := NewBuffer(10000)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				b.Append(Stdout, "x")
			}
		}()
	}
	wg.Wait()
	got := b.Snapshot()
	if len(got) != 4000 {
		t.Fatalf("len = %d", len(got))
	}
	for i, e := range got {
		if e.Seq != uint64(i+1) {
			t.Fatalf("sequence broken at %d: %d", i, e.Seq)
		}
	}
}
