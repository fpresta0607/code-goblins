package conpty

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// consoleHostChildVariable makes the test binary report the console host
// its process chose, for a test that chooses differently, and
// consoleHostFolderVariable names the folder that child puts it in.
const (
	consoleHostChildVariable  = "CONPTY_TEST_CONSOLE_HOST_CHILD"
	consoleHostFolderVariable = "CONPTY_TEST_CONSOLE_HOST_FOLDER"
)

// The embedded console host is Microsoft's own: Windows finds each file's
// Authenticode signature valid, and Microsoft Corporation signed it.
func TestTheEmbeddedConsoleHostIsSignedByMicrosoft(t *testing.T) {
	for name, content := range map[string][]byte{"conpty.dll": embeddedConptyDLL, "OpenConsole.exe": embeddedOpenConsole} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			path := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			verified := verifySignature(path)
			names, err := signatureCertificates(path)

			// Assert
			if verified != nil || err != nil {
				t.Fatalf("the signature does not hold: %v, %v", verified, err)
			}
			if !slices.Contains(names, "Microsoft Corporation") {
				t.Fatalf("the signature's certificates are %q, want Microsoft Corporation's among them", names)
			}
		})
	}
}

// verifySignature checks path's Authenticode signature as Windows does
// before it runs a file, without asking whether a certificate was revoked.
func verifySignature(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	file := windows.WinTrustFileInfo{FilePath: name}
	file.Size = uint32(unsafe.Sizeof(file))
	data := windows.WinTrustData{UIChoice: windows.WTD_UI_NONE, RevocationChecks: windows.WTD_REVOKE_NONE, UnionChoice: windows.WTD_CHOICE_FILE, StateAction: windows.WTD_STATEACTION_VERIFY, FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&file)}
	data.Size = uint32(unsafe.Sizeof(data))
	verified := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data)
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	return errors.Join(verified, windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data))
}

// signatureCertificates names each certificate path's embedded signature
// carries, its signer's among them.
func signatureCertificates(path string) ([]string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var store windows.Handle
	if err := windows.CryptQueryObject(windows.CERT_QUERY_OBJECT_FILE, unsafe.Pointer(name), windows.CERT_QUERY_CONTENT_FLAG_PKCS7_SIGNED_EMBED, windows.CERT_QUERY_FORMAT_FLAG_BINARY, 0, nil, nil, nil, &store, nil, nil); err != nil {
		return nil, err
	}
	defer windows.CertCloseStore(store, 0)
	var names []string
	for certificate, err := windows.CertEnumCertificatesInStore(store, nil); err == nil; certificate, err = windows.CertEnumCertificatesInStore(store, certificate) {
		buffer := make([]uint16, 256)
		length := windows.CertGetNameString(certificate, windows.CERT_NAME_SIMPLE_DISPLAY_TYPE, 0, nil, &buffer[0], uint32(len(buffer)))
		names = append(names, windows.UTF16ToString(buffer[:length]))
	}
	return names, nil
}

// A file where the console host goes is used only once its SHA-256 is the
// embedded copy's: one that differs is replaced, and the one used is held,
// so nothing writes, renames or deletes it while this process may start a
// console from it.
func TestTheConsoleHostUsesOnlyAFileHoldingTheEmbeddedCopy(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "conpty.dll")
	if err := os.WriteFile(path, []byte("not the embedded copy"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	held, err := placeVerified(path, embeddedConptyDLL)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { held.Close() })
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, embeddedConptyDLL) {
		t.Fatalf("the file holds %d bytes (%v), want the embedded copy's %d", len(got), err, len(embeddedConptyDLL))
	}
	for change, err := range map[string]error{
		"rewritten": os.WriteFile(path, []byte("replaced"), 0o600),
		"renamed":   os.Rename(path, path+".moved"),
		"deleted":   os.Remove(path),
	} {
		if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Errorf("the held file was %s or failed otherwise: %v", change, err)
		}
	}
}

