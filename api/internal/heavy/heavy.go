// Package heavy limits how many CPU/RAM-heavy jobs (whisper.cpp, ffmpeg
// encodes) run at once inside the worker, so they don't starve or OOM the host.
package heavy

import "context"

// Gate is a counting semaphore. The zero value (nil) never blocks.
type Gate struct{ ch chan struct{} }

// New returns a gate allowing n concurrent holders (n <= 0 means unlimited).
func New(n int) *Gate {
	if n <= 0 {
		return nil
	}
	return &Gate{ch: make(chan struct{}, n)}
}

// Acquire blocks until a slot is free or ctx ends. onWait is called once if
// the caller has to wait (e.g. to show "waiting for another job").
func (g *Gate) Acquire(ctx context.Context, onWait func()) (release func(), err error) {
	if g == nil {
		return func() {}, nil
	}
	select {
	case g.ch <- struct{}{}:
		return g.release, nil
	default:
	}
	if onWait != nil {
		onWait()
	}
	select {
	case g.ch <- struct{}{}:
		return g.release, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (g *Gate) release() { <-g.ch }
