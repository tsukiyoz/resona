package desktopipc

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tsukiyoz/resona/internal/audio"
)

type soundRequest struct {
	ctx    context.Context
	kind   string
	volume int
}
type soundPlayer struct {
	ctx    context.Context
	mu     sync.Mutex
	cancel context.CancelFunc
	queue  chan soundRequest
	done   chan struct{}
}

func newSoundPlayer(ctx context.Context) *soundPlayer {
	p := &soundPlayer{ctx: ctx, queue: make(chan soundRequest, 1), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		for {
			select {
			case <-ctx.Done():
				return
			case request := <-p.queue:
				if request.ctx.Err() == nil {
					_ = audio.PlayNotification(request.ctx, request.kind, request.volume)
				}
			}
		}
	}()
	return p
}

func (p *soundPlayer) play(kind string, volume int) error {
	if volume < 0 || volume > 100 {
		return errors.New("提示音音量无效")
	}
	switch kind {
	case "connected", "disconnected", "member_joined", "member_left":
	default:
		return errors.New("提示音类型无效")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
	}
	if volume == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(p.ctx, 3*time.Second)
	p.cancel = cancel
	select {
	case <-p.queue:
	default:
	}
	p.queue <- soundRequest{ctx: ctx, kind: kind, volume: volume}
	return nil
}

func (p *soundPlayer) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	select {
	case <-p.queue:
	default:
	}
}
