//go:build darwin && cgo

package credentials

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	keychain "github.com/keybase/go-keychain"
)

const serviceName = "io.github.tsukiyoz.resona.server-password"

type passwordPayload struct {
	Version  int     `json:"version"`
	Password *string `json:"password"`
}

type keychainAPI struct {
	query  func(keychain.Item) ([]keychain.QueryResult, error)
	update func(keychain.Item, keychain.Item) error
	add    func(keychain.Item) error
	delete func(keychain.Item) error
}

type Store struct {
	api keychainAPI
}

func New() *Store {
	return &Store{api: keychainAPI{
		query: keychain.QueryItem, update: keychain.UpdateItem,
		add: keychain.AddItem, delete: keychain.DeleteItem,
	}}
}

func (s *Store) Has(key string) (bool, error) {
	query, err := itemForKey(key)
	if err != nil {
		return false, err
	}
	query.SetMatchLimit(keychain.MatchLimitOne)
	query.SetReturnAttributes(true)
	query.SetReturnData(false)
	disallowAuthenticationUI(&query)
	results, err := s.api.query(query)
	// A locked/protected item is a candidate, not a missing password. Let the
	// explicit Get perform authentication once; never prompt just for status.
	if errors.Is(err, keychain.ErrorInteractionNotAllowed) {
		return true, nil
	}
	if errors.Is(err, keychain.ErrorItemNotFound) {
		return false, nil
	}
	if err != nil || len(results) > 1 {
		return false, errRead
	}
	return len(results) == 1, nil
}

func (s *Store) Get(key string) (string, error) {
	query, err := itemForKey(key)
	if err != nil {
		return "", err
	}
	query.SetMatchLimit(keychain.MatchLimitOne)
	query.SetReturnData(true)
	results, err := s.api.query(query)
	if errors.Is(err, keychain.ErrorItemNotFound) || (err == nil && len(results) == 0) {
		return "", ErrNotFound
	}
	if err != nil || len(results) != 1 {
		return "", errRead
	}
	var payload passwordPayload
	if err := json.Unmarshal(results[0].Data, &payload); err != nil || payload.Version != 1 || payload.Password == nil {
		return "", errRead
	}
	return *payload.Password, nil
}

func (s *Store) Set(key, value string) error {
	query, err := itemForKey(key)
	if err != nil {
		return err
	}
	if !utf8.ValidString(value) {
		return errWrite
	}
	// Nonempty encoded data makes replacing a password with an empty one work
	// with the legacy macOS keychain, which can ignore zero-byte updates.
	data, err := json.Marshal(passwordPayload{Version: 1, Password: &value})
	if err != nil {
		return errWrite
	}
	changes := keychain.NewItem()
	changes.SetData(data)
	err = s.api.update(query, changes)
	if err == nil {
		return nil
	}
	if !errors.Is(err, keychain.ErrorItemNotFound) {
		return errWrite
	}
	item, _ := itemForKey(key)
	item.SetLabel("Resona server password")
	item.SetData(data)
	item.SetAccessible(keychain.AccessibleWhenUnlocked)
	err = s.api.add(item)
	// Another process may create the item after Update reports it missing.
	if errors.Is(err, keychain.ErrorDuplicateItem) {
		err = s.api.update(query, changes)
	}
	if err != nil {
		return errWrite
	}
	return nil
}

func (s *Store) Delete(key string) error {
	query, err := itemForKey(key)
	if err != nil {
		return err
	}
	if err := s.api.delete(query); err != nil && !errors.Is(err, keychain.ErrorItemNotFound) {
		return errDelete
	}
	return nil
}

func itemForKey(key string) (keychain.Item, error) {
	if key == "" || !utf8.ValidString(key) || strings.ContainsRune(key, 0) {
		return keychain.Item{}, errInvalidKey
	}
	item := keychain.NewItem()
	item.SetSecClass(keychain.SecClassGenericPassword)
	item.SetService(serviceName)
	item.SetAccount(key)
	item.SetSynchronizable(keychain.SynchronizableNo)
	return item, nil
}
