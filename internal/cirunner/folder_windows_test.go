package cirunner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// protectFolder gives dir the access list the install gives the service's
// folder, as dir's owner may: SYSTEM, Administrators and the test's own
// user, which stands in for the service, and nothing from its parent.
func protectFolder(t *testing.T, dir string) {
	t.Helper()
	setAccess(t, dir, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;"+thisUser(t)+")")
}

// ownByThisUser makes the test's own user path's owner, as a folder an agent
// made is owned. A folder's maker is its owner only when it runs
// unelevated: an elevated administrator's new folders, as GitHub's runner
// makes them, are owned by Administrators.
func ownByThisUser(t *testing.T, path string) {
	t.Helper()
	user, err := windows.StringToSid(thisUser(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, user, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func setAccess(t *testing.T, path, sddl string) {
	t.Helper()
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	information := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
	if strings.HasPrefix(sddl, "D:P") {
		information |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, information, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

// The service refuses a folder anyone but SYSTEM, Administrators and the
// service could have made or changed, since that one could read each job's
// runner configuration from it.
func TestNewServiceRefusesAFolderOthersCouldChange(t *testing.T) {
	for name, row := range map[string]struct {
		arrange func(t *testing.T, dir string)
		want    string
	}{
		"a folder that takes its access from its parent": {
			arrange: func(*testing.T, string) {},
			want:    "takes its access from its parent folder",
		},
		"a folder that lets Users in": {
			arrange: func(t *testing.T, dir string) {
				setAccess(t, dir, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;"+thisUser(t)+")(A;OICI;FR;;;BU)")
			},
			want: "lets S-1-5-32-545 in",
		},
		"a key that lets Everyone in": {
			arrange: func(t *testing.T, dir string) {
				protectFolder(t, dir)
				setAccess(t, filepath.Join(dir, "app-key.pem"), "D:(A;;FR;;;WD)")
			},
			want: "lets S-1-1-0 in",
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir, _ := serviceFolder(t, goodSettings, "pkcs1")
			row.arrange(t, dir)

			// Act
			_, _, err := NewService(dir, func(string) {})

			// Assert
			if err == nil || !strings.Contains(err.Error(), row.want) {
				t.Errorf("NewService = %v; want it refused, saying %q", err, row.want)
			}
		})
	}
}

// A folder owned by anyone but the trusted is refused, whatever its access
// list says, since its owner may change that list.
func TestAFolderOwnedByAnotherIsRefused(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	protectFolder(t, dir)
	ownByThisUser(t, dir)

	// Act
	err := checkHeldBy(dir, []string{"S-1-5-18", "S-1-5-32-544"}, true)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "is owned by "+thisUser(t)) {
		t.Errorf("checkHeldBy = %v; want the folder refused for its owner", err)
	}
}

// The work folder is made afresh, so nothing left in it before the service
// started is in it once it has.
func TestNewServiceMakesItsWorkFolderAfresh(t *testing.T) {
	// Arrange
	dir, _ := serviceFolder(t, goodSettings, "pkcs1")
	protectFolder(t, dir)
	left := filepath.Join(dir, "work", "left-before.iso")
	if err := os.MkdirAll(filepath.Dir(left), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(left, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	_, _, err := NewService(dir, func(string) {})

	// Assert
	if _, statErr := os.Stat(left); err != nil || !os.IsNotExist(statErr) {
		t.Errorf("NewService = %v, and what was left in work: %v; want it gone", err, statErr)
	}
}
