package desktopipc

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
)

const resourceResyncInterval = 5 * time.Minute

type resourceInterest struct {
	client.ResourceInterest
	Generation uint64 `json:"generation"`
}

// The mailbox stores one latest interest, never an unbounded event backlog.
type resourceSync struct {
	mu       sync.Mutex
	interest resourceInterest
	changed  chan struct{}
}

func newResourceSync() *resourceSync {
	return &resourceSync{interest: resourceInterest{ResourceInterest: client.ResourceInterest{Active: true, AllChannels: true, AllMembers: true}}, changed: make(chan struct{}, 1)}
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

func (r *resourceSync) run(ctx context.Context, service *client.Service, completed func(resourceInterest, string, error)) {
	changes, unsubscribe := service.SubscribeChanges()
	defer unsubscribe()
	timer := time.NewTimer(resourceResyncInterval)
	defer timer.Stop()
	var timerC <-chan time.Time
	var applied resourceInterest
	var session string
	for {
		force := false
		select {
		case <-ctx.Done():
			return
		case <-r.changed:
			force = true
		case <-changes:
		case <-timerC:
			force = true
		}
		next, id := r.current(), service.ResourceSessionID()
		if !force && next == applied && id == session {
			continue
		}
		// No periodic wakeups when inactive. Foreground retries are bounded by
		// the same long interval, never by voice or membership event frequency.
		timer.Stop()
		timerC = nil
		if next.Active {
			timer.Reset(resourceResyncInterval)
			timerC = timer.C
		}
		applied, session = next, id
		err := service.SyncResources(ctx, next.ResourceInterest)
		if ctx.Err() != nil {
			return
		}
		if next != r.current() || id != service.ResourceSessionID() {
			continue
		}
		if err != nil {
			slog.Warn("resource synchronization failed", "error", err)
		}
		completed(next, id, err)
	}
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
