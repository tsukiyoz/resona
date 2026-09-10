package credentials

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

const serviceName = "io.github.tsukiyoz.resona.server-password"
const maxCredentialBytes = 2560

type passwordPayload struct {
	Version  int     `json:"version"`
	Password *string `json:"password"`
}

type credentialAPI struct {
	read   func(string) ([]byte, error)
	write  func(string, []byte) error
	delete func(string) error
}

type Store struct{ api credentialAPI }

func New() *Store {
	return &Store{api: credentialAPI{read: nativeRead, write: nativeWrite, delete: nativeDelete}}
}

func credentialTarget(key string) (string, error) {
	if key == "" || !utf8.ValidString(key) || strings.ContainsRune(key, 0) || len(key) > 256 {
		return "", errInvalidKey
	}
	return serviceName + "/" + key, nil
}

func (s *Store) Has(key string) (bool, error) {
	target, err := credentialTarget(key)
	if err != nil {
		return false, err
	}
	data, err := s.api.read(target)
	clear(data)
	if errors.Is(err, windows.ERROR_NOT_FOUND) {
		return false, nil
	}
	if err != nil {
		return false, errRead
	}
	return true, nil
}

func (s *Store) Get(key string) (string, error) {
	target, err := credentialTarget(key)
	if err != nil {
		return "", err
	}
	data, err := s.api.read(target)
	defer clear(data)
	if errors.Is(err, windows.ERROR_NOT_FOUND) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", errRead
	}
	var p passwordPayload
	if err = json.Unmarshal(data, &p); err != nil || p.Version != 1 || p.Password == nil {
		return "", errRead
	}
	return *p.Password, nil
}

func (s *Store) Set(key, value string) error {
	target, err := credentialTarget(key)
	if err != nil {
		return err
	}
	if !utf8.ValidString(value) {
		return errWrite
	}
	data, err := json.Marshal(passwordPayload{Version: 1, Password: &value})
	defer clear(data)
	if err != nil || len(data) > maxCredentialBytes {
		return errWrite
	}
	if err = s.api.write(target, data); err != nil {
		return errWrite
	}
	return nil
}

func (s *Store) Delete(key string) error {
	target, err := credentialTarget(key)
	if err != nil {
		return err
	}
	if err = s.api.delete(target); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
		return errDelete
	}
	return nil
}

// CREDENTIALW uses pointer-sized members and Windows struct alignment.
type nativeCredential struct {
	Flags, Type             uint32
	TargetName, Comment     *uint16
	LastWritten             windows.Filetime
	BlobSize                uint32
	Blob                    *byte
	Persist, AttributeCount uint32
	Attributes              uintptr
	TargetAlias, UserName   *uint16
}

var credentialDLL = windows.NewLazySystemDLL("advapi32.dll")
var credRead = credentialDLL.NewProc("CredReadW")
var credWrite = credentialDLL.NewProc("CredWriteW")
var credDelete = credentialDLL.NewProc("CredDeleteW")
var credFree = credentialDLL.NewProc("CredFree")

func nativeRead(target string) ([]byte, error) {
	name, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return nil, err
	}
	var c *nativeCredential
	if ok, _, err := credRead.Call(uintptr(unsafe.Pointer(name)), 1, 0, uintptr(unsafe.Pointer(&c))); ok == 0 {
		return nil, err
	}
	defer credFree.Call(uintptr(unsafe.Pointer(c)))
	if c == nil || c.Type != 1 || c.BlobSize > maxCredentialBytes || (c.BlobSize > 0 && c.Blob == nil) {
		return nil, errRead
	}
	blob := unsafe.Slice(c.Blob, int(c.BlobSize))
	data := append([]byte(nil), blob...)
	clear(blob)
	return data, nil
}

func nativeWrite(target string, data []byte) error {
	name, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maxCredentialBytes {
		return errWrite
	}
	// Generic credentials persist only for this user on this machine.
	c := nativeCredential{Type: 1, TargetName: name, BlobSize: uint32(len(data)), Blob: &data[0], Persist: 2}
	if ok, _, err := credWrite.Call(uintptr(unsafe.Pointer(&c)), 0); ok == 0 {
		return err
	}
	return nil
}

func nativeDelete(target string) error {
	name, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	if ok, _, err := credDelete.Call(uintptr(unsafe.Pointer(name)), 1, 0); ok == 0 {
		return err
	}
	return nil
}
