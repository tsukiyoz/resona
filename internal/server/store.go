package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/tsukiyoz/resona/internal/nativeidentity"
	w "github.com/tsukiyoz/resona/internal/nativewire"
)

const resourceKey = "state"

// resourceRecord is one CAS transaction: claiming an owner consumes its claim,
// and channel creation advances the non-reusable ID allocator in the same write.
type resourceRecord struct {
	Version     int               `json:"version"`
	Channels    channelRecord     `json:"channels"`
	Owner       string            `json:"owner,omitempty"`
	Claim       claimSecret       `json:"claim"`
	Admins      []string          `json:"admins,omitempty"`
	Credentials []AdminCredential `json:"credentials,omitempty"`
}

// Store owns one embedded broker shared by chat and durable management state.
// Readers take only a short memory lock; persistence never holds that lock.
// Callers must hold LockAccess while opening/using a persistent store directory.
type Store struct {
	chat       *chatStore
	kv         jetstream.KeyValue
	updateGate chan struct{}
	mu         sync.RWMutex
	record     resourceRecord
	revision   uint64
	closeOnce  sync.Once
}

// OpenStore imports legacy access files only when KV has never been initialized.
// Originals remain untouched. An existing invalid/deleted state fails closed.
func OpenStore(cfg ChatConfig, legacyDir string, seed []w.Channel) (_ *Store, err error) {
	if _, err := authorization(); err != nil {
		return nil, err
	}
	c, err := openChat(cfg)
	if err != nil {
		return nil, err
	}
	s := &Store{chat: c, updateGate: make(chan struct{}, 1)}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, lookupErr := c.js.KeyValue(ctx, "RESONA_RESOURCES")
	newBucket := errors.Is(lookupErr, jetstream.ErrBucketNotFound)
	if lookupErr != nil && !newBucket {
		return nil, lookupErr
	}
	var initial resourceRecord
	if newBucket {
		initial, err = importResources(legacyDir, seed)
		if err != nil {
			return nil, err
		}
	}
	s.kv, err = c.js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket: "RESONA_RESOURCES", Description: "Resona authoritative management state",
		Storage: jetstream.FileStorage, History: 1, MaxValueSize: 128 << 10, MaxBytes: 2 << 20,
	})
	if err != nil {
		return nil, err
	}
	entry, err := s.kv.Get(ctx, resourceKey)
	if errors.Is(err, jetstream.ErrKeyNotFound) && newBucket {
		// Get maps tombstones to ErrKeyNotFound. Only a newly created bucket
		// permits import, never an existing bucket whose state was deleted.
		data, _ := json.Marshal(initial)
		_, err = s.kv.Create(ctx, resourceKey, data)
		if err != nil {
			return nil, err
		}
		entry, err = s.kv.Get(ctx, resourceKey)
	}
	if err != nil {
		return nil, err
	}
	record, err := decodeResources(entry.Value())
	if err != nil {
		return nil, err
	}
	s.record, s.revision = record, entry.Revision()
	return s, nil
}

func (s *Store) Close() { s.closeOnce.Do(s.chat.close) }

func (s *Store) snapshot() resourceRecord {
	r, _ := s.state()
	return r
}

func (s *Store) state() (resourceRecord, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.record
	r.Channels.Channels = append([]w.Channel(nil), r.Channels.Channels...)
	r.Admins = append([]string(nil), r.Admins...)
	r.Credentials = append([]AdminCredential(nil), r.Credentials...)
	return r, s.revision
}

func decodeResources(data []byte) (resourceRecord, error) {
	var r resourceRecord
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	err := d.Decode(&r)
	// Version 1 bound the default to the first entry. Normalize once in memory;
	// the next successful CAS persists version 2 with an independent identity.
	if err == nil && r.Channels.Version == 1 && r.Channels.Default == 0 && len(r.Channels.Channels) > 0 {
		r.Channels.Default = r.Channels.Channels[0].ID
		r.Channels.Version = 2
	}
	var extra any
	if err != nil || len(data) > 128<<10 || d.Decode(&extra) != io.EOF || r.Version != 1 || !validChannelRecord(r.Channels) ||
		(r.Owner != "" && !validIdentity(r.Owner)) ||
		(r.Claim != (claimSecret{}) && (r.Claim.Version != 1 || !validIdentity(r.Claim.Digest) || r.Claim.Expires.IsZero())) ||
		(r.Owner != "" && r.Claim.Digest != "") {
		return r, errors.New("invalid resource state; original preserved")
	}
	seen := make(map[string]bool, len(r.Admins))
	if len(r.Admins) > 64 {
		return r, ErrInvalid
	}
	for _, id := range r.Admins {
		if !validIdentity(id) || id == r.Owner || seen[id] {
			return r, ErrInvalid
		}
		seen[id] = true
	}
	if len(r.Credentials) > 64 {
		return r, ErrInvalid
	}
	clear(seen)
	for _, c := range r.Credentials {
		if len(c.ID) != 26 || seen[c.ID] || !validIdentity(c.Identity) || !validIdentity(c.Digest) || !validName(c.Label, 64) || c.Expires.IsZero() {
			return r, ErrInvalid
		}
		seen[c.ID] = true
	}
	return r, nil
}

