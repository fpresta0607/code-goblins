package browserlogin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/cdp"
)

func TestDevToolsRefusesASecondWindowsUserOnAnIsolatedCIRunner(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("RUNNER_OS") != "Windows" {
		t.Skip("account creation is allowed only on an isolated GitHub-hosted Windows runner")
	}
	process, _ := startChrome(t, []string{"localhost"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
		t.Fatal("the authorized caller could not write its synthetic cookie")
	}
	var result struct {
		Cookies []struct{ Name, Value string } `json:"cookies"`
	}
	if err := connection.Call(ctx, "Storage.getCookies", nil, &result); err != nil || len(result.Cookies) != 1 || result.Cookies[0].Value != canary {
		t.Fatal("the authorized caller could not read its synthetic cookie")
	}

	username := windows.StringToUTF16Ptr("cg-cdp-" + canary[:8])
	password := windows.StringToUTF16Ptr("Aa1!" + canary)
	account := struct {
		Name, Password         *uint16
		PasswordAge, Privilege uint32
		Home, Comment          *uint16
		Flags                  uint32
		Script                 *uint16
	}{Name: username, Password: password, Privilege: 1, Flags: 0x1 | 0x200 | 0x10000}
	network := windows.NewLazySystemDLL("netapi32.dll")
	status, _, _ := network.NewProc("NetUserAdd").Call(0, 1, uintptr(unsafe.Pointer(&account)), 0)
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
