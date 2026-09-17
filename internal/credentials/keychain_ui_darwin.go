//go:build darwin && cgo

package credentials

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	keychain "github.com/keybase/go-keychain"
)

// Legacy keychain UI policy is process-wide. Serialize every native operation
// so a silent status query cannot suppress an explicit read or write's prompt.
var nativeKeychainMu sync.Mutex

var keychainOperationID atomic.Uint64

// Keep only operation metadata, never account identifiers or secret data.
func traceKeychainOperation(operation string) func(error) {
	id := keychainOperationID.Add(1)
	started := time.Now()
	slog.Info("keychain operation started", "operation", operation, "operation_id", id)
	return func(err error) {
		status := "ok"
		var code int64
		if err != nil {
			status = "failed"
			var nativeError keychain.Error
			if errors.As(err, &nativeError) {
				code = int64(nativeError)
			}
		}
		slog.Info("keychain operation finished", "operation", operation, "operation_id", id,
			"status", status, "os_status", code, "elapsed_ms", time.Since(started).Milliseconds())
	}
}

// Existing items live in the file-based Keychain. Read the selected generic
// password directly, without the SecItem compatibility query path or retries.
func readNativePassword(key string) (_ []byte, err error) {
	nativeKeychainMu.Lock()
	defer nativeKeychainMu.Unlock()
	done := traceKeychainOperation("read")
	defer func() { done(err) }()
	service, account := C.CString(serviceName), C.CString(key)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	var length C.UInt32
	var data unsafe.Pointer
	status := C.SecKeychainFindGenericPassword(0, C.UInt32(len(serviceName)), service,
		C.UInt32(len(key)), account, &length, &data, nil)
	if data != nil {
		defer C.SecKeychainItemFreeContent(nil, data)
	}
	if status != C.errSecSuccess {
		return nil, keychain.Error(status)
	}
	if data == nil || length > 1<<20 {
		return nil, errRead
	}
	return C.GoBytes(data, C.int(length)), nil
}

func queryNativeKeychainSilent(item keychain.Item) (results []keychain.QueryResult, err error) {
	nativeKeychainMu.Lock()
	defer nativeKeychainMu.Unlock()
	done := traceKeychainOperation("has_silent")
	defer func() { done(err) }()
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

func queryNativeKeychain(item keychain.Item) (_ []keychain.QueryResult, err error) {
	nativeKeychainMu.Lock()
	defer nativeKeychainMu.Unlock()
	done := traceKeychainOperation("query")
	defer func() { done(err) }()
	return keychain.QueryItem(item)
}

func updateNativeKeychain(item, changes keychain.Item) (err error) {
	nativeKeychainMu.Lock()
	defer nativeKeychainMu.Unlock()
	done := traceKeychainOperation("update")
	defer func() { done(err) }()
	return keychain.UpdateItem(item, changes)
}

func addNativeKeychain(item keychain.Item) (err error) {
	nativeKeychainMu.Lock()
	defer nativeKeychainMu.Unlock()
	done := traceKeychainOperation("add")
	defer func() { done(err) }()
	return keychain.AddItem(item)
}

func deleteNativeKeychain(item keychain.Item) (err error) {
	nativeKeychainMu.Lock()
	defer nativeKeychainMu.Unlock()
	done := traceKeychainOperation("delete")
	defer func() { done(err) }()
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
var (
	authenticationUIKey  = keychainString(C.kSecUseAuthenticationUI)
	authenticationUIFail = keychainString(C.kSecUseAuthenticationUIFail)
)

func disallowAuthenticationUI(item *keychain.Item) {
	item.SetString(authenticationUIKey, authenticationUIFail)
}
