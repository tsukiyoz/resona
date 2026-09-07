package teamspeak

import (
	"context"
	"sync"
	"time"
)

// commandThrottle is a token-bucket limiter for outbound commands.
type commandThrottle struct {
	lastUpdate time.Time
	tokens     float64
	mu         sync.Mutex
}

func newCommandThrottle() *commandThrottle {
	return &commandThrottle{
		tokens:     5,
		lastUpdate: time.Now(),
	}
}

func (t *commandThrottle) wait(ctx context.Context) error {
	const (
		tokenRate = 4.0
		tokenMax  = 8.0
	)

	for {
		t.mu.Lock()

		now := time.Now()
		elapsed := now.Sub(t.lastUpdate).Seconds()
		t.tokens += elapsed * tokenRate
		if t.tokens > tokenMax {
			t.tokens = tokenMax
		}
		t.lastUpdate = now

		if t.tokens >= 1.0 {
			t.tokens -= 1.0
			t.mu.Unlock()

			return nil
		}

		waitDur := time.Duration((1.0-t.tokens)/tokenRate*float64(time.Second)) + 10*time.Millisecond
		t.mu.Unlock()

		timer := time.NewTimer(waitDur)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()

			return ctx.Err()
		}
	}
}
