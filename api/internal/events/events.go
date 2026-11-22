// Package events publishes job progress over Redis pub/sub for SSE clients.
package events

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const channel = "clipforge:events"

type Event struct {
	Kind     string  `json:"kind"` // video|clip|publication
	ID       string  `json:"id"`
	VideoID  string  `json:"videoId,omitempty"`
	Status   string  `json:"status"`
	Stage    string  `json:"stage,omitempty"`
	Progress float32 `json:"progress"`
	Error    string  `json:"error,omitempty"`
}

type Bus struct {
	rdb *redis.Client
	mu  sync.Mutex
	// throttle progress spam: last publish per id
	last map[string]time.Time
}

func NewBus(rdb *redis.Client) *Bus { return &Bus{rdb: rdb, last: map[string]time.Time{}} }

// Publish sends an event. Progress-only updates are throttled to ~4/s per id.
func (b *Bus) Publish(ctx context.Context, e Event, force bool) {
	if force {
		b.mu.Lock()
		delete(b.last, e.ID)
		b.mu.Unlock()
	} else {
		b.mu.Lock()
		if t, ok := b.last[e.ID]; ok && time.Since(t) < 250*time.Millisecond {
			b.mu.Unlock()
			return
		}
		b.last[e.ID] = time.Now()
		b.mu.Unlock()
	}
	data, _ := json.Marshal(e)
	_ = b.rdb.Publish(ctx, channel, data).Err()
}

// Subscribe returns a channel of events until ctx is cancelled.
func (b *Bus) Subscribe(ctx context.Context) <-chan Event {
	out := make(chan Event, 64)
	sub := b.rdb.Subscribe(ctx, channel)
	go func() {
		defer close(out)
		defer sub.Close()
		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-ch:
				if !ok {
					return
				}
				var e Event
				if json.Unmarshal([]byte(m.Payload), &e) == nil {
					select {
					case out <- e:
					default: // slow client: drop
					}
				}
			}
		}
	}()
	return out
}
