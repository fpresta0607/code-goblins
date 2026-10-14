package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

// The service's folder is in Windows' own ProgramData, whatever a variable
// a user can set says.
func TestTheServiceFolderIsNotTakenFromTheEnvironment(t *testing.T) {
	// Arrange
	planted := t.TempDir()
	t.Setenv("ProgramData", planted)
	programData, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	dir, err := serviceFolder()

	// Assert
	if want := filepath.Join(programData, "Code Goblins", "ci-runner"); err != nil || dir != want {
		t.Errorf("serviceFolder = %q, %v; want %q, not under %s", dir, err, want, planted)
	}
}

// A service whose settings cannot be read stops with an error of its own,
// and its log says why, before it serves anything.
func TestServiceThatCannotReadItsSettingsStopsAndSaysWhy(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	// The folder is held as the install holds it, so its settings are what
	// the service finds missing.
	self, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + self.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	requests := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 4)

	// Act
	isServiceError, code := handler{dir: dir}.Execute(nil, requests, changes)

	// Assert
	log, err := os.ReadFile(filepath.Join(dir, "ci-runner.log"))
	if !isServiceError || code != 2 || err != nil || !strings.Contains(string(log), "the service cannot start: cirunner: read the service's settings") {
		t.Errorf("Execute = %v, %d with log %q (%v); want service error 2 and why in the log", isServiceError, code, log, err)
	}
	close(changes)
	for change := range changes {
		if change.State == svc.Running {
			t.Error("the service said it runs; want it never to")
		}
	}
}
