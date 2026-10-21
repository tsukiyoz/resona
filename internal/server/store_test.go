package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/noiseudp"
)

func TestResourceWatchProjectsInterestAndCancels(t *testing.T) {
	store := testStore(t, t.TempDir())
	s := &Server{config: Config{Store: store, Name: "test", Channels: store.snapshot().Channels.Channels}, peers: map[uint16]*peer{}}
	for id := uint16(1); id <= 2; id++ {
		s.peers[id] = &peer{member: w.Member{ID: id, Channel: 1, Nickname: "test"}, out: make(chan []byte, 8), watch: w.WatchResources{AllChannels: id == 2}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- store.watch(ctx, s.applyResources) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("watch did not stop")
		}
	}()
	read := func(p *peer) w.State {
		t.Helper()
		select {
		case data := <-p.out:
			frame, err := w.Read(bytes.NewReader(data))
			var state w.State
			if err != nil || w.Decode(frame, &state) != nil {
				t.Fatal("invalid state")
			}
			return state
		case <-time.After(2 * time.Second):
			t.Fatal("watch update missing")
			return w.State{}
		}
	}
	read(s.peers[1])
	read(s.peers[2])
	r := store.snapshot()
	r.Channels.Channels[1].Name = "Other channel renamed"
	data, _ := json.Marshal(r)
	if _, err := store.kv.Put(ctx, resourceKey, data); err != nil {
		t.Fatal(err)
	}
	if state := read(s.peers[2]); state.Channels[1].Name != "Other channel renamed" {
		t.Fatal("list-watch gap or stale projection")
	}
	select {
	case <-s.peers[1].out:
		t.Fatal("unchanged current-channel scope received an update")
	default:
	}
	r.Channels.Channels[0].Description = "Current channel changed"
	data, _ = json.Marshal(r)
	if _, err := store.kv.Put(ctx, resourceKey, data); err != nil {
		t.Fatal(err)
	}
	if state := read(s.peers[1]); len(state.Channels) != 1 || state.Channels[0].Description != "Current channel changed" {
		t.Fatal("current scope did not update")
	}
	read(s.peers[2])
}

func TestWatchProjectsRevisionAlreadyReadByRejectedMutation(t *testing.T) {
	store := testStore(t, t.TempDir())
	s := &Server{config: Config{Store: store, Channels: store.snapshot().Channels.Channels}, peers: map[uint16]*peer{}}
	r := store.snapshot()
	r.Channels.Channels[1].Name = "Updated externally"
	data, _ := json.Marshal(r)
	revision, err := store.kv.Put(context.Background(), resourceKey, data)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.update(func(*resourceRecord) error { return errors.New("rejected command") }); err == nil {
		t.Fatal("command accepted")
	}
	if err = s.applyResources(r, revision); err != nil {
		t.Fatal(err)
	}
	if s.config.Channels[1].Name != "Updated externally" {
		t.Fatal("watch skipped an unprojected revision")
	}
}

func TestChannelDefaultMigrationIsIndependentOfDisplayPosition(t *testing.T) {
	s := testStore(t, t.TempDir())
	r := s.snapshot()
	r.Channels.Version, r.Channels.Default = 1, 0
	data, _ := json.Marshal(r)
	legacy, err := decodeResources(data)
	if err != nil || legacy.Channels.Version != 2 || legacy.Channels.Default != 1 {
		t.Fatalf("legacy migration: %+v %v", legacy.Channels, err)
	}
	for _, invalid := range []uint16{0, 99} {
		r.Channels.Version, r.Channels.Default = 2, invalid
		data, _ = json.Marshal(r)
		if _, err := decodeResources(data); err == nil {
			t.Fatalf("accepted invalid v2 default %d", invalid)
		}
	}
	r = legacy
	r.Channels.Channels = append([]w.Channel(nil), legacy.Channels.Channels...)
	r.Channels.Channels[0], r.Channels.Channels[1] = r.Channels.Channels[1], r.Channels.Channels[0]
	if err := validResourceTransition(legacy, r); err != nil {
		t.Fatal(err)
	}
	r.Channels.Default = 2
	if err := validResourceTransition(legacy, r); err == nil {
		t.Fatal("accepted default identity replacement")
	}
}

func TestLegacyKVOrderUpgradePersistsOnFirstMutation(t *testing.T) {
	s, owner, dir := channelTestServer(t)
	r := s.config.Store.snapshot()
	r.Channels.Version, r.Channels.Default = 1, 0
	data, _ := json.Marshal(r)
	if _, err := s.config.Store.kv.Put(context.Background(), resourceKey, data); err != nil {
		t.Fatal(err)
	}
	s.config.Store.Close()
	s.config.Store = testStore(t, dir)
	if got := s.config.Store.snapshot().Channels; got.Version != 2 || got.Default != 1 {
		t.Fatalf("legacy KV migration: %+v", got)
	}
	if code := s.ReorderChannel(context.Background(), owner.identity, 1, 0); code != w.OK {
		t.Fatal(code)
	}
	s.config.Store.Close()
	s.config.Store = testStore(t, dir)
	got := s.config.Store.snapshot().Channels
	if got.Version != 2 || got.Default != 1 || got.Channels[0].ID != 2 || got.Channels[1].ID != 1 {
		t.Fatalf("upgraded restart lost order/default: %+v", got)
	}
	entry, err := s.config.Store.kv.Get(context.Background(), resourceKey)
	var persisted resourceRecord
	if err != nil || json.Unmarshal(entry.Value(), &persisted) != nil || persisted.Channels.Version != 2 || persisted.Channels.Default != 1 {
		t.Fatal("first mutation did not persist v2")
	}
}

