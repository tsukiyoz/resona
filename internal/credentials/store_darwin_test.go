//go:build darwin && cgo

package credentials

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	keychain "github.com/keybase/go-keychain"
)

func TestConcurrentReadsReuseAuthorizedPassword(t *testing.T) {
	store := fakeStore(t)
	reads := 0
	store.api.query = func(keychain.Item) ([]keychain.QueryResult, error) {
		reads++
		return []keychain.QueryResult{{Data: []byte(`{"version":1,"password":"cached-only"}`)}}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if value, err := store.Get("destination"); err != nil || value != "cached-only" {
				t.Error("read failed")
			}
		}()
	}
	wg.Wait()
	if found, err := store.Has("destination"); err != nil || !found {
		t.Fatal("cached status missing")
	}
	if err := store.Set("destination", "cached-only"); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("expected one authorized read, got %d", reads)
	}
}

func TestDirectPasswordReadIsSingleAndCached(t *testing.T) {
	store := fakeStore(t)
	reads := 0
	store.api.querySilent = func(keychain.Item) ([]keychain.QueryResult, error) {
		return nil, keychain.ErrorInteractionNotAllowed
	}
	store.api.read = func(key string) ([]byte, error) {
		reads++
		if key != "protected" {
			t.Fatal("read escaped selected destination")
		}
		return []byte(`{"version":1,"password":"test-only"}`), nil
	}
	if found, err := store.Has("protected"); !found || err != nil {
		t.Fatal("protected item not available for explicit reading")
	}
	for i := 0; i < 3; i++ {
		if value, err := store.Get("protected"); err != nil || value != "test-only" {
			t.Fatal("direct password read failed")
		}
	}
	if err := store.Set("protected", "test-only"); err != nil || reads != 1 {
		t.Fatal("read or same-value write caused redundant authorization")
	}
}

func TestDirectPasswordReadDenialIsNotRetriedOrCached(t *testing.T) {
	store := fakeStore(t)
	reads := 0
	store.api.read = func(string) ([]byte, error) {
		reads++
		return nil, keychain.ErrorAuthFailed
	}
	if _, err := store.Get("protected"); !errors.Is(err, errRead) || reads != 1 || len(store.cache) != 0 {
		t.Fatal("denied direct read was retried or cached")
	}
}

func TestCacheInvalidatesOnMutationAndDoesNotCacheDenial(t *testing.T) {
	store := fakeStore(t)
	reads := 0
	store.api.query = func(keychain.Item) ([]keychain.QueryResult, error) {
		reads++
		if reads == 1 {
			return nil, keychain.ErrorAuthFailed
		}
		return []keychain.QueryResult{{Data: []byte(`{"version":1,"password":"old"}`)}}, nil
	}
	if _, err := store.Get("destination"); err == nil {
		t.Fatal("denial accepted")
	}
	if _, err := store.Get("destination"); err != nil {
		t.Fatal(err)
	}
	store.api.update = func(keychain.Item, keychain.Item) error { return keychain.ErrorAuthFailed }
	if err := store.Set("destination", "new"); err == nil {
		t.Fatal("failed write accepted")
	}
	if _, ok := store.cache["destination"]; ok {
		t.Fatal("failed write retained cache")
	}
	store.api.update = func(keychain.Item, keychain.Item) error { return nil }
	if err := store.Set("destination", ""); err != nil {
		t.Fatal(err)
	}
	if value, err := store.Get("destination"); err != nil || value != "" {
		t.Fatal("empty replacement lost")
	}
	store.api.delete = func(keychain.Item) error { return keychain.ErrorAuthFailed }
	if err := store.Delete("destination"); err == nil {
		t.Fatal("failed delete accepted")
	}
	if _, ok := store.cache["destination"]; ok {
		t.Fatal("delete attempt retained cache")
	}
}

