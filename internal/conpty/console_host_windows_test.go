package conpty

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// consoleHostChildVariable makes the test binary report the console host
// its process chose, for a test that chooses differently.
const consoleHostChildVariable = "CONPTY_TEST_CONSOLE_HOST_CHILD"

// The embedded console host is Microsoft's own: Windows finds each file's
// Authenticode signature valid and Microsoft Corporation's.
func TestTheEmbeddedConsoleHostIsSignedByMicrosoft(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	for name, content := range map[string][]byte{"conpty.dll": embeddedConptyDLL, "OpenConsole.exe": embeddedOpenConsole} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	read := `Get-ChildItem -LiteralPath $env:CONPTY_SIGNED_DIR | ForEach-Object { $s = Get-AuthenticodeSignature -LiteralPath $_.FullName; "$($_.Name)|$($s.Status)|$($s.SignerCertificate.Subject)" }`
	command := exec.Command(filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-Command", read)
	command.Env = append(os.Environ(), "CONPTY_SIGNED_DIR="+dir)

	// Act
	out, err := command.Output()

	// Assert
	if err != nil {
		t.Fatalf("read the signatures: %v", err)
	}
	signatures := strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(out)), " ", "_"))
	if len(signatures) != 2 {
		t.Fatalf("signatures = %q, want one for each embedded file", signatures)
	}
	for _, signature := range signatures {
		if parts := strings.SplitN(signature, "|", 3); len(parts) != 3 || parts[1] != "Valid" || !strings.Contains(parts[2], "O=Microsoft_Corporation,") {
			t.Errorf("%s: want a valid signature of Microsoft Corporation's", signature)
		}
	}
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
	want := regexp.MustCompile(`^D:PA?I?\(A;OICI;FA;;;` + regexp.QuoteMeta(user.User.Sid.String()) + `\)$`)
	if got := descriptor.String(); !want.MatchString(got) {
		t.Fatalf("the folder's access is %s, want this user's full access alone, protected from inheritance", got)
	}
}

// Consoles run on the embedded OpenConsole, written to this user's folder,
// rather than on the system conhost.
func TestConsolesRunOnTheEmbeddedOpenConsole(t *testing.T) {
	// Arrange
	if problem := ConsoleHostProblem(); problem != nil {
		t.Fatal(problem)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cache, "cfo", "conpty", openConsoleVersion, "OpenConsole.exe")

	// Act
	_, _, server := startChildWithServer(t, Spec{Cols: 80, Rows: 25})

	// Assert
	if got := processImage(t, server); !strings.EqualFold(got, want) {
		t.Fatalf("the console server runs %s, want %s", got, want)
	}
}

// When the embedded console host cannot be written, consoles run on the
// system conhost, and the process says why. Each process chooses once, so a
// child chooses with a cache folder that cannot be made.
func TestAConsoleHostThatCannotBeWrittenLeavesTheSystemConhostAndSaysWhy(t *testing.T) {
	// Arrange
	blocked := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestConsoleHostChoiceChild$", "-test.v")
	child.Env = append(os.Environ(), "LOCALAPPDATA="+blocked, consoleHostChildVariable+"=1")

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

// TestConsoleHostChoiceChild starts a console and says what its server runs
// and why it is not the embedded OpenConsole.
func TestConsoleHostChoiceChild(t *testing.T) {
	if os.Getenv(consoleHostChildVariable) == "" {
		return
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
