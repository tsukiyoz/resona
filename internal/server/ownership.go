package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"time"
)

type claimSecret struct {
	Version int       `json:"version"`
	Digest  string    `json:"digest"`
	Expires time.Time `json:"expires"`
}

// ownerRecord is retained only for importing pre-KV installations.
type ownerRecord struct {
	Version  int    `json:"version"`
	Identity string `json:"identity"`
}

func validIdentity(id string) bool {
	data, err := hex.DecodeString(id)
	return err == nil && len(data) == 32 && id == hex.EncodeToString(data)
}

// ProvisionOwnerClaim is an offline operation under LockAccess. Only the digest
// and expiry are persisted. Replacing an unused claim never replaces an owner.
func (s *Store) ProvisionOwnerClaim(replace bool) (string, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return "", err
	}
	encoded := hex.EncodeToString(token)
	digest := sha256.Sum256([]byte(encoded))
	err := s.update(func(r *resourceRecord) error {
		if r.Owner != "" {
			return errors.New("server already has an owner")
		}
		if !replace && r.Claim.Digest != "" {
			return errors.New("owner claim already exists; use owner reset-claim")
		}
		r.Claim = claimSecret{Version: 1, Digest: hex.EncodeToString(digest[:]), Expires: time.Now().UTC().Add(24 * time.Hour)}
		return nil
	})
	if err != nil {
		return "", err
	}
	return encoded, nil
}

// Status is a memory-only lookup, safe while the server holds its routing mutex.
func (s *Store) Status(identity string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.record.Owner == identity && identity != "" {
		return "owner", false
	}
	return "member", s.record.Owner == "" && s.record.Claim.Digest != "" && time.Now().Before(s.record.Claim.Expires)
}

// Claim atomically installs the immutable owner and consumes the one-use code.
func (s *Store) Claim(identity, token string) error {
	return s.claim(context.Background(), identity, token)
}

func (s *Store) claim(ctx context.Context, identity, token string) error {
	return s.updateContext(ctx, func(r *resourceRecord) error {
		if !validIdentity(identity) || r.Owner != "" || !time.Now().Before(r.Claim.Expires) || len(token) != 64 {
			return errors.New("owner claim rejected")
		}
		digest := sha256.Sum256([]byte(token))
		want, err := hex.DecodeString(r.Claim.Digest)
		if err != nil || len(want) != 32 || subtle.ConstantTimeCompare(digest[:], want) != 1 {
			return errors.New("owner claim rejected")
		}
		r.Owner, r.Claim = identity, claimSecret{}
		return nil
	})
}
