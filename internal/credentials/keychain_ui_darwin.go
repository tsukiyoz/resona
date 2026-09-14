//go:build darwin && cgo

package credentials

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
*/
import "C"

import (
	"sync"

	keychain "github.com/keybase/go-keychain"
)

// Legacy keychain UI policy is process-wide. Serialize every native operation
// so a silent status query cannot suppress an explicit read or write's prompt.
var nativeKeychainMu sync.Mutex

func queryNativeKeychainSilent(item keychain.Item) (results []keychain.QueryResult, err error) {
	nativeKeychainMu.Lock()
	defer nativeKeychainMu.Unlock()
	var previous C.Boolean
	if C.SecKeychainGetUserInteractionAllowed(&previous) != C.errSecSuccess {
		return nil, errRead
	}
	if C.SecKeychainSetUserInteractionAllowed(0) != C.errSecSuccess {
		return nil, errRead
	}
	defer func() {
		if C.SecKeychainSetUserInteractionAllowed(previous) != C.errSecSuccess {
			results, err = nil, errRead
		}
	}()
	return keychain.QueryItem(item)
}

func queryNativeKeychain(item keychain.Item) ([]keychain.QueryResult, error) {
	nativeKeychainMu.Lock()
	defer nativeKeychainMu.Unlock()
	return keychain.QueryItem(item)
}

func updateNativeKeychain(item, changes keychain.Item) error {
	nativeKeychainMu.Lock()
	defer nativeKeychainMu.Unlock()
	return keychain.UpdateItem(item, changes)
}

func addNativeKeychain(item keychain.Item) error {
	nativeKeychainMu.Lock()
	defer nativeKeychainMu.Unlock()
	return keychain.AddItem(item)
}

func deleteNativeKeychain(item keychain.Item) error {
	nativeKeychainMu.Lock()
	defer nativeKeychainMu.Unlock()
	return keychain.DeleteItem(item)
}

func keychainString(value C.CFStringRef) string {
	var buffer [128]C.char
	if C.CFStringGetCString(value, &buffer[0], C.CFIndex(len(buffer)), C.kCFStringEncodingUTF8) == 0 {
		panic("cannot convert Keychain API constant")
	}
	return C.GoString(&buffer[0])
}

// Use SDK constants rather than depending on the private string spellings.
var authenticationUIKey = keychainString(C.kSecUseAuthenticationUI)
var authenticationUIFail = keychainString(C.kSecUseAuthenticationUIFail)

func disallowAuthenticationUI(item *keychain.Item) {
	item.SetString(authenticationUIKey, authenticationUIFail)
}
