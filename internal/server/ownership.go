package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tsukiyoz/resona/internal/nativeidentity"
)

type claimSecret struct {
	Version int       `json:"version"`
	Digest  string    `json:"digest"`
	Expires time.Time `json:"expires"`
}
type ownerRecord struct {
	Version  int    `json:"version"`
	Identity string `json:"identity"`
}

// Ownership persists a single immutable owner record. Atomic no-replace
// publication also prevents two server processes from claiming different owners.
type Ownership struct {
	mu    sync.Mutex
	dir   string
	owner string
	claim claimSecret
}

func OpenOwnership(dir string) (*Ownership, error) {
	s := &Ownership{dir: dir}
	if err := s.loadOwner(); err != nil {
		return nil, err
	}
	if s.owner != "" {
		return s, nil
	}
	data, err := nativeidentity.ReadPrivate(filepath.Join(dir, "claim.json"), 1024)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(data, &s.claim) != nil || s.claim.Version != 1 || !validIdentity(s.claim.Digest) || s.claim.Expires.IsZero() {
		return nil, errors.New("invalid owner claim file; original preserved")
	}
	return s, nil
}

func validIdentity(id string) bool {
	data, err := hex.DecodeString(id)
	return err == nil && len(data) == 32 && id == hex.EncodeToString(data)
}

func (s *Ownership) loadOwner() error {
	data, err := nativeidentity.ReadPrivate(filepath.Join(s.dir, "owner.json"), 1024)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var record ownerRecord
	if json.Unmarshal(data, &record) != nil || record.Version != 1 || !validIdentity(record.Identity) {
		return errors.New("invalid owner record; original preserved")
	}
	s.owner = record.Identity
	return nil
}

// InitOwnerClaim is an offline provisioning operation. The operator receives
// the token once; only its digest and expiry are stored. It never overwrites.
func InitOwnerClaim(dir string) (string, error) {
	return provisionOwnerClaim(dir, false)
}

// ResetOwnerClaim is offline only, under the same access lock as server startup.
// It invalidates an unused code even if it has not expired, never an owner.
func ResetOwnerClaim(dir string) (string, error) {
	return provisionOwnerClaim(dir, true)
}

func provisionOwnerClaim(dir string, replace bool) (string, error) {
	s, err := OpenOwnership(dir)
	if err != nil {
		return "", err
	}
	if s.owner != "" {
		return "", errors.New("server already has an owner")
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return "", err
	}
	encoded := hex.EncodeToString(token)
	digest := sha256.Sum256([]byte(encoded))
	data, err := json.Marshal(claimSecret{Version: 1, Digest: hex.EncodeToString(digest[:]), Expires: time.Now().UTC().Add(24 * time.Hour)})
	if err != nil {
		return "", err
	}
	write := nativeidentity.WriteNew
	if replace {
		write = nativeidentity.ReplacePrivate
	}
	if err = write(filepath.Join(dir, "claim.json"), data); err != nil {
		return "", err
	}
	return encoded, nil
}

func (s *Ownership) Status(identity string) (string, bool) {
	if s == nil {
		return "member", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner == identity && identity != "" {
		return "owner", false
	}
	return "member", s.owner == "" && s.claim.Digest != "" && time.Now().Before(s.claim.Expires)
}

func (s *Ownership) Claim(identity, token string) error {
	if s == nil {
		return errors.New("owner claim unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validIdentity(identity) || s.owner != "" || !time.Now().Before(s.claim.Expires) || len(token) != 64 {
		return errors.New("owner claim rejected")
	}
	digest := sha256.Sum256([]byte(token))
	want, err := hex.DecodeString(s.claim.Digest)
	if err != nil || len(want) != 32 || subtle.ConstantTimeCompare(digest[:], want) != 1 {
		return errors.New("owner claim rejected")
	}
	data, _ := json.Marshal(ownerRecord{Version: 1, Identity: identity})
	err = nativeidentity.WriteNew(filepath.Join(s.dir, "owner.json"), data)
	// A competing process or a directory-sync failure may have published a
	// complete record. Reload so no later operation can reuse the claim in memory.
	if err != nil {
		_ = s.loadOwner()
		return errors.New("owner claim could not be confirmed")
	}
	s.owner = identity
	s.claim = claimSecret{}
	return nil
}
