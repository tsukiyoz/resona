package native

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukiyoz/resona/internal/client"
	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/server"
)

func TestChannelManagementSyncPermissionsAndRestart(t *testing.T) {
	t.Run("noise", func(t *testing.T) {
		dir := t.TempDir()
		token, err := server.InitOwnerClaim(dir)
		if err != nil {
			t.Fatal(err)
		}
		owner, err := server.OpenOwnership(dir)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "channels.json")
		store, err := server.OpenChannelStore(path, []w.Channel{{ID: 1, Name: "Lobby"}, {ID: 2, Name: "Gaming"}})
		if err != nil {
			t.Fatal(err)
		}
		cfg := server.Config{Name: "test", Password: "test-password", Ownership: owner, ChannelStore: store}
		p, stop, done := startServerConfigured(t, cfg)
		connector := Connector{IdentityPath: filepath.Join(t.TempDir(), "identity.key")}
		connect := func(p client.ServerProfile) (*connection, *observer) {
			o := &observer{}
			c, err := connector.Connect(context.Background(), p, "test-password", o.update)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			if err := c.(*connection).SetResourceInterest(context.Background(), true, true); err != nil {
				t.Fatal(err)
			}
			return c.(*connection), o
		}
		a, oa := connect(p)
		b, ob := connectTest(t, p)
		ctx := context.Background()
		if err = a.ClaimOwner(ctx, token); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool { return oa.snapshot().CanManageChannels })
		if ob.snapshot().CanManageChannels {
			t.Fatal("member capability enabled")
		}
		if b.CreateChannel(ctx, "forbidden", "") == nil {
			t.Fatal("member create accepted")
		}
		if !oa.snapshot().CanConfigureChannelAudio {
			t.Fatal("missing audio capability")
		}
		if err = a.ManageChannelAudio(ctx, "create", "", "Squad", "description", 20000); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool { return len(ob.snapshot().Channels) == 3 })
		if ob.snapshot().Channels[2].Bitrate != 20000 || a.VoiceBitrate() != 48000 {
			t.Fatal("wrong channel bitrate applied")
		}
		if oa.snapshot().ChannelID != "1" || ob.snapshot().ChannelID != "1" {
			t.Fatal("create moved user")
		}
		if err = a.UpdateChannel(ctx, "3", "Renamed", "updated\ntext"); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool { return ob.snapshot().Channels[2].Description == "updated\ntext" })
		if ob.snapshot().Channels[2].Bitrate != 20000 {
			t.Fatal("legacy edit reset quality")
		}
		if b.UpdateChannel(ctx, "3", "forbidden", "") == nil || b.DeleteChannel(ctx, "3") == nil {
			t.Fatal("member mutation accepted")
		}
		if err = b.MoveChannel(ctx, "3"); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool { return b.VoiceBitrate() == 20000 })
		time.Sleep(600 * time.Millisecond)
		if err = a.ManageChannelAudio(ctx, "update", "3", "Renamed", "updated\ntext", 64000); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool { return b.VoiceBitrate() == 64000 && ob.snapshot().Channels[2].Bitrate == 64000 })
		if a.VoiceBitrate() != 48000 || ob.snapshot().ChannelID != "3" {
			t.Fatal("audio update changed channel routing")
		}
		// Let the administrative token bucket refill; moves use a separate peer.
		time.Sleep(600 * time.Millisecond)
		if a.DeleteChannel(ctx, "3") == nil {
			t.Fatal("occupied channel deleted")
		}
		if err = b.MoveChannel(ctx, "1"); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool { return b.VoiceBitrate() == 48000 })
		time.Sleep(600 * time.Millisecond)
		if err = a.DeleteChannel(ctx, "3"); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool { return len(ob.snapshot().Channels) == 2 })
		if a.DeleteChannel(ctx, "1") == nil {
			t.Fatal("default channel deleted")
		}
		_ = a.Close()
		_ = b.Close()
		stop()
		if err = <-done; err != nil {
			t.Fatal(err)
		}
		store, err = server.OpenChannelStore(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		owner, err = server.OpenOwnership(dir)
		if err != nil {
			t.Fatal(err)
		}
		cfg.ChannelStore, cfg.Ownership = store, owner
		p, _, _ = startServerConfigured(t, cfg)
		a, oa = connect(p)
		if !oa.snapshot().CanManageChannels || len(oa.snapshot().Channels) != 2 {
			t.Fatal("restart lost state")
		}
		if err = a.CreateChannel(ctx, "After restart", ""); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool { return len(oa.snapshot().Channels) == 3 })
		if oa.snapshot().Channels[2].ID != "4" {
			t.Fatal("deleted ID reused after restart")
		}
	})
}