func TestResourceCASRetriesPreserveConcurrentChangesAndAreBounded(t *testing.T) {
	for _, alwaysConflict := range []bool{false, true} {
		s := testStore(t, t.TempDir())
		attempts := 0
		err := s.update(func(r *resourceRecord) error {
			attempts++
			if attempts == 1 || alwaysConflict {
				concurrent := *r
				concurrent.Channels.Channels = append([]w.Channel(nil), r.Channels.Channels...)
				concurrent.Channels.Channels[1].Description = "concurrent change"
				data, _ := json.Marshal(concurrent)
				if _, err := s.kv.Put(context.Background(), resourceKey, data); err != nil {
					return err
				}
			}
			r.Channels.Channels[0].Name = "local change"
			return nil
		})
		if alwaysConflict {
			if err == nil || attempts != 3 {
				t.Fatalf("unbounded/unreported conflict: %d %v", attempts, err)
			}
		} else {
			r := s.snapshot()
			if err != nil || attempts != 2 || r.Channels.Channels[0].Name != "local change" || r.Channels.Channels[1].Description != "concurrent change" {
				t.Fatalf("lost concurrent update: %d %v", attempts, err)
			}
		}
	}
}

func TestResourcesCorruptionAndDeletionNeverReimportLegacy(t *testing.T) {
	for _, fault := range []string{"invalid", "deleted"} {
		t.Run(fault, func(t *testing.T) {
			dir := t.TempDir()
			s := testStore(t, dir)
			var err error
			if fault == "deleted" {
				err = s.kv.Delete(context.Background(), resourceKey)
			} else {
				_, err = s.kv.Put(context.Background(), resourceKey, []byte(`{"version":999}`))
			}
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			if reopened, err := OpenStore(ChatConfig{Directory: filepath.Join(dir, "chat")}, dir, []w.Channel{{ID: 1, Name: "Seed"}}); err == nil {
				reopened.Close()
				t.Fatal("invalid existing KV was reset")
			}
		})
	}
}

func TestLegacyImportFailsClosedAndCanBeRetriedAfterRepair(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "channels.json")
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	open := func() (*Store, error) {
		return OpenStore(ChatConfig{Directory: filepath.Join(dir, "chat")}, dir, []w.Channel{{ID: 1, Name: "Seed"}})
	}
	if s, err := open(); err == nil {
		s.Close()
		t.Fatal("corrupt legacy data ignored")
	}
	r := channelRecord{Version: 1, Next: 8, Channels: []w.Channel{{ID: 4, Name: "Imported", Bitrate: 20000}}}
	data, _ := json.Marshal(r)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := open()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.snapshot().Channels; got.Next != 8 || got.Channels[0].ID != 4 || got.Channels[0].Bitrate != 20000 {
		t.Fatal("legacy IDs/audio lost")
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, data) {
		t.Fatal("legacy original modified")
	}
}

func TestResourceReadDoesNotWaitForWriter(t *testing.T) {
	s := testStore(t, t.TempDir())
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.update(func(*resourceRecord) error { close(entered); <-release; return errors.New("cancel write") })
	}()
	<-entered
	defer func() { close(release); <-done }()
	read := make(chan struct{})
	go func() { s.Status("member"); close(read) }()
	select {
	case <-read:
	case <-time.After(time.Second):
		t.Fatal("status blocked on persistence writer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	queued := make(chan error, 1)
	go func() {
		queued <- s.updateContext(ctx, func(*resourceRecord) error { t.Error("canceled queued writer ran"); return nil })
	}()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued writer did not cancel")
	}
}

func TestServeJoinsResourceWatchOnCancellationAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		store := testStore(t, t.TempDir())
		key, err := noiseudp.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		s, err := Listen("127.0.0.1:0", Config{Store: store, Name: "watch lifecycle", NoiseKey: key})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if !fail {
			cancel()
		}
		done := make(chan error, 1)
		go func() { done <- s.Serve(ctx) }()
		if fail {
			if _, err := store.kv.Put(context.Background(), resourceKey, []byte("invalid")); err != nil {
				cancel()
				t.Fatal(err)
			}
		}
		select {
		case err := <-done:
			cancel()
			if (err != nil) != fail {
				t.Fatalf("fail=%v: %v", fail, err)
			}
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatal("Serve did not join and stop")
		}
		if !store.chat.conn.IsClosed() {
			t.Fatal("broker connection retained after Serve")
		}
	}
}

func TestInvalidWatchStopsAndImmutableOwnerCannotChange(t *testing.T) {
	s := testStore(t, t.TempDir())
	token, err := s.ProvisionOwnerClaim(false)
	if err != nil {
		t.Fatal(err)
	}
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err = s.Claim(id, token); err != nil {
		t.Fatal(err)
	}
	if err = s.update(func(r *resourceRecord) error { r.Owner = ""; return nil }); err == nil {
		t.Fatal("owner could be removed")
	}
	if role, _ := s.Status(id); role != "owner" {
		t.Fatal("failed mutation changed owner")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.watch(ctx, func(resourceRecord, uint64) error { return nil }) }()
	if _, err = s.kv.Put(ctx, resourceKey, []byte("invalid")); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("watch silently accepted corrupt state")
		}
	case <-ctx.Done():
		t.Fatal("watch failed to report corruption")
	}
}
