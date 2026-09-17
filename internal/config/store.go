// Package config persists server bookmarks without authentication secrets.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tsukiyoz/resona/internal/client"
)

type Store struct {
	path string
}

func New(path string) *Store { return &Store{path: path} }

func NewDefault() (*Store, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return New(filepath.Join(directory, "resona", "servers.json")), nil
}

func (s *Store) Load() ([]client.ServerProfile, error) {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return []client.ServerProfile{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", s.path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > 1024*1024 {
		return nil, fmt.Errorf("read %s: server profiles exceed the 1 MiB limit", s.path)
	}
	var stored []struct {
		client.ServerProfile
		RetiredTrust json.RawMessage `json:"certificateFingerprint,omitempty"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1024*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("read %s: unexpected trailing data", s.path)
	}
	profiles := make([]client.ServerProfile, 0, len(stored))
	for _, entry := range stored {
		profiles = append(profiles, entry.ServerProfile)
	}
	if err := validate(profiles); err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	return profiles, nil
}

func (s *Store) Save(profiles []client.ServerProfile) error {
	if err := validate(profiles); err != nil {
		return err
	}
	if profiles == nil {
		profiles = []client.ServerProfile{}
	}
	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > 1024*1024 {
		return errors.New("server profiles exceed the 1 MiB limit")
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".servers-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, s.path); err != nil {
		return err
	}
	return nil
}

func validate(profiles []client.ServerProfile) error {
	ids := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		if profile.ID == "" || ids[profile.ID] {
			return errors.New("server profiles require unique nonempty IDs")
		}
		ids[profile.ID] = true
		// Obsolete bookmarks remain editable/deletable, but cannot connect.
		// Validate current records strictly; never discard the whole file merely
		// because it also contains a bookmark from an unsupported version.
		if profile.Protocol == "resona-noise" {
			if err := client.ValidateProfile(profile); err != nil {
				return err
			}
		}
	}
	return nil
}
