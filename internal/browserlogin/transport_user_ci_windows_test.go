package browserlogin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/cdp"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

func TestDevToolsRefusesASecondWindowsUserOnAnIsolatedCIRunner(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("RUNNER_OS") != "Windows" {
		t.Skip("account creation is allowed only on an isolated GitHub-hosted Windows runner")
	}
	process, _ := startChrome(t, []string{"localhost"})
	writeAndReadCookie(t, process)

	username := windows.StringToUTF16Ptr("cg-cdp-" + rand.Text()[:8])
	password := windows.StringToUTF16Ptr("Aa1!" + rand.Text())
	type passwordHash struct {
		Length uint32
		Hash   *byte
	}
	type persistedPassword struct {
		PresentFields                                 uint32
		PasswordLastSet, BadPasswordTime, LockoutTime windows.Filetime
		BadPasswordCount, PasswordHistoryLength       uint32
		PasswordHistory                               *passwordHash
	}
	validation := struct {
		Persisted                persistedPassword
		Password, Username       *uint16
		Hash                     passwordHash
		MustChange, ClearLockout byte
	}{Password: password, Username: username}
	var validated *struct {
		Changed persistedPassword
		Status  uint32
	}
	network := windows.NewLazySystemDLL("netapi32.dll")
	status, _, _ := network.NewProc("NetValidatePasswordPolicy").Call(0, 0, 3, uintptr(unsafe.Pointer(&validation)), uintptr(unsafe.Pointer(&validated)))
	if status != 0 {
		t.Fatalf("read the isolated runner's password policy: status %d", status)
	}
	validationStatus := validated.Status
	network.NewProc("NetValidatePasswordPolicyFree").Call(uintptr(unsafe.Pointer(&validated)))
	if validationStatus != 0 {
		t.Fatalf("the isolated runner's password policy rejected its independent fixture credential: status %d", validationStatus)
	}
	account := struct {
		Name, Password         *uint16
		PasswordAge, Privilege uint32
		Home, Comment          *uint16
		Flags                  uint32
		Script                 *uint16
	}{Name: username, Password: password, Privilege: 1, Flags: 0x1 | 0x200 | 0x10000}
	status, _, _ = network.NewProc("NetUserAdd").Call(0, 1, uintptr(unsafe.Pointer(&account)), 0)
	if status != 0 {
		t.Fatalf("create the isolated runner's temporary non-admin account: status %d", status)
	}
	t.Cleanup(func() {
		status, _, _ := network.NewProc("NetUserDel").Call(0, uintptr(unsafe.Pointer(username)))
		if status != 0 {
			t.Errorf("remove the temporary account: status %d", status)
		}
		var data *byte
		if err := windows.NetUserGetInfo(nil, username, 0, &data); err != syscall.Errno(2221) {
			if data != nil {
				windows.NetApiBufferFree(data)
			}
			t.Errorf("verify temporary account removal: %v", err)
		}
	})
	member := struct{ Name *uint16 }{username}
	status, _, _ = network.NewProc("NetLocalGroupAddMembers").Call(0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Users"))), 3, uintptr(unsafe.Pointer(&member)), 1)
	if status != 0 && status != 1378 {
		t.Fatalf("give the temporary account normal user rights: status %d", status)
	}

	// The password stays in the Windows API call. The child receives only
	// the test endpoint and returns a status, never browser content.
	probe := devToolsDenialProbe(process.URL)
	probeFolder := t.TempDir()
	other, _, _, err := windows.LookupSID("", windows.UTF16PtrToString(username))
	if err != nil {
		t.Fatal(err)
	}
	mine, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + mine.User.Sid.String() + ")(A;OICI;GRGX;;;" + other.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(probeFolder, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	probePath := filepath.Join(probeFolder, "probe.ps1")
	if err := os.WriteFile(probePath, []byte(probe), 0o600); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	command := windows.StringToUTF16Ptr(syscall.EscapeArg(program) + " -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File " + syscall.EscapeArg(probePath))
	startup := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{})), Flags: windows.STARTF_USESHOWWINDOW, ShowWindow: windows.SW_HIDE}
	var child windows.ProcessInformation
	created, _, createErr := windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateProcessWithLogonW").Call(
		uintptr(unsafe.Pointer(username)), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("."))), uintptr(unsafe.Pointer(password)), 0,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(program))), uintptr(unsafe.Pointer(command)), windows.CREATE_NO_WINDOW,
		0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(os.Getenv("SystemRoot")))), uintptr(unsafe.Pointer(&startup)), uintptr(unsafe.Pointer(&child)))
	if created == 0 {
		t.Fatalf("start the second-user probe: %v", createErr)
	}
	windows.CloseHandle(child.Thread)
	defer windows.CloseHandle(child.Process)
	defer func() {
		windows.TerminateProcess(child.Process, 1)
		if event, err := windows.WaitForSingleObject(child.Process, 10000); err != nil || event != windows.WAIT_OBJECT_0 {
			t.Errorf("the owned second-user probe did not end: %v", err)
		}
	}()
	var token windows.Token
	if err := windows.OpenProcessToken(child.Process, windows.TOKEN_QUERY, &token); err != nil {
		t.Fatal(err)
	}
	user, err := token.GetTokenUser()
	groups, groupErr := token.GetTokenGroups()
	token.Close()
	if err != nil {
		t.Fatal(err)
	}
	if user.User.Sid.Equals(mine.User.Sid) {
		t.Fatal("the probe did not run as a different Windows user")
	}
	if groupErr != nil {
		t.Fatal(groupErr)
	}
	for _, group := range groups.AllGroups() {
		if group.Sid.String() == "S-1-5-32-544" {
			t.Fatal("the probe's temporary account has administrator membership")
		}
	}
	if event, err := windows.WaitForSingleObject(child.Process, 30000); err != nil || event != windows.WAIT_OBJECT_0 {
		t.Fatalf("the second-user probe did not finish: %v", err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(child.Process, &code); err != nil || code != 0 {
		t.Fatalf("the second user could reach browser metadata or CDP, or its proof failed: status %d, %v", code, err)
	}
}

// writeAndReadCookie writes a cookie of a random value over process's
// DevTools as this test's own Windows user and reads it back, so a refusal
// of a second user is not a refusal of everyone. A fresh profile's Chrome
// took up to 13.4 s to answer its first Storage.setCookies alone on a hosted
// runner, 2 of 590 timed past 10 s, the bound this had when it failed by
// chance (#1038), so it has the minute a Chrome's DevTools have to answer.
func writeAndReadCookie(t *testing.T, process Process) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), devToolsWait)
	defer cancel()
	socket, err := browserSocket(ctx, process.URL)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := cdp.Dial(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	canary := hex.EncodeToString(random[:])
	if err := connection.Call(ctx, "Storage.setCookies", map[string]any{"cookies": []map[string]string{{"name": "ci_only", "value": canary, "domain": "localhost", "path": "/"}}}, nil); err != nil {
		t.Fatalf("the authorized caller could not write its synthetic cookie: %v", err)
	}
	var result struct {
		Cookies []struct{ Name, Value string } `json:"cookies"`
	}
	if err := connection.Call(ctx, "Storage.getCookies", nil, &result); err != nil || len(result.Cookies) != 1 || result.Cookies[0].Value != canary {
		t.Fatal("the authorized caller could not read its synthetic cookie")
	}
}

