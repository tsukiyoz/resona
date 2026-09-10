//go:build darwin && cgo

package credentials

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
*/
import "C"

import keychain "github.com/keybase/go-keychain"

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
