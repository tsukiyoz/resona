package desktopipc

import (
	"context"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
)

type shutdownStatus struct {
	Disconnected   bool   `json:"disconnected"`
	SoundCompleted bool   `json:"soundCompleted"`
	Error          string `json:"error,omitempty"`
}

func prepareShutdown(ctx context.Context, s *client.Service, enabled bool, volume int, play func(context.Context, string, int) error) shutdownStatus {
	w, _ := s.GetWorkspace()
	voice := s.GetVoiceState()
	shouldPlay := w.Session.Mode == "connected" && enabled && volume > 0 && !voice.Deafened
	done := make(chan struct{})
	go func() { s.Shutdown(); close(done) }()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-ctx.Done():
		return shutdownStatus{Error: "等待退出已取消，核心仍在清理"}
	case <-timer.C:
		return shutdownStatus{Error: "核心清理超时"}
	}
	status := shutdownStatus{Disconnected: true}
	if !shouldPlay {
		return status
	}
	soundCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	played := make(chan error, 1)
	go func() { played <- play(soundCtx, "disconnected", volume) }()
	select {
	case err := <-played:
		if err != nil {
			status.Error = "退出提示音播放失败"
		} else {
			status.SoundCompleted = true
		}
	case <-soundCtx.Done():
		status.Error = "退出提示音超时"
	}
	return status
}
