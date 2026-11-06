package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/tsukiyoz/resona/internal/serverauth"
)

const (
	authenticationBucket = "RESONA_AUTH"
	passwordKey          = "server-password"
)

type passwordRecord struct {
	Version    int    `json:"version"`
	Revision   string `json:"revision"`
	Credential []byte `json:"credential"`
}

type passwordState struct {
	auth       *serverauth.Server
	revision   string
	kvRevision uint64
}

func (s *Store) openAuthentication(ctx context.Context) error {
	initialized := s.snapshot().AuthenticationInitialized
	kv, err := s.chat.js.KeyValue(ctx, authenticationBucket)
	newBucket := errors.Is(err, jetstream.ErrBucketNotFound)
	if newBucket && !initialized {
		kv, err = s.chat.js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket: authenticationBucket, Description: "Private Resona server authentication credentials",
			Storage: jetstream.FileStorage, History: 1, MaxValueSize: 8 << 10, MaxBytes: 64 << 10,
		})
	}
	if err != nil {
		return errors.New("server authentication store unavailable")
	}
	s.authKV = kv
	status, err := kv.Status(ctx)
	if err != nil || status.TTL() != 0 || status.History() != 1 {
		return errors.New("invalid server authentication store configuration")
	}
	stream, err := s.chat.js.Stream(ctx, "KV_"+authenticationBucket)
	if err != nil || stream.CachedInfo().Config.Storage != jetstream.FileStorage {
		return errors.New("server authentication requires durable file storage")
	}
	if newBucket {
		auth, _ := serverauth.New("")
		data, err := encodePassword(auth, rand.Text())
		if err != nil {
			return err
		}
		if _, err = kv.Create(ctx, passwordKey, data); err != nil {
			return errors.New("initialize server authentication failed")
		}
	}
	if _, err := s.readPassword(ctx); err != nil {
		return err
	}
	if !initialized {
		return s.updateContext(ctx, func(r *resourceRecord) error {
			r.AuthenticationInitialized = true
			return nil
		})
	}
	return nil
}

func encodePassword(auth *serverauth.Server, revision string) ([]byte, error) {
	credential, err := auth.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return json.Marshal(passwordRecord{Version: 1, Revision: revision, Credential: credential})
}

func (s *Store) readPassword(ctx context.Context) (passwordState, error) {
	entry, err := s.authKV.Get(ctx, passwordKey)
	if err != nil {
		return passwordState{}, errors.New("server authentication record unavailable")
	}
	var r passwordRecord
	d := json.NewDecoder(bytes.NewReader(entry.Value()))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&r) != nil || r.Version != 1 || len(r.Revision) < 26 || len(r.Revision) > 128 || d.Decode(&extra) != io.EOF {
		return passwordState{}, serverauth.ErrInvalidRecord
	}
	auth, err := serverauth.Parse(r.Credential)
	if err != nil {
		return passwordState{}, err
	}
	return passwordState{auth: auth, revision: r.Revision, kvRevision: entry.Revision()}, nil
}

// replacePassword returns authoritative state even after a lost write response.
// If readback also fails, callers must gate new admission until a healthy restart.
func (s *Store) replacePassword(ctx context.Context, auth *serverauth.Server, expected passwordState) (passwordState, error) {
	before, err := s.readPassword(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return expected, ctx.Err()
		}
		return passwordState{}, err
	}
	if before.revision != expected.revision {
		return before, ErrConflict
	}
	next := passwordState{auth: auth, revision: rand.Text()}
	data, err := encodePassword(auth, next.revision)
	if err != nil {
		return before, err
	}
	if err = ctx.Err(); err != nil {
		return before, err
	}
	next.kvRevision, err = s.authKV.Update(ctx, passwordKey, data, before.kvRevision)
	if err == nil {
		return next, nil
	}
	if errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
		err = ErrConflict
	}
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	actual, readErr := s.readPassword(readCtx)
	if readErr != nil {
		return passwordState{}, errors.New("server authentication outcome unknown; restore storage and restart")
	}
	return actual, err
}

// SetConnectionPassword is an offline operation under LockAccess. It never
// returns or stores the plaintext; the caller owns any once-only CLI output.
func (s *Store) SetConnectionPassword(ctx context.Context, password string) error {
	if len(password) < 8 || !validConnectionPassword(password) {
		return ErrInvalid
	}
	auth, err := serverauth.New(password)
	if err != nil {
		return err
	}
	before, err := s.readPassword(ctx)
	if err != nil {
		return err
	}
	_, err = s.replacePassword(ctx, auth, before)
	return err
}
