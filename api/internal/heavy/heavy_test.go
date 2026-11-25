package heavy

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGateLimitsConcurrency(t *testing.T) {
	g := New(1)
	var cur, peak, waited atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := g.Acquire(context.Background(), func() { waited.Add(1) })
			if err != nil {
				t.Error(err)
				return
			}
			n := cur.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			cur.Add(-1)
			rel()
		}()
	}
	wg.Wait()
	if peak.Load() != 1 || waited.Load() == 0 {
		t.Fatalf("peak=%d waited=%d", peak.Load(), waited.Load())
	}
}

func TestGateCancel(t *testing.T) {
	g := New(1)
	rel, _ := g.Acquire(context.Background(), nil)
	defer rel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := g.Acquire(ctx, nil); err == nil {
		t.Fatal("expected ctx error")
	}
	var nilGate *Gate
	if r, err := nilGate.Acquire(context.Background(), nil); err != nil || r == nil {
		t.Fatal("nil gate must not block")
	}
}
