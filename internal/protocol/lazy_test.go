package protocol

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
)

type connectFunc func(context.Context, client.ServerProfile, string, func(client.RemoteState)) (client.RemoteConnection, error)

func (f connectFunc) Connect(ctx context.Context, p client.ServerProfile, password string, update func(client.RemoteState)) (client.RemoteConnection, error) {
	return f(ctx, p, password, update)
}

func TestLazySetupCancellationAndRetry(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	target := connectFunc(func(context.Context, client.ServerProfile, string, func(client.RemoteState)) (client.RemoteConnection, error) {
		calls.Add(1)
		return nil, nil
	})
	lazy := NewLazyConnector(func() (client.RemoteConnector, error) { close(entered); <-release; return target, nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := lazy.Connect(ctx, client.ServerProfile{}, "", nil); done <- err }()
	<-entered
	waitCtx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if _, err := lazy.Connect(waitCtx, client.ServerProfile{}, "", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait cancellation: %v", err)
	}
	cancel()
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("cancelled setup connected: %v", err)
	}
	if _, err := lazy.Connect(context.Background(), client.ServerProfile{}, "", nil); err != nil || calls.Load() != 1 {
		t.Fatal("initialized connector not reused")
	}
	tries := 0
	retry := NewLazyConnector(func() (client.RemoteConnector, error) {
		tries++
		if tries == 1 {
			return nil, errors.New("temporary failure")
		}
		return target, nil
	})
	_, _ = retry.Connect(context.Background(), client.ServerProfile{}, "", nil)
	if _, err := retry.Connect(context.Background(), client.ServerProfile{}, "", nil); err != nil || tries != 2 {
		t.Fatal("failed initialization cached forever")
	}
}