var (
	ntdll            = windows.NewLazySystemDLL("ntdll.dll")
	ntSuspendProcess = ntdll.NewProc("NtSuspendProcess")
	ntResumeProcess  = ntdll.NewProc("NtResumeProcess")
)

// Chrome's wait for its first cookie is inside its cookie store, which its
// network service holds, so holding that service past the slowest first
// Storage.setCookies timed on a hosted runner, 13.4 s, gives the cookie
// check that wait on any machine.
func TestAChromeSlowToAnswerItsFirstCookieStillAnswersItsOwner(t *testing.T) {
	// Arrange
	const hold = 14 * time.Second
	process, _ := startChrome(t, []string{"localhost"})
	// Chrome answers its DevTools before it has started its network service.
	network := -1
	for deadline := time.Now().Add(devToolsWait); network == -1 && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		ids, _, err := proc.JobProcessIDs(syscall.Handle(process.job.handle))
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if arguments, err := proc.Arguments(int(id)); err == nil && slices.Contains(arguments, "--utility-sub-type=network.mojom.NetworkService") {
				network = int(id)
			}
		}
	}
	if network == -1 {
		t.Fatalf("chrome started no network service within %s", devToolsWait)
	}
	service, err := windows.OpenProcess(windows.PROCESS_SUSPEND_RESUME, false, uint32(network))
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ := ntSuspendProcess.Call(uintptr(service)); status != 0 {
		windows.CloseHandle(service)
		t.Fatalf("hold chrome's network service %d: status %#x", network, status)
	}
	held := time.Now()
	resumed := make(chan uintptr, 1)
	time.AfterFunc(hold, func() {
		status, _, _ := ntResumeProcess.Call(uintptr(service))
		windows.CloseHandle(service)
		resumed <- status
	})

	// Act
	writeAndReadCookie(t, process)

	// Assert
	if waited := time.Since(held); waited < hold {
		t.Fatalf("the cookie check finished %s into the network service's %s hold, so the hold never reached the cookie store", waited, hold)
	}
	if status := <-resumed; status != 0 {
		t.Fatalf("resume chrome's network service %d: status %#x", network, status)
	}
}

func devToolsDenialProbe(address string) string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
foreach ($path in '/json/version', '/json/list', '/devtools/browser') {
  $request = [Net.HttpWebRequest]::Create('%s' + $path)
  $request.Proxy = $null
  $request.Timeout = 5000
  if ($path -eq '/devtools/browser') {
    $request.Connection = 'Upgrade'
    $request.Headers.Add('Upgrade', 'websocket')
    $request.Headers.Add('Sec-WebSocket-Version', '13')
    $request.Headers.Add('Sec-WebSocket-Key', 'dGhlIHNhbXBsZSBub25jZQ==')
  }
  try {
    $response = $request.GetResponse()
    $response.Close()
    exit 2
  } catch [Net.WebException] {
    if ($null -eq $_.Exception.Response) { exit 3 }
    $status = [int]$_.Exception.Response.StatusCode
    $_.Exception.Response.Close()
    if ($status -ne 403) { exit 4 }
  }
}
exit 0`, address)
}