// The console host's folder lets this Windows user, and no one else, in,
// whoever made it and whatever access it had.
func TestTheConsoleHostFolderIsThisUsersAlone(t *testing.T) {
	// Arrange
	dir := filepath.Join(t.TempDir(), openConsoleVersion)
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	err = userOnlyFolder(dir)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	// Windows writes the SID of a well-known account by its alias, as a
	// hosted runner's administrator is written LA, so the wanted list is
	// written the same way.
	var want []string
	for _, flags := range []string{"P", "PAI"} {
		expected, err := windows.SecurityDescriptorFromString("D:" + flags + "(A;OICI;FA;;;" + user.User.Sid.String() + ")")
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, expected.String())
	}
	if got := descriptor.String(); !slices.Contains(want, got) {
		t.Fatalf("the folder's access is %s, want %s: this user's full access alone, protected from inheritance", got, want[0])
	}
}

// Consoles run on the embedded OpenConsole, written to this user's folder,
// rather than on the system conhost.
func TestConsolesRunOnTheEmbeddedOpenConsole(t *testing.T) {
	// Arrange
	if problem := ConsoleHostProblem(); problem != nil {
		t.Fatal(problem)
	}
	dir, err := consoleHostFolder()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	_, _, server := startChildWithServer(t, Spec{Cols: 80, Rows: 25})

	// Assert
	if got, want := processImage(t, server), filepath.Join(dir, "OpenConsole.exe"); !strings.EqualFold(got, want) {
		t.Fatalf("the console server runs %s, want %s", got, want)
	}
}

// A process whose environment points LOCALAPPDATA and USERPROFILE at a
// test's own folder still runs its consoles from this user's own folder, so
// nothing it loads is left in a folder the test deletes as it ends.
func TestTheConsoleHostIsNeverPlacedWhereTheEnvironmentPointsLocalAppData(t *testing.T) {
	// Arrange
	dir, err := consoleHostFolder()
	if err != nil {
		t.Fatal(err)
	}
	pointed := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestConsoleHostChoiceChild$", "-test.v")
	child.Env = append(os.Environ(), "LOCALAPPDATA="+pointed, "USERPROFILE="+pointed, consoleHostChildVariable+"=1")

	// Act
	out, err := child.CombinedOutput()

	// Assert
	if err != nil {
		t.Fatalf("the child failed: %v\n%s", err, out)
	}
	if want := "server " + filepath.Join(dir, "OpenConsole.exe"); !strings.Contains(strings.ToLower(string(out)), strings.ToLower(want)) {
		t.Fatalf("the child said %s, want its console on %s", out, want)
	}
	if entries, err := os.ReadDir(pointed); err != nil || len(entries) > 0 {
		t.Fatalf("the folder the environment pointed at holds %v (%v), want nothing", entries, err)
	}
}

// When the embedded console host cannot be written, consoles run on the
// system conhost, and the process says why. Each process chooses once, so a
// child chooses with a folder that cannot be made.
func TestAConsoleHostThatCannotBeWrittenLeavesTheSystemConhostAndSaysWhy(t *testing.T) {
	// Arrange
	blocked := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestConsoleHostChoiceChild$", "-test.v")
	child.Env = append(os.Environ(), consoleHostFolderVariable+"="+filepath.Join(blocked, openConsoleVersion), consoleHostChildVariable+"=1")

	// Act
	out, err := child.CombinedOutput()

	// Assert
	if err != nil {
		t.Fatalf("the child failed: %v\n%s", err, out)
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(string(out)), strings.ToLower("server "+filepath.Join(system, "conhost.exe"))) || !strings.Contains(string(out), "problem the embedded OpenConsole "+openConsoleVersion+" cannot be used, so terminals run on the system conhost") {
		t.Fatalf("the child said %s, want its console on the system conhost and why", out)
	}
}

// TestConsoleHostChoiceChild starts a console, with its console host in the
// folder consoleHostFolderVariable names when it names one, and says what its
// server runs and why it is not the embedded OpenConsole.
func TestConsoleHostChoiceChild(t *testing.T) {
	if os.Getenv(consoleHostChildVariable) == "" {
		return
	}
	if dir := os.Getenv(consoleHostFolderVariable); dir != "" {
		consoleHostFolder = func() (string, error) { return dir, nil }
	}
	_, _, server := startChildWithServer(t, Spec{Cols: 80, Rows: 25})
	fmt.Printf("server %s\nproblem %v\n", processImage(t, server), ConsoleHostProblem())
}

// processImage is the program process runs.
func processImage(t *testing.T, process windows.Handle) string {
	t.Helper()
	image := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(image))
	if err := windows.QueryFullProcessImageName(process, 0, &image[0], &size); err != nil {
		t.Fatal(err)
	}
	return windows.UTF16ToString(image[:size])
}
