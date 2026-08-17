//go:build windows

package auth

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

var (
	advapi32           = syscall.NewLazyDLL("advapi32.dll")
	procCredReadW      = advapi32.NewProc("CredReadW")
	procCredWriteW     = advapi32.NewProc("CredWriteW")
	procCredFree       = advapi32.NewProc("CredFree")
	procCredEnumerateW = advapi32.NewProc("CredEnumerateW")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
	errorNotFound           = syscall.Errno(1168)
)

// credentialW mirrors CREDENTIALW from wincred.h. The field order and types
// must match exactly; Go's own alignment rules reproduce the C padding on
// amd64 because every pointer field is naturally 8-aligned.
type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// credentialManagerStore keeps secrets in Windows Credential Manager, where
// they are encrypted at rest under the logged-in user rather than sitting in
// a readable file.
type credentialManagerStore struct{}

func openCredentialManager() (Store, error) {
	// Prove the vault answers before claiming it: a read of a name that does
	// not exist must come back as "not found", not as a load failure.
	store := credentialManagerStore{}
	if _, _, err := store.Get("CFO_CREDENTIAL_STORE_PROBE"); err != nil {
		return nil, err
	}
	return store, nil
}

func (credentialManagerStore) Describe() string {
	return "Windows Credential Manager"
}

func (credentialManagerStore) Get(key string) (string, bool, error) {
	if !ValidEnvName(key) {
		return "", false, fmt.Errorf("auth: invalid credential key %q", key)
	}
	target, err := syscall.UTF16PtrFromString(credentialTarget + key)
	if err != nil {
		return "", false, err
	}
	var credential *credentialW
	ret, _, callErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(credTypeGeneric),
		0,
		uintptr(unsafe.Pointer(&credential)),
	)
	if ret == 0 {
		if errors.Is(callErr, errorNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("auth: CredRead %s: %w", key, callErr)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(credential)))
	if credential.CredentialBlobSize == 0 || credential.CredentialBlob == nil {
		return "", true, nil
	}
	blob := unsafe.Slice(credential.CredentialBlob, credential.CredentialBlobSize)
	// The blob is written as UTF-8 bytes, so it is read back the same way.
	return string(blob), true, nil
}

func (credentialManagerStore) Set(key, value string) error {
	if !ValidEnvName(key) {
		return fmt.Errorf("auth: invalid credential key %q", key)
	}
	target, err := syscall.UTF16PtrFromString(credentialTarget + key)
	if err != nil {
		return err
	}
	user, err := syscall.UTF16PtrFromString("cfo")
	if err != nil {
		return err
	}
	blob := []byte(value)
	credential := credentialW{
		Type:       credTypeGeneric,
		TargetName: target,
		Persist:    credPersistLocalMachine,
		UserName:   user,
	}
	if len(blob) > 0 {
		credential.CredentialBlob = &blob[0]
		credential.CredentialBlobSize = uint32(len(blob))
	}
	ret, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&credential)), 0)
	// blob must outlive the call; keeping it addressable here stops the
	// collector from moving or reclaiming it while the syscall reads it.
	runtime.KeepAlive(blob)
	if ret == 0 {
		return fmt.Errorf("auth: CredWrite %s: %w", key, callErr)
	}
	return nil
}

func (credentialManagerStore) Keys() ([]string, error) {
	filter, err := syscall.UTF16PtrFromString(credentialTarget + "*")
	if err != nil {
		return nil, err
	}
	var count uint32
	var credentials **credentialW
	ret, _, callErr := procCredEnumerateW.Call(
		uintptr(unsafe.Pointer(filter)),
		0,
		uintptr(unsafe.Pointer(&count)),
		uintptr(unsafe.Pointer(&credentials)),
	)
	if ret == 0 {
		if errors.Is(callErr, errorNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("auth: CredEnumerate: %w", callErr)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(credentials)))
	var keys []string
	for _, credential := range unsafe.Slice(credentials, count) {
		if credential == nil || credential.TargetName == nil {
			continue
		}
		name := syscall.UTF16ToString(unsafe.Slice(credential.TargetName, targetNameLen(credential.TargetName)))
		if key, found := strings.CutPrefix(name, credentialTarget); found && ValidEnvName(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// targetNameLen measures a NUL-terminated UTF-16 string the API returns
// without a length. The bound is the longest target name Credential Manager
// accepts, so a corrupt pointer cannot walk memory indefinitely.
func targetNameLen(pointer *uint16) int {
	const maxTargetName = 32768
	for length := 0; length < maxTargetName; length++ {
		if *(*uint16)(unsafe.Add(unsafe.Pointer(pointer), uintptr(length)*2)) == 0 {
			return length
		}
	}
	return maxTargetName
}

// restrictToOwner strips inherited access from a secret file and grants only
// the current user, which is the Windows equivalent of chmod 0600. Go's
// os.WriteFile permission bits do not produce an ACL on their own.
func restrictToOwner(path string) error {
	user := os.Getenv("USERNAME")
	if user == "" {
		return errors.New("auth: cannot restrict credential file: USERNAME is unset")
	}
	if domain := os.Getenv("USERDOMAIN"); domain != "" {
		user = domain + `\` + user
	}
	out, err := exec.Command("icacls", path, "/inheritance:r", "/grant:r", user+":(F)").CombinedOutput()
	if err != nil {
		return fmt.Errorf("auth: restrict %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}
