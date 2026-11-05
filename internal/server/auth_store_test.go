package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	w "github.com/tsukiyoz/resona/internal/nativewire"
	"github.com/tsukiyoz/resona/internal/serverauth"
)

func checkStoredPassword(t *testing.T, auth *serverauth.Server, password string, want bool) {
	t.Helper()
	a, b := net.Pipe()
	defer b.Close()
	_ = a.SetDeadline(time.Now().Add(2 * time.Second))
	_ = b.SetDeadline(time.Now().Add(2 * time.Second))
	done := make(chan error, 1)
	go func() { defer a.Close(); done <- auth.Accept(a, make([]byte, 32)) }()
	err := serverauth.Authenticate(b, make([]byte, 32), password, password == "")
	_ = b.Close()
	serverErr := <-done
	if (err == nil) != want || (want && serverErr != nil) {
		t.Fatalf("client=%v server=%v want success=%v", err, serverErr, want)
	}
}

func TestPrivateAuthenticationSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)
	ctx := context.Background()
	if err := s.SetConnectionPassword(ctx, "durable-shared-password"); err != nil {
		t.Fatal(err)
	}
	before, err := s.readPassword(ctx)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := s.authKV.Get(ctx, passwordKey)
	if err != nil {
		t.Fatal(err)
	}
	credential, _ := before.auth.MarshalBinary()
	resources, _ := json.Marshal(s.snapshot())
	if bytes.Contains(entry.Value(), []byte("durable-shared-password")) || bytes.Contains(resources, credential) || bytes.Contains(resources, []byte(`"credential"`)) {
		t.Fatal("credential exposed outside private auth KV")
	}
	s.Close()
	s = testStore(t, dir)
	after, err := s.readPassword(ctx)
	if err != nil || before.revision != after.revision {
		t.Fatal("authentication revision not durable", err)
	}
	restored, _ := after.auth.MarshalBinary()
	if !bytes.Equal(credential, restored) {
		t.Fatal("restart regenerated authentication keys")
	}
	checkStoredPassword(t, after.auth, "durable-shared-password", true)
	checkStoredPassword(t, after.auth, "wrong-password", false)
}

func TestPrivateAuthenticationMissingOrInvalidFailsClosed(t *testing.T) {
	for _, fault := range []string{"key", "bucket", "invalid", "ttl"} {
		t.Run(fault, func(t *testing.T) {
			dir := t.TempDir()
			s := testStore(t, dir)
			ctx := context.Background()
			var err error
			switch fault {
			case "key":
				err = s.authKV.Delete(ctx, passwordKey)
			case "bucket":
				err = s.chat.js.DeleteKeyValue(ctx, authenticationBucket)
			case "invalid":
				_, err = s.authKV.Put(ctx, passwordKey, []byte(`{"version":999}`))
			case "ttl":
				_, err = s.chat.js.UpdateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: authenticationBucket, Storage: jetstream.FileStorage, History: 1, TTL: time.Hour})
			}
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			store, err := OpenStore(ChatConfig{Directory: filepath.Join(dir, "chat")}, dir, []w.Channel{{ID: 1, Name: "Lobby"}})
			if err == nil {
				store.Close()
				t.Fatal("invalid auth silently fell back to public mode")
			}
		})
	}
}

type lostPasswordAck struct {
	jetstream.KeyValue
	written, failReadback bool
}

func (k *lostPasswordAck) Update(ctx context.Context, key string, value []byte, last uint64) (uint64, error) {
	_, err := k.KeyValue.Update(ctx, key, value, last)
	if err != nil {
		return 0, err
	}
	k.written = true
	return 0, context.DeadlineExceeded
}

func (k *lostPasswordAck) Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
	if k.written && k.failReadback {
		return nil, errors.New("readback unavailable")
	}
	return k.KeyValue.Get(ctx, key)
}

func TestPasswordUnknownWriteOutcomeReconcilesOrGatesAdmission(t *testing.T) {
	for _, failReadback := range []bool{false, true} {
		name := "reconcile"
		if failReadback {
			name = "gate"
		}
		t.Run(name, func(t *testing.T) {
			s, owner, _ := channelTestServer(t)
			before, err := s.config.Store.readPassword(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			s.passwordAuth, s.passwordStatus = before.auth, before.status()
			kv := s.config.Store.authKV
			s.config.Store.authKV = &lostPasswordAck{KeyValue: kv, failReadback: failReadback}
			err = s.SetConnectionPassword(context.Background(), owner.identity, "replacement-password", before.revision)
			if err == nil {
				t.Fatal("uncertain write reported success")
			}
			if failReadback {
				if s.passwordAuth != nil || s.passwordStatus.Editable {
					t.Fatal("unknown state still admits members")
				}
			} else {
				checkStoredPassword(t, s.passwordAuth, "replacement-password", true)
				if s.passwordStatus.Revision == before.revision {
					t.Fatal("committed revision not published")
				}
			}
			s.config.Store.authKV = kv
			actual, err := s.config.Store.readPassword(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			checkStoredPassword(t, actual.auth, "replacement-password", true)
		})
	}
}
