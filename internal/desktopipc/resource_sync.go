package desktopipc

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
)

const resourceResyncInterval = 5 * time.Minute

type resourceInterest struct {
	client.ResourceInterest
	Generation uint64 `json:"generation"`
	Retry      bool   `json:"retry"`
}

type resourceSyncStatus struct {
	Generation uint64 `json:"generation"`
	SessionID  string `json:"sessionID"`
	State      string `json:"state"`
	Attempt    int    `json:"attempt"`
	Error      string `json:"error"`
}

// The mailbox stores one latest interest, never an unbounded event backlog.
type resourceSync struct {
	mu             sync.Mutex
	interest       resourceInterest
	changed        chan struct{}
	attemptTimeout time.Duration
	retryDelay     time.Duration
}

func newResourceSync() *resourceSync {
	return &resourceSync{interest: resourceInterest{ResourceInterest: client.ResourceInterest{Active: true, AllChannels: true, AllMembers: true}}, changed: make(chan struct{}, 1), attemptTimeout: 2 * time.Second, retryDelay: 300 * time.Millisecond}
}

func (r *resourceSync) current() resourceInterest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.interest
}

func (r *resourceSync) set(next resourceInterest) {
	r.mu.Lock()
	r.interest = next
	r.mu.Unlock()
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

func (r *resourceSync) run(ctx context.Context, service *client.Service, completed func(resourceSyncStatus)) {
	changes, unsubscribe := service.SubscribeChanges()
	defer unsubscribe()
	timer := time.NewTimer(resourceResyncInterval)
	timer.Stop()
	defer timer.Stop()
	var timerC <-chan time.Time
	var desired resourceInterest
	var session string
	var ready, failed, needed, running bool
	var status resourceSyncStatus
	var cancel context.CancelFunc
	type outcome struct {
		interest resourceInterest
		session  string
		err      error
		attempt  int
		final    bool
	}
	results := make(chan outcome, 1)
	defer func() {
		if running {
			cancel()
			for result := range results {
				if result.final {
					break
				}
			}
		}
	}()
	requestLatest := func(force bool) {
		next, id := r.current(), service.ResourceSessionID()
		if !force && next == desired && id == session {
			return
		}
		if running {
			cancel()
		}
		if id != session {
			ready, failed = false, false
		}
		desired, session, needed = next, id, true
		timer.Stop()
		timerC = nil
	}
	for {
		if needed && !running {
			needed = false
			status.Generation, status.SessionID = desired.Generation, session
			status.Attempt = 0
			switch {
			case session == "":
				status.State, status.Error = "idle", ""
				completed(status)
			case failed && !desired.Retry:
				status.State = "failed"
				completed(status)
			default:
				status.State, status.Error = "refreshing", ""
				if !ready || failed {
					status.State = "syncing"
				}
				failed = false
				completed(status)
				jobCtx, stop := context.WithCancel(ctx)
				cancel, running = stop, true
				interest, id := desired, session
				go func() {
					lastAttempt := 0
					err := r.synchronize(jobCtx, service, interest, func(attempt int) {
						lastAttempt = attempt
						select {
						case results <- outcome{interest: interest, session: id, attempt: attempt}:
						case <-jobCtx.Done():
						}
					})
					results <- outcome{interest: interest, session: id, err: err, attempt: lastAttempt, final: true}
				}()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-r.changed:
			requestLatest(false)
		case <-changes:
			requestLatest(false)
		case <-timerC:
			requestLatest(true)
		case result := <-results:
			if !result.final {
				if !needed && result.interest == desired && result.session == session && desired == r.current() && session == service.ResourceSessionID() {
					status.Attempt = result.attempt
					completed(status)
				}
				continue
			}
			cancel()
			running = false
			if needed || result.interest != r.current() || result.session != service.ResourceSessionID() {
				// A coalesced change may still be waiting in the mailbox.
				requestLatest(false)
				continue
			}
			status.Attempt = result.attempt
			if result.err == nil {
				ready, failed = true, false
				status.State, status.Error = "ready", ""
				if desired.Active {
					timer.Reset(resourceResyncInterval)
					timerC = timer.C
				}
			} else {
				failed = true
				status.State, status.Error = "failed", "资源同步失败，请重试；语音连接可能仍然正常"
				var failure *client.ResourceSyncError
				if errors.As(result.err, &failure) {
					status.Error = failure.Message
				}
				slog.Warn("resource synchronization failed", "attempt", status.Attempt)
			}
			completed(status)
		}
	}
}

// There is at most one in-flight request; only an active job owns retry timers.
func (r *resourceSync) synchronize(ctx context.Context, service *client.Service, interest resourceInterest, progress func(int)) error {
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		progress(attempt)
		attemptCtx, stop := context.WithTimeout(ctx, r.attemptTimeout)
		err = service.SyncResources(attemptCtx, interest.ResourceInterest)
		stop()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var failure *client.ResourceSyncError
		if err == nil || attempt == 3 || (errors.As(err, &failure) && !failure.Retryable) {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt) * r.retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func backgroundVoice(state client.VoiceState) client.VoiceState {
	state.SpeakingClientIDs = nil
	state.LocalSpeaking = false
	state.InputLevelDB = 0
	return state
}

func publishWorkspace(active bool, previous *client.Workspace, next client.Workspace) bool {
	// Session transitions and permission changes remain urgent in the background.
	if active || previous == nil {
		return true
	}
	old, current := previous.Session, next.Session
	old.MemberSyncState, current.MemberSyncState = "", ""
	old.MemberSyncError, current.MemberSyncError = "", ""
	old.ServerName, current.ServerName = "", ""
	return old != current
}
