//go:build integration

package ts3

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/tsukiyoz/resona/internal/client"
)

func TestLiveDedicatedChannelIcons(t *testing.T) {
	address, target, name, identity := os.Getenv("RESONA_TS_ADDRESS"), os.Getenv("RESONA_TS_TEST_CHANNEL_ID"), os.Getenv("RESONA_TS_TEST_CHANNEL_NAME"), os.Getenv("RESONA_TS_IDENTITY")
	if address == "" || target == "" || name == "" || identity == "" {
		t.Skip("authorized identity and dedicated test channel environment are required")
	}
	if !strings.HasPrefix(name, "Resona Test ") || !validID(target) {
		t.Fatal("dedicated Resona test channel required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	endpoint, err := resolveEndpoint(ctx, address)
	if err != nil {
		t.Fatal("endpoint resolution failed")
	}
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		t.Fatal("invalid resolved endpoint")
	}
	var mu sync.Mutex
	var latest client.RemoteState
	changed := make(chan struct{}, 1)
	conn, err := New(identity).Connect(ctx, client.ServerProfile{Address: endpoint, Nickname: "Resona-IconVerify"}, os.Getenv("RESONA_TS_PASSWORD"), func(state client.RemoteState) {
		mu.Lock()
		latest = state
		mu.Unlock()
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatal("icon verification connection failed")
	}
	defer conn.Close()
	mu.Lock()
	found := false
	for _, ch := range latest.Channels {
		if ch.ID == target && ch.Name == name && !ch.PasswordRequired && ch.Kind != "separator" {
			found = true
		}
	}
	mu.Unlock()
	if !found {
		t.Fatal("dedicated channel absent; refusing to move")
	}
	if err := conn.MoveChannel(ctx, target); err != nil {
		t.Fatal("dedicated channel move failed")
	}
	for {
		mu.Lock()
		state := latest
		mu.Unlock()
		if state.Closed || state.ChannelID != target {
			t.Fatal("left dedicated channel during icon verification")
		}
		expected, loaded, spacers := 0, 0, 0
		unique := make(map[string]bool)
		for _, ch := range state.Channels {
			if ch.Kind == "separator" {
				spacers++
			}
			if !customIconID(ch.IconID) {
				continue
			}
			expected++
			unique[ch.IconID] = true
			if strings.HasPrefix(ch.IconDataURL, "data:image/png;base64,") {
				loaded++
			}
		}
		if expected == 0 {
			t.Skip("server has no custom channel icons")
		}
		if loaded == expected {
			t.Logf("all %d custom-icon channel rows loaded from %d server resources; spacers=%d", loaded, len(unique), spacers)
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			diagnostics := 0
			for _, ch := range state.Channels {
				if !customIconID(ch.IconID) || ch.IconDataURL != "" || diagnostics >= 3 {
					continue
				}
				diagnostics++
				diagnosticCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
				_, diagnosticErr := downloadChannelIcon(diagnosticCtx, host, ch.IconID, func(ctx context.Context, cid uint64, path, password string) (*teamspeak.FileDownloadInfo, error) {
					info, err := conn.(*connection).client.FileTransferInitDownloadContext(ctx, cid, path, password)
					if err != nil {
						var commandErr *teamspeak.CommandError
						if errors.As(err, &commandErr) {
							return nil, fmt.Errorf("icon initialization rejected: code=%d", commandErr.ID)
						}
						return nil, errors.New("icon initialization failed")
					}
					return info, nil
				})
				stop()
				t.Logf("unloaded icon id=%s diagnostic=%v", ch.IconID, diagnosticErr)
			}
			t.Fatalf("server icon load incomplete: %d/%d channel rows", loaded, expected)
		}
	}
}
