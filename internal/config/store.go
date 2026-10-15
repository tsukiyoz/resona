// Package config persists server bookmarks without authentication secrets.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tsukiyoz/resona/internal/client"
)

const (
	SchemaVersion = 1
	maxBytes      = 1024 * 1024
)

type storedProfile struct {
	client.ServerProfile
	RetiredTrust json.RawMessage `json:"certificateFingerprint,omitempty"`
}

type document struct {
	SchemaVersion int             `json:"schema_version"`
	Servers       []storedProfile `json:"servers"`
}

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
	profiles, _, _, err := s.read()
	return profiles, err
}

func (s *Store) read() ([]client.ServerProfile, int, []byte, error) {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return []client.ServerProfile{}, SchemaVersion, nil, nil
	}
	if err != nil {
		return nil, 0, nil, fmt.Errorf("open %s: %w", s.path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, 0, nil, err
	}
	if len(data) > maxBytes {
		return nil, 0, nil, fmt.Errorf("read %s: server profiles exceed the 1 MiB limit", s.path)
	}
	var stored []storedProfile
	version := 0
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		err = decoder.Decode(&stored)
	} else {
		var doc document
		err = decoder.Decode(&doc)
		if err == nil && (doc.SchemaVersion != SchemaVersion || doc.Servers == nil) {
			err = errors.New("unsupported server profiles schema or missing servers array")
		}
		stored, version = doc.Servers, doc.SchemaVersion
	}
	if err != nil {
		return nil, 0, nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, 0, nil, fmt.Errorf("read %s: unexpected trailing data", s.path)
	}
	profiles := make([]client.ServerProfile, 0, len(stored))
	for _, entry := range stored {
		profiles = append(profiles, entry.ServerProfile)
	}
	if err := validate(profiles); err != nil {
		return nil, 0, nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	return profiles, version, data, nil
}

func (s *Store) Save(profiles []client.ServerProfile) error {
	if err := validate(profiles); err != nil {
		return err
	}
	if profiles == nil {
		profiles = []client.ServerProfile{}
	}
	_, version, original, err := s.read()
	if err != nil {
		return fmt.Errorf("refusing to overwrite unreadable server profiles: %w", err)
	}
	data, err := json.MarshalIndent(struct {
		SchemaVersion int                    `json:"schema_version"`
		Servers       []client.ServerProfile `json:"servers"`
	}{SchemaVersion, profiles}, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > maxBytes {
		return errors.New("server profiles exceed the 1 MiB limit")
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	if version == 0 {
		if err := s.backup(original); err != nil {
			return fmt.Errorf("back up server profiles before migration: %w", err)
		}
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

func (s *Store) backup(data []byte) error {
	path := s.path + ".pre-v1.bak"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		existing, err := os.Open(path)
		if err != nil {
			return err
		}
		defer existing.Close()
		previous, err := io.ReadAll(io.LimitReader(existing, int64(len(data))+1))
		if err != nil {
			return err
		}
		if !bytes.Equal(previous, data) {
			return errors.New("migration backup already exists with different contents")
		}
		return nil
	}
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		file.Close()
		if !complete {
			os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	complete = true
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
