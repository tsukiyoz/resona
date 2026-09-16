package client

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	mathrand "math/rand/v2"
	"sync"
	"time"

	"github.com/tsukiyoz/resona/internal/audio"
)

type reconnectPlan struct {
	profile  ServerProfile
	password string
	channel  string
	voice    audio.VoiceConfig
}

// One worker owns cleanup, backoff and setup. No ticker runs while connected.
// The caller already registered the detached connection/voice with cleanupWG.
func (s *Service) startReconnectLocked(plan reconnectPlan, old RemoteConnection, closeVoice bool) {
	previous := s.connectDone
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.connectCancel, s.connectDone = cancel, done
	s.generation++
	generation := s.generation
	s.state.Session.Mode = "reconnecting"
	s.state.Session.Error = "连接中断，正在重连；恢复后麦克风保持静音"
	s.notifyChangedLocked()
	go func() {
		defer cancel()
		defer func() {
			s.mu.Lock()
			if s.connectDone == done {
				s.connectCancel, s.connectDone = nil, nil
			}
			s.mu.Unlock()
			close(done)
		}()
		if previous != nil {
			<-previous
		}
		if closeVoice {
			s.closeRetiredVoices()
		}
		if old != nil {
			_ = old.Close()
		}
		if old != nil || closeVoice {
			s.cleanupWG.Done()
		}
		for attempt := 0; ; attempt++ {
			delay := reconnectBackoff(attempt)
			if s.reconnectDelay > 0 {
				delay = s.reconnectDelay
			}
			slog.Info("reconnect scheduled", "attempt", attempt+1, "delay_ms", delay.Milliseconds())
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			s.mu.Lock()
			if s.generation != generation || s.shutdown || ctx.Err() != nil {
				s.mu.Unlock()
				return
			}
			s.generation++
			generation = s.generation
			attemptGeneration := generation
			s.state.Session.Error = "正在尝试恢复连接；可随时取消"
			s.notifyChangedLocked()
			s.mu.Unlock()
			started := time.Now()
			attemptCtx, stop := context.WithTimeout(ctx, s.connectTimeout)
			// Bootstrap and channel restoration stay private until the connection is
			// accepted. A close racing setup must not publish a half-restored session.
			var setupMu sync.Mutex
			var latest RemoteState
			ready := false
			conn, err := s.connector.Connect(attemptCtx, plan.profile, plan.password, func(state RemoteState) {
				setupMu.Lock()
				defer setupMu.Unlock()
				if !ready {
					latest = state
					return
				}
				s.applyRemoteState(attemptGeneration, state)
			})
			if err == nil && conn == nil {
				err = errors.New("empty connection")
			}
			if err == nil && attemptCtx.Err() != nil {
				err = attemptCtx.Err()
			}
			if err != nil {
				stop()
				if conn != nil {
					_ = conn.Close()
				}
				var failure *ConnectFailure
				retry := !errors.As(err, &failure) || failure.Retryable
				slog.Warn("reconnect failed", "attempt", attempt+1, "retryable", retry, "elapsed_ms", time.Since(started).Milliseconds())
				s.mu.Lock()
				current := s.generation == generation && ctx.Err() == nil && s.state.Session.Mode == "reconnecting"
				if current && !retry {
					s.state.Session.Mode = "failed"
					s.state.Session.Error = failure.Message
					s.reconnect = nil
					s.notifyChangedLocked()
				}
				s.mu.Unlock()
				if !current || !retry {
					return
				}
				continue
			}
			setupMu.Lock()
			currentChannel := latest.ChannelID
			setupMu.Unlock()
			restored := currentChannel == plan.channel
			if !restored && plan.channel != "" {
				restored = conn.MoveChannel(attemptCtx, plan.channel) == nil
			}
			setupMu.Lock()
			if latest.Closed || attemptCtx.Err() != nil {
				retry := !latest.Closed || latest.Retryable
				setupMu.Unlock()
				stop()
				_ = conn.Close()
				s.mu.Lock()
				current := s.generation == generation && ctx.Err() == nil
				if current && !retry {
					s.state.Session.Mode = "failed"
					s.state.Session.Error = "服务器拒绝恢复连接，请检查配置后重试"
					s.reconnect = nil
					s.notifyChangedLocked()
				}
				s.mu.Unlock()
				if !current || !retry {
					return
				}
				continue
			}
			s.applyRemoteState(generation, latest)
			s.mu.Lock()
			if ctx.Err() != nil || attemptCtx.Err() != nil || generation != s.generation || s.state.Session.Mode != "reconnecting" {
				if ctx.Err() == nil && generation == s.generation && s.state.Session.Mode == "reconnecting" {
					s.state.Session.Mode = "failed"
					s.state.Session.Error = "恢复频道超时，请重新连接"
					s.reconnect = nil
					s.notifyChangedLocked()
				}
				s.mu.Unlock()
				setupMu.Unlock()
				stop()
				_ = conn.Close()
				return
			}
			s.connection = conn
			s.reconnect = &plan
			s.state.Session.ID = rand.Text()
			s.state.Messages = []Message{}
			s.state.Session.Mode = "connected"
			s.state.Session.Error = "连接已恢复，麦克风保持静音，请确认后开启"
			if !restored {
				s.state.Session.Error = "连接已恢复，但原频道不可用；已留在当前频道并保持麦克风静音"
			}
			s.addNotificationLocked("connected", s.state.Session.ChannelID)
			s.notifyChangedLocked()
			s.mu.Unlock()
			ready = true
			setupMu.Unlock()
			stop()
			_, _ = s.configureVoice(plan.voice, &generation, 3)
			slog.Info("reconnect succeeded", "attempt", attempt+1, "channel_restored", restored, "elapsed_ms", time.Since(started).Milliseconds())
			return
		}
	}()
}

func reconnectBackoff(attempt int) time.Duration {
	base := time.Second << min(attempt, 5)
	if base > 25*time.Second {
		base = 25 * time.Second
	}
	return base + time.Duration(mathrand.Int64N(int64(base/5)+1))
}
