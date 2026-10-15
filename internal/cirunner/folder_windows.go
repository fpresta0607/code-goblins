package cirunner

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"unsafe"

	"golang.org/x/sys/windows"
)

// checkFolder refuses a service folder that anyone but SYSTEM,
// Administrators and the service itself could have made or changed. The
// folder, its settings and the App's key must each be owned by one of them,
// and allow no one else anything, and the folder's own entries must not come
// from its parent. A folder a user made before the install keeps that user
// its owner, who may change its entries whatever they are set to, and could
// then read each job's runner configuration from its work folder.
func checkFolder(dir string) error {
	self, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("cirunner: read the service's identity: %w", err)
	}
	trusted := []string{"S-1-5-18", "S-1-5-32-544", self.User.Sid.String()}
	for _, path := range []string{dir, filepath.Join(dir, "app.json"), filepath.Join(dir, "app-key.pem")} {
		// A file not there is not read, and ReadConfig says it is missing.
		if err := checkHeldBy(path, trusted, path == dir); err != nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return err
		}
	}
	return nil
}

// checkInstalledFolder refuses the install's service folder, dir, unless it
// and its parent are held as the install makes them: no link, owned by
// SYSTEM or Administrators, the parent open to those two alone and dir to
// them and the service, and neither taking entries from its own parent. The
// account the install runs as is never trusted here, as checkFolder trusts
// the service's, since an agent runs as that account: a parent an agent
// made while the install ran, or a folder swapped in since, is refused
// before the App's key goes in.
func checkInstalledFolder(dir string) error {
	administrators := []string{"S-1-5-18", "S-1-5-32-544"}
	for _, folder := range []struct {
		path    string
		trusted []string
	}{
		{filepath.Dir(dir), administrators},
		{dir, append(slices.Clone(administrators), ServiceSID(ServiceName))},
	} {
		name, err := windows.UTF16PtrFromString(folder.path)
		if err != nil {
			return err
		}
		attributes, err := windows.GetFileAttributes(name)
		if err != nil {
			return fmt.Errorf("cirunner: read %s: %w", folder.path, err)
		}
		if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("cirunner: %s is a link, so the install keeps no key in it", folder.path)
		}
		if err := checkHeldBy(folder.path, folder.trusted, true); err != nil {
			return err
		}
	}
	return nil
}

// checkHeldBy refuses path unless its owner is one of trusted, every entry
// of its access list names one of them, and, when isProtected is asked for,
// its list takes nothing from its parent.
func checkHeldBy(path string, trusted []string, isProtected bool) error {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("cirunner: read who holds %s: %w", path, err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("cirunner: read the owner of %s: %w", path, err)
	}
	if !slices.Contains(trusted, owner.String()) {
		return fmt.Errorf("cirunner: %s is owned by %s, not by SYSTEM, Administrators or the service, so the service will not run from it", path, owner)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return fmt.Errorf("cirunner: read the access list of %s: %w", path, err)
	}
	if isProtected && control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("cirunner: %s takes its access from its parent folder, so the service will not run from it", path)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("cirunner: %s has no access list to read: %v", path, err)
	}
	for index := range uint32(dacl.AceCount) {
		var entry *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &entry); err != nil {
			return fmt.Errorf("cirunner: read the access list of %s: %w", path, err)
		}
		if entry.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE && entry.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE {
			return fmt.Errorf("cirunner: %s has an access entry of type %d, which the service does not read", path, entry.Header.AceType)
		}
		sid := (*windows.SID)(unsafe.Pointer(&entry.SidStart))
		if entry.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE && !slices.Contains(trusted, sid.String()) {
			return fmt.Errorf("cirunner: %s lets %s in, so the service will not run from it", path, sid)
		}
	}
	return nil
}