func TestCacheIsDestinationAndInstanceScoped(t *testing.T) {
	store := fakeStore(t)
	reads := 0
	store.api.query = func(keychain.Item) ([]keychain.QueryResult, error) {
		reads++
		return []keychain.QueryResult{{Data: []byte(`{"version":1,"password":""}`)}}, nil
	}
	other := &Store{api: store.api}
	for _, key := range []string{"a", "b", "a"} {
		if _, err := store.Get(key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := other.Get("a"); err != nil {
		t.Fatal(err)
	}
	if reads != 3 {
		t.Fatalf("incorrect cache scope: %d reads", reads)
	}
}

func fakeStore(t *testing.T) *Store {
	t.Helper()
	return &Store{api: keychainAPI{
		query: func(keychain.Item) ([]keychain.QueryResult, error) {
			t.Fatal("unexpected query")
			return nil, nil
		},
		update: func(keychain.Item, keychain.Item) error { t.Fatal("unexpected update"); return nil },
		add:    func(keychain.Item) error { t.Fatal("unexpected add"); return nil },
		delete: func(keychain.Item) error { t.Fatal("unexpected delete"); return nil },
	}}
}

func expectedQuery(key string) keychain.Item {
	item := keychain.NewItem()
	item.SetSecClass(keychain.SecClassGenericPassword)
	item.SetService("io.github.tsukiyoz.resona.server-password")
	item.SetAccount(key)
	item.SetSynchronizable(keychain.SynchronizableNo)
	return item
}

func TestHasRequestsAttributesWithoutSecretData(t *testing.T) {
	store := fakeStore(t)
	store.api.query = func(query keychain.Item) ([]keychain.QueryResult, error) {
		want := expectedQuery("profile-key")
		want.SetMatchLimit(keychain.MatchLimitOne)
		want.SetReturnAttributes(true)
		want.SetReturnData(false)
		disallowAuthenticationUI(&want)
		if !reflect.DeepEqual(query, want) {
			t.Fatal("existence query must select only this local item and return attributes")
		}
		return []keychain.QueryResult{{Account: "profile-key"}}, nil
	}
	if found, err := store.Has("profile-key"); err != nil || !found {
		t.Fatalf("existing item was not found: %v", err)
	}
}

func TestGetDistinguishesEmptyPasswordFromMissingItem(t *testing.T) {
	for _, tc := range []struct {
		name    string
		results []keychain.QueryResult
		err     error
		want    string
		wantErr error
	}{
		{name: "password", results: []keychain.QueryResult{{Data: []byte(`{"version":1,"password":"test-only"}`)}}, want: "test-only"},
		{name: "empty password", results: []keychain.QueryResult{{Data: []byte(`{"version":1,"password":""}`)}}},
		{name: "special characters", results: []keychain.QueryResult{{Data: []byte(`{"version":1,"password":"密碼\n\"\\\u0000"}`)}}, want: "密碼\n\"\\\x00"},
		{name: "raw data rejected", results: []keychain.QueryResult{{Data: []byte("test-only")}}, wantErr: errRead},
		{name: "empty data rejected", results: []keychain.QueryResult{{Data: []byte{}}}, wantErr: errRead},
		{name: "unknown version", results: []keychain.QueryResult{{Data: []byte(`{"version":2,"password":"test-only"}`)}}, wantErr: errRead},
		{name: "missing password", results: []keychain.QueryResult{{Data: []byte(`{"version":1}`)}}, wantErr: errRead},
		{name: "null password", results: []keychain.QueryResult{{Data: []byte(`{"version":1,"password":null}`)}}, wantErr: errRead},
		{name: "missing result", wantErr: ErrNotFound},
		{name: "missing status", err: keychain.ErrorItemNotFound, wantErr: ErrNotFound},
		{name: "denied", err: keychain.ErrorAuthFailed, wantErr: errRead},
		{name: "invalid count", results: []keychain.QueryResult{{}, {}}, wantErr: errRead},
		{name: "backend detail", err: errors.New("secret-backend-detail"), wantErr: errRead},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := fakeStore(t)
			store.api.query = func(query keychain.Item) ([]keychain.QueryResult, error) {
				want := expectedQuery("profile-key")
				want.SetMatchLimit(keychain.MatchLimitOne)
				want.SetReturnData(true)
				if !reflect.DeepEqual(query, want) {
					t.Fatal("password query is not scoped to the intended local item")
				}
				return tc.results, tc.err
			}
			value, err := store.Get("profile-key")
			if value != tc.want || !errors.Is(err, tc.wantErr) {
				t.Fatalf("incorrect result or unsanitized error: %v", err)
			}
		})
	}
}

func TestHasMissingAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"missing result", nil, nil},
		{"missing status", keychain.ErrorItemNotFound, nil},
		{"backend detail", errors.New("secret-backend-detail"), errRead},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := fakeStore(t)
			store.api.query = func(keychain.Item) ([]keychain.QueryResult, error) { return nil, tc.err }
			found, err := store.Has("profile-key")
			if found || !errors.Is(err, tc.want) {
				t.Fatalf("incorrect existence result: %v", err)
			}
		})
	}
}

func TestStatusDefersAuthenticationToSinglePasswordRead(t *testing.T) {
	store := fakeStore(t)
	queries := 0
	store.api.query = func(query keychain.Item) ([]keychain.QueryResult, error) {
		queries++
		want := expectedQuery("protected")
		want.SetMatchLimit(keychain.MatchLimitOne)
		if queries == 1 {
			want.SetReturnAttributes(true)
			want.SetReturnData(false)
			want.SetString(authenticationUIKey, authenticationUIFail)
			if !reflect.DeepEqual(query, want) {
				t.Fatal("status query must fail rather than show authentication UI")
			}
			return nil, keychain.ErrorInteractionNotAllowed
		}
		want.SetReturnData(true)
		if queries != 2 || !reflect.DeepEqual(query, want) {
			t.Fatal("only the explicit password read may request authentication")
		}
		return []keychain.QueryResult{{Data: []byte(`{"version":1,"password":"test-only"}`)}}, nil
	}
	if found, err := store.Has("protected"); !found || err != nil {
		t.Fatalf("protected credential cannot be selected for reading: %v", err)
	}
	if password, err := store.Get("protected"); err != nil || password != "test-only" || queries != 2 {
		t.Fatalf("expected exactly one password read: queries=%d error=%v", queries, err)
	}
}

