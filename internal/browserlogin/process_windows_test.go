package browserlogin

import (
	"context"
	"encoding/json"
	"fmt"
	"math/bits"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/cdp"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// startChrome starts a headless Chrome on a fresh profile and ends it with
// the test.
func startChrome(t *testing.T, hosts []string) (Process, string) {
	t.Helper()
	executable, err := Executable()
	if err != nil {
		t.Fatal(err)
	}
	profile := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	process, err := Start(ctx, Launch{Executable: executable, Profile: profile, Hosts: hosts, Headless: true})
	if process.PID != 0 {
		t.Cleanup(func() {
			if err := End(context.Background(), process); err != nil {
				t.Errorf("end chrome %d: %v", process.PID, err)
			}
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	return process, profile
}

// navigate opens url in the browser's first page and returns the error text
// Chrome gave for it, empty when it loaded, and the page's text.
func navigate(t *testing.T, address, url string) (string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	response, err := http.Get(address + "/json/list")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var targets []struct {
		Type   string `json:"type"`
		Socket string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(response.Body).Decode(&targets); err != nil {
		t.Fatal(err)
	}
	socket := ""
	for _, target := range targets {
		if target.Type == "page" {
			socket = target.Socket
			break
		}
	}
	if socket == "" {
		t.Fatal("chrome has no page")
	}
	conn, err := cdp.Dial(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var navigated struct {
		ErrorText string `json:"errorText"`
	}
	if err := conn.Call(ctx, "Page.navigate", map[string]string{"url": url}, &navigated); err != nil {
		t.Fatal(err)
	}
	if navigated.ErrorText != "" {
		return navigated.ErrorText, ""
	}
	for {
		var evaluated struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		if err := conn.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "document.readyState === 'complete' ? document.body.innerText : ''", "returnByValue": true}, &evaluated); err != nil {
			t.Fatal(err)
		}
		if evaluated.Result.Value != "" {
			return "", evaluated.Result.Value
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestStartedChromeAnswersOnItsProfilesPortAndClosesWhenAsked(t *testing.T) {
	process, profile := startChrome(t, []string{"localhost"})

	if !strings.HasPrefix(process.URL, "http://127.0.0.1:") {
		t.Fatalf("DevTools address = %q, want one on 127.0.0.1", process.URL)
	}
	if !Running(process) {
		t.Fatal("the started chrome is not running")
	}
	if err := End(context.Background(), process); err != nil {
		t.Fatal(err)
	}
	if Running(process) {
		t.Fatal("chrome still runs after End")
	}
	// Nothing of the Chrome holds a file of the profile, its crash
	// handler included, so Forget can delete it at once.
	if err := os.RemoveAll(profile); err != nil {
		t.Fatalf("the profile cannot be removed after End: %v", err)
	}
}

// A Chrome on a loaded machine can take over half a minute to answer its
// DevTools: one did on a CI runner on 2026-10-14. It still starts.
func TestAChromeSlowToAnswerItsDevToolsStillStarts(t *testing.T) {
	// Arrange
	executable, err := Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	slowAnswer := func(ctx context.Context, pipe *devToolsPipe) error {
		if err := sleep(ctx, 35*time.Second); err != nil {
			return err
		}
		return pipe.call(ctx, "Browser.getVersion", nil, nil)
	}

	// Act
	process, err := start(ctx, Launch{Executable: executable, Profile: t.TempDir(), Headless: true}, slowAnswer, Process.close)

	// Assert
	if process.PID != 0 {
		t.Cleanup(func() {
			if err := End(context.Background(), process); err != nil {
				t.Errorf("end chrome %d: %v", process.PID, err)
			}
		})
	}
	if err != nil {
		t.Fatalf("a Chrome that answered after 35 s did not start: %v", err)
	}
}

func TestPrivateChromeHasNoTCPListenerAndKeepsTwoClientsIndependent(t *testing.T) {
	process, profile := startChrome(t, []string{"localhost"})
	if _, err := os.Stat(filepath.Join(profile, activePortFile)); !os.IsNotExist(err) {
		t.Fatalf("Chrome published a raw debugger port file: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	// Windows' own table of listening sockets, read in this process: a
	// PowerShell query of it took over the 30 s above to start and answer on
	// a busy CI runner and was ended with no count. A listener of this test's
	// own shows the table is read at all.
	own, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer own.Close()
	listens, err := fleettree.Listeners()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(listens[os.Getpid()], own.Addr().(*net.TCPAddr).Port) {
		t.Fatalf("the listening table holds %v for this test, want its own port %s", listens[os.Getpid()], own.Addr())
	}
	if ports := listens[process.PID]; len(ports) != 0 {
		t.Fatalf("Chrome %d listens on TCP ports %v, want none", process.PID, ports)
	}
	socket, err := browserSocket(ctx, process.URL)
	if err != nil {
		t.Fatal(err)
	}
	first, err := cdp.Dial(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := cdp.Dial(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	finished := make(chan error, 2)
	for _, connection := range []*cdp.Conn{first, second} {
		go func() { finished <- connection.Call(ctx, "Browser.getVersion", nil, nil) }()
	}
	for range 2 {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
	first.Close()
	if err := second.Call(ctx, "Target.getTargets", nil, nil); err != nil {
		t.Fatal("closing one client broke the other: ", err)
	}
}

func TestPrivateChromeRoutesAutoAttachedPageCommandsAndEvents(t *testing.T) {
	process, _ := startChrome(t, []string{"localhost"})
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	socket, err := browserSocket(ctx, process.URL)
	if err != nil {
		t.Fatal(err)
	}
	connection, _, err := websocket.Dial(ctx, socket, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if err := wsjson.Write(ctx, connection, devToolsMessage{ID: 1, Method: "Target.setAutoAttach", Params: json.RawMessage(`{"autoAttach":true,"waitForDebuggerOnStart":false,"flatten":true}`)}); err != nil {
		t.Fatal(err)
	}
	var session string
	var isAttached bool
	for !isAttached || session == "" {
		var message devToolsMessage
		if err := wsjson.Read(ctx, connection, &message); err != nil {
			t.Fatal(err)
		}
		if len(message.Error) != 0 {
			t.Fatal("Chrome refused auto attachment")
		}
		if message.ID == 1 {
			isAttached = true
		}
		if message.Method == "Target.attachedToTarget" {
			var attached struct {
				Session string                `json:"sessionId"`
				Target  struct{ Type string } `json:"targetInfo"`
			}
			if err := json.Unmarshal(message.Params, &attached); err != nil {
				t.Fatal(err)
			}
			if attached.Target.Type == "page" {
				if message.Session != "" {
					t.Fatal("the client's private root session leaked into an event")
				}
				session = attached.Session
			}
		}
	}
	if err := wsjson.Write(ctx, connection, devToolsMessage{ID: 2, Method: "Runtime.evaluate", Session: session, Params: json.RawMessage(`{"expression":"6 * 7","returnByValue":true}`)}); err != nil {
		t.Fatal(err)
	}
	// Chrome may send an event first, such as another target's attachment,
	// so the reply is the message that answers the command's id.
	var response devToolsMessage
	for response.ID != 2 {
		response = devToolsMessage{}
		if err := wsjson.Read(ctx, connection, &response); err != nil {
			t.Fatal(err)
		}
	}
	var result struct{ Result struct{ Value int } }
	if err := json.Unmarshal(response.Result, &result); err != nil || response.ID != 2 || response.Session != session || result.Result.Value != 42 {
		t.Fatal("an auto-attached page did not receive its command and reply")
	}
}

func TestGoneConfirmsAChromeOnlyOnceItsEndedJobHasExited(t *testing.T) {
	// Arrange
	process, _ := startChrome(t, []string{"localhost"})
	isGoneWhileRunning := Gone(process)

	// Act
	err := End(context.Background(), process)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if isGoneWhileRunning || !Gone(process) {
		t.Fatalf("gone while running = %t, after its end = %t, want false then true", isGoneWhileRunning, Gone(process))
	}
}

// A Chrome that closed when asked leaves its job holding nothing when the
// job is ended, and that job is gone. One whose end never ran is not.
func TestGoneReadsAJobByWhetherItWasEndedWhenItHeldNothing(t *testing.T) {
	// Arrange
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ended := Process{PID: 4242, job: &job{handle: handle}}
	never := Process{PID: 4243, job: &job{}}

	// Act
	err = ended.close()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !Gone(ended) || Gone(never) {
		t.Fatalf("gone after an end of nothing = %t, with no end = %t, want true then false", Gone(ended), Gone(never))
	}
}

func TestGoneReadsAnEndedJobMemberStillRunningAsNotGone(t *testing.T) {
	// Arrange
	started, ok := proc.StartTime(os.Getpid())
	if !ok {
		t.Fatal("cannot read this process's start")
	}
	process := Process{PID: os.Getpid(), Started: started, job: &job{members: []proc.Entry{{PID: os.Getpid(), Start: started}}, isEnded: true}}

	// Act
	isGone := Gone(process)

	// Assert
	if isGone {
		t.Fatal("a job member that still runs read as gone")
	}
}

func TestAChromeThatCannotBeAskedToCloseIsEndedWithEverythingItStarted(t *testing.T) {
	process, profile := startChrome(t, []string{"localhost"})
	// Nothing answers at this address, so the browser is never asked to
	// close and only its job can end it.
	unreachable := process
	unreachable.URL = "http://127.0.0.1:1"

	if err := End(context.Background(), unreachable); err != nil {
		t.Fatal(err)
	}

	if Running(process) {
		t.Fatal("chrome still runs after End")
	}
	if err := os.RemoveAll(profile); err != nil {
		t.Fatalf("the profile cannot be removed after End: %v", err)
	}
}

func TestChromeOnHostRulesReachesOnlyTheRecipeHosts(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "the key page") }))
	defer page.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(page.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	process, _ := startChrome(t, []string{"localhost"})

	failed, text := navigate(t, process.URL, "http://localhost:"+port+"/")
	if failed != "" || text != "the key page" {
		t.Fatalf("the recipe's host: error %q, text %q, want the page", failed, text)
	}
	failed, _ = navigate(t, process.URL, "http://elsewhere.localhost:"+port+"/")
	if failed != "net::ERR_NAME_NOT_RESOLVED" {
		t.Fatalf("a host outside the recipe: error %q, want net::ERR_NAME_NOT_RESOLVED", failed)
	}
	failed, text = navigate(t, process.URL, page.URL)
	if failed == "" && text == "the key page" {
		t.Fatal("an IP address outside the recipe reached the server")
	}
}

var (
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	getProcessAffinityMask = kernel32.NewProc("GetProcessAffinityMask")
	setThreadAffinityMask  = kernel32.NewProc("SetThreadAffinityMask")
)

// busyBeside pins process's job to one processor this test may run on and
// keeps a thread of the test busy on it at normal priority until the test
// ends, as the fleet's builds and tests keep the machine's cores busy.
func busyBeside(t *testing.T, process Process) {
	t.Helper()
	var allowed, system uintptr
	if result, _, err := getProcessAffinityMask.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&allowed)), uintptr(unsafe.Pointer(&system))); result == 0 {
		t.Fatal(err)
	}
	processor := uintptr(1) << (bits.Len(uint(allowed)) - 1)
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := proc.QueryJobInformation(process.job.handle, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), unsafe.Sizeof(limits)); err != nil {
		t.Fatal(err)
	}
	limits.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_AFFINITY
	limits.BasicLimitInformation.Affinity = processor
	if err := proc.SetJobInformation(process.job.handle, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), unsafe.Sizeof(limits)); err != nil {
		t.Fatal(err)
	}
	var isStopped atomic.Bool
	pinned, stopped := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(stopped)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if result, _, err := setThreadAffinityMask.Call(uintptr(windows.CurrentThread()), processor); result == 0 {
			pinned <- err
			return
		}
		pinned <- nil
		for !isStopped.Load() {
		}
	}()
	t.Cleanup(func() { isStopped.Store(true); <-stopped })
	if err := <-pinned; err != nil {
		t.Fatal(err)
	}
}

// setSignInCookie signs process in with a cookie its profile keeps for a
// day.
func setSignInCookie(t *testing.T, process Process) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	cookie := map[string]any{"name": "standin-login", "value": "accepted", "domain": "localhost", "path": "/", "expires": time.Now().Add(24 * time.Hour).Unix()}
	if err := connection.Call(ctx, "Storage.setCookies", map[string]any{"cookies": []map[string]any{cookie}}, nil); err != nil {
		t.Fatal(err)
	}
}

// hasSignInCookie reports whether process holds the cookie setSignInCookie
// gave its profile.
func hasSignInCookie(t *testing.T, process Process) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	var result struct {
		Cookies []struct{ Name, Value string } `json:"cookies"`
	}
	if err := connection.Call(ctx, "Storage.getCookies", nil, &result); err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(result.Cookies, func(cookie struct{ Name, Value string }) bool {
		return cookie.Name == "standin-login" && cookie.Value == "accepted"
	})
}

// Chrome saves its cookies on threads it runs at background priority, and
// each of its processes ends only once its background threads have run.
// While other work kept the machine's cores busy at normal priority, a
// browser asked to close waited tens of seconds for them, so it was ended
// before it had written its sign-in and the next browser on the profile was
// signed out, and its end was not confirmed within a minute (issue #896).
// Here its job has one processor, which a thread of the test keeps busy.
func TestAChromeClosedBesideBusyWorkKeepsItsSignInAndIsConfirmedClosed(t *testing.T) {
	// Arrange
	process, profile := startChrome(t, []string{"localhost"})
	setSignInCookie(t, process)
	browser, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(process.PID))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(browser)
	busyBeside(t, process)

	// Act
	began := time.Now()
	err = End(context.Background(), process)

	// Assert
	if err != nil {
		t.Fatalf("End beside busy work after %s: %v", time.Since(began), err)
	}
	// Raised, the browser closed and was torn down in under 5 seconds here;
	// left at background priority, its end took from 53 seconds to past
	// teardownWait.
	if took := time.Since(began); took > closeWait+15*time.Second {
		t.Fatalf("End beside busy work took %s, want %s at most", took, closeWait+15*time.Second)
	}
	// A browser that closed when asked exits 0, and one ended through its
	// job, before it had written what it holds, exits 1.
	var exit uint32
	if err := windows.GetExitCodeProcess(browser, &exit); err != nil || exit != 0 {
		t.Fatalf("the browser exited %d, %v; want 0, closed when asked within %s", exit, err, closeWait)
	}
	executable, err := Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	next, err := Start(ctx, Launch{Executable: executable, Profile: profile, Hosts: []string{"localhost"}, Headless: true})
	if next.PID != 0 {
		t.Cleanup(func() {
			if err := End(context.Background(), next); err != nil {
				t.Errorf("end chrome %d: %v", next.PID, err)
			}
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	if !hasSignInCookie(t, next) {
		t.Fatal("the next browser on the profile lost the sign-in")
	}
}
