package cirunner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Every script the install runs is one Windows PowerShell parses without
// error, so none fails first on the Overlord's machine.
func TestInstallScriptsParse(t *testing.T) {
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	const parse = "$errors = $null; [void][System.Management.Automation.Language.Parser]::ParseInput($env:CG_CI_SCRIPT, [ref]$null, [ref]$errors); $errors | ForEach-Object { $_.Message }; exit $errors.Count"
	for name, text := range map[string]string{"check": checkScript, "folder": folderScript, "disk": diskScript, "settings": settingsScript, "vm": vmScript, "checkpoint": checkpointScript, "service": serviceScript, "start": startScript, "machine": machineScript, "remove the service": removeServiceScript, "remove the VM": removeVMScript, "remove the folder": removeFolderScript} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			command := execx.CommandContext(context.Background(), shell, "-NoProfile", "-NonInteractive", "-Command", parse)
			command.Env = append(os.Environ(), "CG_CI_SCRIPT=$ErrorActionPreference = 'Stop'; "+text)

			// Act
			output, err := command.CombinedOutput()

			// Assert
			if err != nil {
				t.Errorf("the %s script does not parse: %v: %s", name, err, strings.TrimSpace(string(output)))
			}
		})
	}
}

// The service's folder, where the App's key goes, and its parent, when the
// install makes one, are made owned by Administrators, with an access list
// that takes nothing from the parent: the folder open to SYSTEM,
// Administrators and the service alone, and its parent to the first two.
func TestTheServicesFolderIsMadeHeldByItsThreeAlone(t *testing.T) {
	// Arrange
	sddls := regexp.MustCompile(`'(O:BA[^']*)'( \+ \$env:CG_CI_SERVICE_SID \+ '\)')?`).FindAllStringSubmatch(folderScript, -1)
	if len(sddls) != 2 {
		t.Fatalf("the folder script names %d access lists; want the parent's and the folder's", len(sddls))
	}
	service := ServiceSID(ServiceName)
	for i, want := range [][]string{
		{"S-1-5-18=0x1f01ff", "S-1-5-32-544=0x1f01ff"},
		{"S-1-5-18=0x1f01ff", "S-1-5-32-544=0x1f01ff", service + "=0x1301bf"},
	} {
		sddl := sddls[i][1]
		if sddls[i][2] != "" {
			sddl += service + ")"
		}

		// Act
		descriptor, err := windows.SecurityDescriptorFromString(sddl)
		if err != nil {
			t.Fatalf("%s: %v", sddl, err)
		}
		owner, _, err := descriptor.Owner()

		// Assert
		if err != nil || owner.String() != "S-1-5-32-544" {
			t.Errorf("%s is owned by %v (%v); want Administrators", sddl, owner, err)
		}
		if got := folderEntries(t, descriptor); !slices.Equal(got, sorted(want)) {
			t.Errorf("%s gives %q; want %q alone", sddl, got, sorted(want))
		}
	}
}

// folderEntries reads every entry of a protected DACL as its SID and access
// mask, sorted, and fails the test on a DACL that is not protected or an
// entry that is not an allowing one its folder's files and folders take,
// object and container inherit, and nothing else.
func folderEntries(t *testing.T, descriptor *windows.SECURITY_DESCRIPTOR) []string {
	t.Helper()
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("the DACL in %s is not protected, so it may inherit entries", descriptor)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("no DACL to read: %v", err)
	}
	var entries []string
	for index := range uint32(dacl.AceCount) {
		var entry *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || entry.Header.AceFlags != windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE {
			t.Errorf("the DACL in %s holds an entry of type %d with flags %#x; want allowing entries its files and folders take", descriptor, entry.Header.AceType, entry.Header.AceFlags)
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&entry.SidStart))
		entries = append(entries, fmt.Sprintf("%s=%#x", sid.String(), uint32(entry.Mask)))
	}
	return sorted(entries)
}

// The install refuses a service folder that is here already, whoever made
// it, and a parent that is a link or is owned by anyone but SYSTEM and
// Administrators, since an owner may change a folder's access at any time.
func TestTheInstallRefusesAFolderAnotherCouldHold(t *testing.T) {
	for name, row := range map[string]struct {
		arrange func(t *testing.T) string
		want    string
	}{
		"a folder here already": {
			arrange: func(t *testing.T) string { return t.TempDir() },
			want:    "is here already",
		},
		"a parent the user owns": {
			arrange: func(t *testing.T) string { return filepath.Join(t.TempDir(), "ci-runner") },
			want:    "is owned by " + thisUser(t),
		},
		"a parent that is a link": {
			arrange: func(t *testing.T) string {
				parent := filepath.Join(t.TempDir(), "Code Goblins")
				target := t.TempDir()
				if output, err := execx.CommandContext(context.Background(), filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/d", "/c", "mklink", "/J", parent, target).CombinedOutput(); err != nil {
					t.Fatalf("mklink /J: %v: %s", err, output)
				}
				return filepath.Join(parent, "ci-runner")
			},
			want: "is a link",
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := row.arrange(t)

			// Act
			_, err := powerShell(context.Background(), execx.OSRunner{}, installStepLimit, "check the service's folder", folderCheckScript, "CG_CI_DIR="+dir)

			// Assert
			if err == nil || !strings.Contains(err.Error(), row.want) {
				t.Errorf("the check = %v; want the folder refused, saying %q", err, row.want)
			}
		})
	}
}

// The install's own making of the folder refuses one that is here already,
// as a folder another made between the check and the making would be.
func TestTheInstallMakesNoFolderThatIsHereAlready(t *testing.T) {
	// Arrange
	dir := t.TempDir()

	// Act
	_, err := powerShell(context.Background(), execx.OSRunner{}, installStepLimit, "make the service's folder", folderScript, "CG_CI_DIR="+dir, "CG_CI_SERVICE_SID="+ServiceSID(ServiceName))

	// Assert
	if err == nil || !strings.Contains(err.Error(), "is here already") {
		t.Errorf("the folder script = %v; want the folder refused", err)
	}
}

// ServiceSID names the account Windows would give any service, as sc.exe
// showsid does, which needs no service to exist.
func TestServiceSIDIsWhatWindowsSays(t *testing.T) {
	for _, name := range []string{"cg-ci-runner", "Code-Goblins-Test", "x"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			sc := filepath.Join(os.Getenv("SystemRoot"), "System32", "sc.exe")

			// Act
			output, err := execx.CommandContext(context.Background(), sc, "showsid", name).Output()

			// Assert
			said := regexp.MustCompile(`S-1-5-80(-\d+){5}`).FindString(string(output))
			if err != nil || said == "" || ServiceSID(name) != said {
				t.Errorf("ServiceSID(%q) = %q; sc.exe showsid says %q (%v)", name, ServiceSID(name), said, err)
			}
		})
	}
}