func TestSetUpdatesAtomicallyAndHandlesConcurrentCreation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		value      string
		updates    []error
		addErr     error
		wantAdds   int
		wantResult error
	}{
		{name: "replace", value: "test-only", updates: []error{nil}},
		{name: "empty replacement", updates: []error{nil}},
		{name: "special character replacement", value: "密碼\n\"\\\x00", updates: []error{nil}},
		{name: "create", value: "test-only", updates: []error{keychain.ErrorItemNotFound}, wantAdds: 1},
		{name: "create empty", updates: []error{keychain.ErrorItemNotFound}, wantAdds: 1},
		{name: "concurrent creation", updates: []error{keychain.ErrorItemNotFound, nil}, addErr: keychain.ErrorDuplicateItem, wantAdds: 1},
		{name: "update denied", updates: []error{keychain.ErrorAuthFailed}, wantResult: errWrite},
		{name: "create denied", updates: []error{keychain.ErrorItemNotFound}, addErr: keychain.ErrorAuthFailed, wantAdds: 1, wantResult: errWrite},
		{name: "retry denied", updates: []error{keychain.ErrorItemNotFound, errors.New("secret-backend-detail")}, addErr: keychain.ErrorDuplicateItem, wantAdds: 1, wantResult: errWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := fakeStore(t)
			updates, adds := 0, 0
			data, err := json.Marshal(struct {
				Version  int    `json:"version"`
				Password string `json:"password"`
			}{Version: 1, Password: tc.value})
			if err != nil {
				t.Fatal(err)
			}
			store.api.update = func(query, changes keychain.Item) error {
				wantChanges := keychain.NewItem()
				wantChanges.SetData(data)
				if !reflect.DeepEqual(query, expectedQuery("profile-key")) || !reflect.DeepEqual(changes, wantChanges) {
					t.Fatal("update must target one local item and change only its data")
				}
				if updates >= len(tc.updates) {
					t.Fatal("unexpected update retry")
				}
				err := tc.updates[updates]
				updates++
				return err
			}
			store.api.add = func(item keychain.Item) error {
				want := expectedQuery("profile-key")
				want.SetLabel("Resona server password")
				want.SetData(data)
				want.SetAccessible(keychain.AccessibleWhenUnlocked)
				if !reflect.DeepEqual(item, want) {
					t.Fatal("new item must remain local with default access control")
				}
				adds++
				return tc.addErr
			}
			if err := store.Set("profile-key", tc.value); !errors.Is(err, tc.wantResult) {
				t.Fatalf("incorrect update result: %v", err)
			}
			if updates != len(tc.updates) || adds != tc.wantAdds {
				t.Fatalf("unexpected write counts: updates=%d adds=%d", updates, adds)
			}
		})
	}
}

func TestDeleteOnlyTargetsSelectedItemAndMissingIsSuccess(t *testing.T) {
	for _, backendError := range []error{nil, keychain.ErrorItemNotFound, errors.New("secret-backend-detail")} {
		store := fakeStore(t)
		store.api.delete = func(query keychain.Item) error {
			if !reflect.DeepEqual(query, expectedQuery("profile-key")) {
				t.Fatal("delete query is not scoped to the intended local item")
			}
			return backendError
		}
		want := error(nil)
		if backendError != nil && !errors.Is(backendError, keychain.ErrorItemNotFound) {
			want = errDelete
		}
		if err := store.Delete("profile-key"); !errors.Is(err, want) {
			t.Fatalf("incorrect deletion result: %v", err)
		}
	}
}

func TestInvalidKeysNeverReachKeychain(t *testing.T) {
	for _, key := range []string{"", "bad\x00key", "\xff"} {
		store := fakeStore(t)
		if _, err := store.Has(key); !errors.Is(err, errInvalidKey) {
			t.Fatal("Has accepted invalid key")
		}
		if _, err := store.Get(key); !errors.Is(err, errInvalidKey) {
			t.Fatal("Get accepted invalid key")
		}
		if err := store.Set(key, "test-only"); !errors.Is(err, errInvalidKey) {
			t.Fatal("Set accepted invalid key")
		}
		if err := store.Delete(key); !errors.Is(err, errInvalidKey) {
			t.Fatal("Delete accepted invalid key")
		}
	}
}
