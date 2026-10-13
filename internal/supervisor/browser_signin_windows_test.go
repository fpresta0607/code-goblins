package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/cdp"
	"github.com/fpresta0607/code-goblins/internal/keypages"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestBrowserSignInWindowOnAHiddenDesktop(t *testing.T) {
	if os.Getenv("CFO_SIGNIN_DESKTOP_PROOF") == "child" {
		proveBrowserSignInWindow(t)
		return
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	name, err := windows.UTF16PtrFromString(fmt.Sprintf("cfo-signin-test-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	desktop, _, callErr := user32.NewProc("CreateDesktopW").Call(uintptr(unsafe.Pointer(name)), 0, 0, 0, 0x01ff, 0)
	if desktop == 0 {
		t.Fatal(callErr)
	}
	defer user32.NewProc("CloseDesktop").Call(desktop)
	report := filepath.Join(t.TempDir(), "proof.txt")
	output, err := os.Create(report)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err := windows.SetHandleInformation(windows.Handle(output.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		t.Fatal(err)
	}
	held, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(held)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(held, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_SIGNIN_DESKTOP_PROOF", "child")
	command, err := windows.UTF16PtrFromString(syscall.EscapeArg(os.Args[0]) + " -test.run=^TestBrowserSignInWindowOnAHiddenDesktop$ -test.v")
	if err != nil {
		t.Fatal(err)
	}
	startup := windows.StartupInfo{Desktop: name, Flags: windows.STARTF_USESTDHANDLES, StdOutput: windows.Handle(output.Fd()), StdErr: windows.Handle(output.Fd())}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	var child windows.ProcessInformation
	if err := windows.CreateProcess(nil, command, nil, nil, true, windows.CREATE_SUSPENDED|windows.CREATE_NO_WINDOW, nil, nil, &startup, &child); err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(child.Process)
	defer windows.CloseHandle(child.Thread)
	defer func() { _ = windows.TerminateProcess(child.Process, 1) }()
	if err := windows.AssignProcessToJobObject(held, child.Process); err != nil {
		t.Fatal(err)
	}
	if _, err := windows.ResumeThread(child.Thread); err != nil {
		t.Fatal(err)
	}
	event, err := windows.WaitForSingleObject(child.Process, 120000)
	if err != nil || event != windows.WAIT_OBJECT_0 {
		t.Fatalf("hidden proof did not end within two minutes: %v", err)
	}
	var exit uint32
	if err := windows.GetExitCodeProcess(child.Process, &exit); err != nil {
		t.Fatal(err)
	}
	if exit != 0 {
		text, err := os.ReadFile(report)
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("hidden sign-in proof failed: %s", text)
	}
}

func proveBrowserSignInWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/test/apikeys":
			if _, err := r.Cookie("standin-login"); err != nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			fmt.Fprint(w, "A signed-in stand-in page with no keys")
		case "/signed-in":
			http.SetCookie(w, &http.Cookie{Name: "standin-login", Value: "accepted", MaxAge: 3600, Path: "/", HttpOnly: true})
			http.Redirect(w, r, "/test/apikeys", http.StatusSeeOther)
		default:
			fmt.Fprint(w, `<a href="/signed-in">Sign in to the stand-in</a>`)
		}
	}))
	defer server.Close()
	address := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	store, h := testStore(t)
	broker := newBrowserBroker(h.State, h.Data, Options{KeyPages: keypages.Recipes{Providers: []keypages.Provider{{ID: "standin", Name: "Stand-in", Hosts: []string{"localhost"}, KeyPage: address + "/test/apikeys", SignedInPath: "^/test/apikeys$"}}}, Dispatch: &Dispatch{Memory: func() (Memory, error) { return Memory{Available: 8 << 30, CommitAvailable: 8 << 30}, nil }}}, func(err error) { t.Error(err) })
	broker.live = func(string) error { return nil }
	service := &Service{Store: store, Instance: "test-instance", browsers: broker}
	brokerTask(t, broker, "pd-keys", "throwaway")
	if err := state.WriteTaskCredentials(broker.state, "pd-keys", []string{"standin"}); err != nil {
		t.Fatal(err)
	}
	brokerManifest(t, broker, "throwaway", auth.Service{Name: "standin", Method: auth.MethodEnv, Env: []string{"STANDIN_TEST_SECRET_KEY"}})
	t.Cleanup(func() {
		if err := broker.closeAll(); err != nil {
			t.Error(err)
		}
	})
	if _, err := service.requestBrowserSignIn(BrowserRequest{Action: "sign-in", Task: "pd-keys", Provider: "standin"}); err != nil {
		t.Fatal(err)
	}
	card := store.Snapshot().BrowserSignIns[0]
	handler := NewHTTP(service, credentialBoardHost, nil)
	request := httptest.NewRequest(http.MethodPost, "http://"+credentialBoardHost+"/api/browser-signins/open", strings.NewReader(fmt.Sprintf(`{"id":%q,"generation":%q}`, card.ID, card.Generation)))
	request.RemoteAddr = "127.0.0.1:8000"
	request.Header.Set("Origin", "http://"+credentialBoardHost)
	request.Header.Set("X-CFO-Token", service.Instance)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("local click = %d: %s", response.Code, response.Body.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := service.processBrowserSignIns(ctx); err != nil {
		t.Fatal(err)
	}
	process := broker.processes[card.Login]
	user32 := windows.NewLazySystemDLL("user32.dll")
	desktop, _, _ := user32.NewProc("GetThreadDesktop").Call(uintptr(windows.GetCurrentThreadId()))
	isVisible := false
	callback := syscall.NewCallback(func(window, _ uintptr) uintptr {
		var pid uint32
		user32.NewProc("GetWindowThreadProcessId").Call(window, uintptr(unsafe.Pointer(&pid)))
		visible, _, _ := user32.NewProc("IsWindowVisible").Call(window)
		if int(pid) == process.PID && visible != 0 {
			isVisible = true
		}
		return 1
	})
	for deadline := time.Now().Add(10 * time.Second); !isVisible && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		user32.NewProc("EnumDesktopWindows").Call(desktop, callback, 0)
	}
	if !isVisible {
		t.Fatal("the sign-in browser has no visible window on its hidden desktop")
	}
	arguments, err := proc.Arguments(process.PID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(arguments, "--remote-debugging-pipe") || slices.ContainsFunc(arguments, func(argument string) bool {
		return strings.HasPrefix(argument, "--remote-debugging-port") || strings.HasPrefix(argument, "--host-resolver-rules")
	}) {
		t.Fatalf("the sign-in window's arguments %q, want a private pipe and no host rules", arguments)
	}
	if isSignedIn, err := browserlogin.SignedIn(ctx, process.URL, broker.recipes.Providers[0]); err != nil || isSignedIn {
		t.Fatalf("unsigned page = %t, %v", isSignedIn, err)
	}
	pageList, err := http.Get(process.URL + "/json/list")
	if err != nil {
		t.Fatal(err)
	}
	var pages []struct {
		Socket string `json:"webSocketDebuggerUrl"`
		Type   string `json:"type"`
	}
	err = json.NewDecoder(pageList.Body).Decode(&pages)
	pageList.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range pages {
		if page.Type != "page" {
			continue
		}
		connection, err := cdp.Dial(ctx, page.Socket)
		if err != nil {
			t.Fatal(err)
		}
		err = connection.Call(ctx, "Page.navigate", map[string]string{"url": address + "/signed-in"}, nil)
		connection.Close()
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	for ctx.Err() == nil && store.Snapshot().BrowserSignIns[0].State == "open" {
		time.Sleep(100 * time.Millisecond)
		if err := service.processBrowserSignIns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if card := store.Snapshot().BrowserSignIns[0]; card.State != "signed_in" {
		t.Fatalf("card = %+v, want signed in", card)
	}
	broker.work.Wait()
	if browserlogin.Running(process) {
		t.Fatal("the sign-in window still runs after completion")
	}
	lease, err := broker.attach(ctx, "pd-keys", "standin")
	if err != nil {
		t.Fatal(err)
	}
	// The next browser uses the same profile, so a persistent sign-in
	// reaches the reviewed page with no second window.
	pageList, err = http.Get(lease.URL + "/json/list")
	if err != nil {
		t.Fatal(err)
	}
	err = json.NewDecoder(pageList.Body).Decode(&pages)
	pageList.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	connection, err := cdp.Dial(ctx, pages[0].Socket)
	if err != nil {
		t.Fatal(err)
	}
	err = connection.Call(ctx, "Page.navigate", map[string]string{"url": address + "/test/apikeys"}, nil)
	connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	isSignedIn := false
	for ctx.Err() == nil && !isSignedIn {
		isSignedIn, err = browserlogin.SignedIn(ctx, lease.URL, broker.recipes.Providers[0])
		if err != nil {
			t.Fatal(err)
		}
		if !isSignedIn {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !isSignedIn {
		t.Fatal("the headless browser lost the sign-in")
	}
}