func validChannelRecord(r channelRecord) bool {
	if r.Version != 2 || !validChannels(r.Channels) || r.Next == 0 || r.Next > 65536 {
		return false
	}
	defaultFound := false
	for _, ch := range r.Channels {
		defaultFound = defaultFound || ch.ID == r.Default
		if uint32(ch.ID) >= r.Next {
			return false
		}
	}
	return defaultFound
}

func (s *Store) install(r resourceRecord, revision uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if revision <= s.revision {
		return nil
	}
	if err := validResourceTransition(s.record, r); err != nil {
		return err
	}
	s.record, s.revision = r, revision
	return nil
}

func validResourceTransition(before, after resourceRecord) error {
	if (before.Owner != "" && after.Owner != before.Owner) || after.Channels.Next < before.Channels.Next ||
		after.Channels.Default != before.Channels.Default {
		return errors.New("resource update violates immutable owner/default channel/ID sequence")
	}
	for _, ch := range after.Channels.Channels {
		found := false
		for _, old := range before.Channels.Channels {
			found = found || ch.ID == old.ID
		}
		if !found && uint32(ch.ID) < before.Channels.Next {
			return errors.New("resource update reused a deleted channel ID")
		}
	}
	return nil
}

// update serializes local writers and uses server-side CAS against other writers.
// fn may validate current membership, but no KV operation runs under the routing lock.
func (s *Store) update(fn func(*resourceRecord) error) error {
	return s.updateContext(context.Background(), fn)
}

func (s *Store) updateContext(parent context.Context, fn func(*resourceRecord) error) error {
	return s.updateAtContext(parent, 0, fn)
}

func (s *Store) updateAtContext(parent context.Context, revision uint64, fn func(*resourceRecord) error) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.updateGate <- struct{}{}:
	}
	defer func() { <-s.updateGate }()
	for range 3 {
		entry, err := s.kv.Get(ctx, resourceKey)
		if err != nil {
			return err
		}
		if revision != 0 && entry.Revision() != revision {
			return ErrConflict
		}
		r, err := decodeResources(entry.Value())
		if err != nil {
			return err
		}
		if err = s.install(r, entry.Revision()); err != nil {
			return err
		}
		// The cached immutable slice must never alias the mutation draft.
		r.Channels.Channels = append([]w.Channel(nil), r.Channels.Channels...)
		r.Admins = append([]string(nil), r.Admins...)
		r.Credentials = append([]AdminCredential(nil), r.Credentials...)
		before := r
		before.Channels.Channels = append([]w.Channel(nil), r.Channels.Channels...)
		if err = fn(&r); err != nil {
			return err
		}
		if err = validResourceTransition(before, r); err != nil {
			return err
		}
		data, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err = decodeResources(data); err != nil {
			return err
		}
		revision, err := s.kv.Update(ctx, resourceKey, data, entry.Revision())
		if errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
			continue
		}
		if err != nil {
			return err
		}
		return s.install(r, revision)
	}
	return ErrConflict
}

// watch receives the initial snapshot and subsequent revisions on one consumer,
// avoiding the gap between Get and an UpdatesOnly subscription. Serve owns it.
func (s *Store) watch(ctx context.Context, apply func(resourceRecord, uint64) error) error {
	watch, err := s.kv.Watch(ctx, resourceKey)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer watch.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.chat.closed:
			return errors.New("resource broker disconnected")
		case entry, ok := <-watch.Updates():
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return errors.New("resource watch stopped")
			}
			if entry == nil {
				continue
			}
			if entry.Operation() != jetstream.KeyValuePut {
				return errors.New("authoritative resource state was deleted")
			}
			r, err := decodeResources(entry.Value())
			if err != nil {
				return err
			}
			if err = apply(r, entry.Revision()); err != nil {
				return err
			}
		}
	}
}

func importResources(dir string, seed []w.Channel) (resourceRecord, error) {
	r := resourceRecord{Version: 1, Channels: channelRecord{Version: 1, Next: 1, Channels: seed}}
	for _, ch := range seed {
		r.Channels.Next = max(r.Channels.Next, uint32(ch.ID)+1)
	}
	if dir != "" {
		for _, file := range []struct {
			name string
			into any
			max  int64
		}{{"channels.json", &r.Channels, 65536}, {"owner.json", new(ownerRecord), 1024}, {"claim.json", &r.Claim, 1024}} {
			// A consumed legacy claim can be expired or malformed; the owner wins.
			if file.name == "claim.json" && r.Owner != "" {
				continue
			}
			data, err := nativeidentity.ReadPrivate(filepath.Join(dir, file.name), file.max)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return r, err
			}
			d := json.NewDecoder(bytes.NewReader(data))
			d.DisallowUnknownFields()
			var extra any
			if d.Decode(file.into) != nil || d.Decode(&extra) != io.EOF {
				return r, errors.New("invalid legacy resource file; original preserved")
			}
			if owner, ok := file.into.(*ownerRecord); ok {
				if owner.Version != 1 || !validIdentity(owner.Identity) {
					return r, errors.New("invalid legacy owner; original preserved")
				}
				r.Owner = owner.Identity
			}
		}
	}
	data, _ := json.Marshal(r)
	return decodeResources(data)
}
