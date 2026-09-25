package supervisor

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/coder/websocket"
	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// viewQuery opens task-1's current generation with the board's token.
const viewQuery = "task=task-1&generation=g1&token=instance"

// TestNativeTerminalProgram is not a test but the program a native terminal
// test runs in its terminal: it records each typed line in the file it is
// given, prints its terminal's size for "size", registers as the CFO of the
// state directory it is given for "register", recording the outcome, prints
// more than the host's pipe holds for "spill", recording "spilled" after, and
// exits for "exit N".
func TestNativeTerminalProgram(t *testing.T) {
	args := flag.Args()
	if len(args) != 3 || args[0] != "native-terminal-program" {
		t.Skip("runs only in a native terminal test's terminal")
	}
	record := func(text string) {
		file, err := os.OpenFile(args[1], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Println("record error", err)
			return
		}
		fmt.Fprintln(file, text)
		file.Close()
	}
	fmt.Println("program ready")
	lines := bufio.NewScanner(os.Stdin)
	for lines.Scan() {
		line := lines.Text()
		switch {
		case line == "register":
			described, err := Register(context.Background(), args[2], nil, "claude", "session-1")
			if err != nil {
				record("register error: " + err.Error())
				continue
			}
			record("registered " + described)
		case line == "size":
			var info windows.ConsoleScreenBufferInfo
			if err := windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &info); err != nil {
				fmt.Println("size error", err)
				continue
			}
			fmt.Printf("size %dx%d\n", info.Window.Right-info.Window.Left+1, info.Window.Bottom-info.Window.Top+1)
		case line == "spill":
			for i := 0; i < 2000; i++ {
				fmt.Println(strings.Repeat("s", 100))
			}
			record("spilled")
		case strings.HasPrefix(line, "exit "):
			code, _ := strconv.Atoi(strings.TrimPrefix(line, "exit "))
			os.Exit(code)
		default:
			record(line)
		}
	}
	os.Exit(0)
}

// nativeBoard serves a board whose task-1 is a native task in mode.
func nativeBoard(t *testing.T, mode string) (*HTTP, *httptest.Server) {
	t.Helper()
	store, _ := testStore(t)
	meta, err := state.ReadTaskMeta(store.Home.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.Backend, meta.Mode = "native", mode
	meta.HerdrSession, meta.HerdrWorkspaceID, meta.HerdrTabID, meta.HerdrPaneID = "", "", "", ""
	if err := state.WriteTaskMeta(store.Home.State, meta); err != nil {
		t.Fatal(err)
	}
	h := NewHTTP(&Service{Store: store, Instance: "instance", done: make(chan struct{})}, "", nil)
	server := httptest.NewServer(h)
	h.Host = strings.TrimPrefix(server.URL, "http://")
	t.Cleanup(server.Close)
	return h, server
}

// hostedTerminal is a terminal hosted in this test process as cfo host hosts
// one, running this test binary as TestNativeTerminalProgram.
type hostedTerminal struct {
	stateDir, id string
	// typed is the file where the program records each line typed into it.
	typed string
	ended chan struct{}
}

// hostTask hosts task-1's terminal.
func hostTask(t *testing.T, h *HTTP) hostedTerminal {
	t.Helper()
	return hostTerminal(t, h.Service.Store.Home.State, "task-1")
}

func hostTerminal(t *testing.T, stateDir, id string) hostedTerminal {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	terminal := hostedTerminal{stateDir: stateDir, id: id, typed: filepath.Join(t.TempDir(), "typed.txt"), ended: make(chan struct{})}
	go func() {
		defer close(terminal.ended)
		err := host.Run(stateDir, host.Spec{ID: id, Args: []string{program, "-test.run=^TestNativeTerminalProgram$", "--", "native-terminal-program", terminal.typed, stateDir}, Cols: 80, Rows: 24})
		if err != nil {
			t.Errorf("the host ended with %v", err)
		}
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := host.ReadRecord(stateDir, id); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the host never recorded itself")
		}
	}
	t.Cleanup(func() {
		if record, err := host.ReadRecord(stateDir, id); err == nil {
			if client, err := host.Dial(record); err == nil {
				_ = client.CloseTerminal()
				_ = client.Close()
			}
		}
		select {
		case <-terminal.ended:
		case <-time.After(15 * time.Second):
			t.Error("the terminal's host did not end")
		}
	})
	return terminal
}

// typeLine types line and Enter into the terminal directly, not through a
// view.
func (terminal hostedTerminal) typeLine(t *testing.T, line string) {
	t.Helper()
	record, err := host.ReadRecord(terminal.stateDir, terminal.id)
	if err != nil {
		t.Fatal(err)
	}
	client, err := host.Dial(record)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Input([]byte(line + "\r")); err != nil {
		t.Fatal(err)
	}
}

// exit ends the terminal by typing into it directly, not through a view, and
// returns every line the program recorded before it exited.
func (terminal hostedTerminal) exit(t *testing.T) []string {
	t.Helper()
	terminal.typeLine(t, "exit 0")
	select {
	case <-terminal.ended:
	case <-time.After(15 * time.Second):
		t.Fatal("the terminal did not exit")
	}
	return terminal.lines(t)
}

func (terminal hostedTerminal) lines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(terminal.typed)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return strings.FieldsFunc(string(data), func(r rune) bool { return r == '\n' || r == '\r' })
}

// waitForLines waits until the program recorded want lines.
func (terminal hostedTerminal) waitForLines(t *testing.T, want int) []string {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if lines := terminal.lines(t); len(lines) >= want || time.Now().After(deadline) {
			return lines
		}
	}
}

// taskWorktree is task-1's worktree, where a gate's custody check reads its
// branch.
func taskWorktree(t *testing.T, h *HTTP) string {
	t.Helper()
	meta, err := state.ReadTaskMeta(h.Service.Store.Home.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	return meta.Worktree
}

// nativeView is a test's view of a native terminal: everything the terminal
// showed, how many bytes of output it received, and how the view closed. Once
// acking is set it acknowledges each output message as a browser does.
type nativeView struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	screen strings.Builder
	output int64
	acking atomic.Bool
	closed chan struct{}
	err    error
}

func dialNative(server *httptest.Server, query, origin string) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/terminal/native?"+query, &websocket.DialOptions{HTTPHeader: header})
}

func openNativeView(t *testing.T, server *httptest.Server, query string) *nativeView {
	t.Helper()
	conn, _, err := dialNative(server, query, server.URL)
	if err != nil {
		t.Fatalf("the view did not open: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	// A browser takes a message of any size; the relay's are at most 256 KiB.
	conn.SetReadLimit(1 << 20)
	v := &nativeView{conn: conn, closed: make(chan struct{})}
	go func() {
		defer close(v.closed)
		for {
			kind, data, err := conn.Read(context.Background())
			v.mu.Lock()
			if err != nil {
				v.err = err
				v.mu.Unlock()
				return
			}
			v.screen.Write(data)
			if kind == websocket.MessageBinary {
				v.output += int64(len(data))
			}
			output := v.output
			v.mu.Unlock()
			if kind == websocket.MessageBinary && v.acking.Load() {
				v.acknowledge(output)
			}
		}
	}()
	return v
}

// acknowledge tells the board the view has shown output bytes of output.
func (v *nativeView) acknowledge(output int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = v.conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"type":"ack","bytes":%d}`, output)))
}

// ackAll acknowledges everything received so far and each message after.
func (v *nativeView) ackAll() {
	v.acking.Store(true)
	v.mu.Lock()
	output := v.output
	v.mu.Unlock()
	v.acknowledge(output)
}

func (v *nativeView) shows(text string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return strings.Contains(v.screen.String(), text)
}

func (v *nativeView) waitFor(t *testing.T, text string) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		v.mu.Lock()
		screen := v.screen.String()
		v.mu.Unlock()
		if strings.Contains(screen, text) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never showed %q; it shows %q", text, screen[max(0, len(screen)-300):])
		}
	}
}

func (v *nativeView) send(t *testing.T, kind websocket.MessageType, text string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := v.conn.Write(ctx, kind, []byte(text)); err != nil {
		t.Fatalf("sending %q: %v", text, err)
	}
}

// waitForClose returns how the board closed the view.
func (v *nativeView) waitForClose(t *testing.T) websocket.CloseError {
	t.Helper()
	select {
	case <-v.closed:
	case <-time.After(15 * time.Second):
		t.Fatal("the view never closed")
	}
	var closed websocket.CloseError
	if !errors.As(v.err, &closed) {
		t.Fatalf("the view ended with %v, not a close with a reason", v.err)
	}
	return closed
}

// processesStarted puts this test process in a job of its own and returns a
// count of the processes that have joined the job since: every process this
// one starts from then on.
func processesStarted(t *testing.T) func() uint32 {
	t.Helper()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(job) })
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		t.Fatal(err)
	}
	return func() uint32 {
		// JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, which x/sys does not define.
		var accounting struct {
			TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
			TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
		}
		if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); err != nil {
			t.Fatal(err)
		}
		return accounting.TotalProcesses
	}
}

// Only the board's own page, holding the board's token, opens a terminal.
func TestANativeTerminalRefusesCrossOriginAndTokenlessViews(t *testing.T) {
	_, server := nativeBoard(t, "direct")
	for name, view := range map[string]struct{ origin, query string }{
		"another origin": {"http://evil.example", viewQuery},
		"no origin":      {"", viewQuery},
		"no token":       {server.URL, "task=task-1&generation=g1"},
		"a wrong token":  {server.URL, "task=task-1&generation=g1&token=stale"},
	} {
		t.Run(name, func(t *testing.T) {
			conn, response, err := dialNative(server, view.query, view.origin)
			if err == nil {
				_ = conn.CloseNow()
				t.Fatal("the view opened")
			}
			if response == nil || response.StatusCode != http.StatusForbidden {
				t.Fatalf("the view was refused with %v, want 403", response)
			}
		})
	}
}

// A thousand keys reach the terminal in order and start no process: the view
// was bound to its terminal when it connected, so a key is only relayed.
func TestANativeTerminalCarriesAThousandKeysInOrderAndStartsNoProcess(t *testing.T) {
	h, server := nativeBoard(t, "direct")
	terminal := hostTask(t, h)
	h.terminalTick = time.Hour
	v := openNativeView(t, server, viewQuery)
	v.waitFor(t, "program ready")
	started := processesStarted(t)
	before := started()

	var want []string
	for line := range 100 {
		text := fmt.Sprintf("key%06d", line)
		want = append(want, text)
		for _, key := range text + "\r" {
			v.send(t, websocket.MessageBinary, string(key))
		}
	}
	got := terminal.waitForLines(t, len(want))

	if !reflect.DeepEqual(got, want) {
		t.Errorf("the terminal received %d lines %q, want the %d typed in order", len(got), got, len(want))
	}
	if after := started(); after != before {
		t.Errorf("typing started %d processes, want none", after-before)
	}
}

// A resize from the view reaches the terminal.
func TestANativeTerminalResizesTheTerminal(t *testing.T) {
	h, server := nativeBoard(t, "direct")
	hostTask(t, h)
	v := openNativeView(t, server, viewQuery)
	v.waitFor(t, "program ready")

	v.send(t, websocket.MessageText, `{"type":"resize","cols":100,"rows":30}`)
	v.send(t, websocket.MessageBinary, "size\r")

	v.waitFor(t, "size 100x30")
}

// A resize from any view reaches every other view of the terminal, so a second
// window draws the output at the size the terminal now has, and the view that
// sent it is never told its own size back as if another view had taken it.
func TestANativeTerminalTellsEveryOtherViewItsNewSize(t *testing.T) {
	h, server := nativeBoard(t, "direct")
	hostTask(t, h)
	first := openNativeView(t, server, viewQuery)
	second := openNativeView(t, server, viewQuery)
	first.waitFor(t, "program ready")
	second.waitFor(t, "program ready")

	first.send(t, websocket.MessageText, `{"type":"resize","cols":100,"rows":30}`)

	second.waitFor(t, `{"type":"size","cols":100,"rows":30}`)
	first.send(t, websocket.MessageBinary, "size")
	first.waitFor(t, "size 100x30")
	if first.shows(`"type":"size"`) {
		t.Error("the view that sized the terminal was told its own size")
	}
}

// A view measured while hidden reports a size no terminal can use; the
// terminal keeps its size rather than shrinking to it.
func TestANativeTerminalKeepsItsSizeForAViewTooSmallToUse(t *testing.T) {
	h, server := nativeBoard(t, "direct")
	hostTask(t, h)
	v := openNativeView(t, server, viewQuery)
	v.waitFor(t, "program ready")

	v.send(t, websocket.MessageText, `{"type":"resize","cols":1,"rows":1}`)
	v.send(t, websocket.MessageBinary, "size\r")

	v.waitFor(t, "size 80x24")
	if v.shows(`"type":"size"`) {
		t.Error("the views were told of a size the terminal never took")
	}
}

// The board sends a view no more output than it has acknowledged plus its
// window, and the rest once it catches up, so a slow window never holds an
// unbounded queue.
func TestANativeTerminalHoldsOutputAViewHasNotAcknowledged(t *testing.T) {
	h, server := nativeBoard(t, "direct")
	h.terminalWindow = 2 << 10
	hostTask(t, h)
	v := openNativeView(t, server, viewQuery)
	v.waitFor(t, "program ready")

	v.send(t, websocket.MessageBinary, "spill\r")
	v.send(t, websocket.MessageBinary, "size\r")
	time.Sleep(time.Second)

	if v.shows("size 80x24") {
		t.Fatal("the view received everything without acknowledging any of it")
	}
	v.mu.Lock()
	output := v.output
	v.mu.Unlock()
	if output > int64(h.terminalWindow) {
		t.Fatalf("the view received %d bytes it had not acknowledged, beyond its %d-byte window", output, h.terminalWindow)
	}
	v.ackAll()
	v.waitFor(t, "size 80x24")
}

// A view that falls further behind than the board holds for it is closed with
// a reason that tells the browser to reconnect, and no output is dropped from
// a view that stays open.
func TestANativeViewThatFallsBehindIsToldToReconnect(t *testing.T) {
	h, server := nativeBoard(t, "direct")
	h.terminalWindow, h.terminalBacklog = 1<<10, 16<<10
	hostTask(t, h)
	v := openNativeView(t, server, viewQuery)
	v.waitFor(t, "program ready")

	v.send(t, websocket.MessageBinary, "spill\r")

	closed := v.waitForClose(t)
	if closed.Code != websocket.StatusTryAgainLater || closed.Reason != "The view fell behind the terminal's output." {
		t.Errorf("the view closed with %d %q, want a try-again close for falling behind", closed.Code, closed.Reason)
	}
}

// The terminal's end closes the view with its exit code as the reason.
func TestANativeTerminalClosesWithItsExitCode(t *testing.T) {
	h, server := nativeBoard(t, "direct")
	hostTask(t, h)
	v := openNativeView(t, server, viewQuery)
	v.waitFor(t, "program ready")

	v.send(t, websocket.MessageBinary, "exit 7\r")

	closed := v.waitForClose(t)
	if closed.Code != websocket.StatusNormalClosure || closed.Reason != "The terminal ended with exit code 7." {
		t.Errorf("the view closed with %d %q, want a normal close with exit code 7", closed.Code, closed.Reason)
	}
}

// A view that cannot open closes with the reason, which a browser can read.
func TestANativeViewClosesWithTheReasonItCannotOpen(t *testing.T) {
	for name, view := range map[string]struct {
		backend, query, reason string
	}{
		"a replaced task": {"native", "task=task-1&generation=g0&token=instance", "This task restarted or was replaced. Select its current session."},
		"a Herdr task":    {"herdr", viewQuery, "This task's terminal runs in Herdr, not natively."},
		"no running host": {"native", viewQuery, "No terminal is running for this task."},
	} {
		t.Run(name, func(t *testing.T) {
			h, server := nativeBoard(t, "direct")
			meta, err := state.ReadTaskMeta(h.Service.Store.Home.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			meta.Backend = view.backend
			if view.backend == "herdr" {
				meta.HerdrSession, meta.HerdrWorkspaceID, meta.HerdrTabID, meta.HerdrPaneID = "test", "w1", "tab1", "p1"
			}
			if err := state.WriteTaskMeta(h.Service.Store.Home.State, meta); err != nil {
				t.Fatal(err)
			}

			closed := openNativeView(t, server, view.query).waitForClose(t)

			if closed.Code != websocket.StatusPolicyViolation || closed.Reason != view.reason {
				t.Errorf("the view closed with %d %q, want %q", closed.Code, closed.Reason, view.reason)
			}
		})
	}
}

// A task replaced while its view is open closes the view on the next tick.
func TestANativeViewClosesWhenItsTaskIsReplaced(t *testing.T) {
	h, server := nativeBoard(t, "direct")
	hostTask(t, h)
	h.terminalTick = 10 * time.Millisecond
	v := openNativeView(t, server, viewQuery)
	v.waitFor(t, "program ready")

	meta, err := state.ReadTaskMeta(h.Service.Store.Home.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.SpawnGen = "g2"
	if err := state.WriteTaskMeta(h.Service.Store.Home.State, meta); err != nil {
		t.Fatal(err)
	}

	closed := v.waitForClose(t)
	if closed.Code != websocket.StatusPolicyViolation || closed.Reason != "This task restarted or was replaced. Select its current session." {
		t.Errorf("the view closed with %d %q, want the replaced task's reason", closed.Code, closed.Reason)
	}
}

// Custody is checked when a view opens rather than per key: a view of a task
// whose gate owns it shows the terminal, and a key closes it with the gate's
// reason, with nothing typed.
func TestANativeTerminalRefusesTypingWhileAGateOwnsTheTask(t *testing.T) {
	h, server := nativeBoard(t, "no-mistakes")
	gitFixture(t, taskWorktree(t, h))
	h.Service.Options.Gate = fakeProgress{value: pipeline.Progress{Status: "running"}}
	terminal := hostTask(t, h)
	v := openNativeView(t, server, viewQuery)
	v.waitFor(t, "program ready")

	v.send(t, websocket.MessageBinary, "typed under the gate\r")

	closed := v.waitForClose(t)
	if closed.Code != websocket.StatusPolicyViolation || closed.Reason != "pipeline retains custody" {
		t.Errorf("the view closed with %d %q, want the gate's reason", closed.Code, closed.Reason)
	}
	if typed := terminal.exit(t); len(typed) != 0 {
		t.Errorf("the terminal received %q under the gate, want nothing", typed)
	}
}

// Custody is checked again on every tick, so a gate that takes a task over
// after its view opened refuses the next key.
func TestANativeTerminalNoticesAGateTakingOverOnItsTick(t *testing.T) {
	h, server := nativeBoard(t, "no-mistakes")
	gitFixture(t, taskWorktree(t, h))
	gate := &takingGate{}
	h.Service.Options.Gate = gate
	h.terminalTick = 10 * time.Millisecond
	terminal := hostTask(t, h)
	v := openNativeView(t, server, viewQuery)
	v.waitFor(t, "program ready")
	v.send(t, websocket.MessageBinary, "before\r")
	terminal.waitForLines(t, 1)

	gate.taken.Store(true)
	// The first refusal is kept before the next check starts.
	for deadline := time.Now().Add(10 * time.Second); gate.refused.Load() < 2; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no tick checked custody after the gate took over")
		}
	}
	v.send(t, websocket.MessageBinary, "after\r")

	closed := v.waitForClose(t)
	if closed.Code != websocket.StatusPolicyViolation || closed.Reason != "pipeline owns this task" {
		t.Errorf("the view closed with %d %q, want the gate's reason", closed.Code, closed.Reason)
	}
	if typed := terminal.exit(t); !reflect.DeepEqual(typed, []string{"before"}) {
		t.Errorf("the terminal received %q, want only the line typed before the gate", typed)
	}
}

// A resize is a control action, so a view of a task whose gate owns it cannot
// resize the terminal; the resize is dropped, and the first one after custody
// ends applies.
func TestANativeTerminalHoldsItsSizeWhileAGateOwnsTheTask(t *testing.T) {
	h, server := nativeBoard(t, "no-mistakes")
	gitFixture(t, taskWorktree(t, h))
	gate := &takingGate{}
	gate.taken.Store(true)
	h.Service.Options.Gate = gate
	h.terminalTick = 10 * time.Millisecond
	hostTask(t, h)
	v := openNativeView(t, server, viewQuery)
	v.waitFor(t, "program ready")

	v.send(t, websocket.MessageText, `{"type":"resize","cols":100,"rows":30}`)
	// The relay answers a ping only after its loop has handled the resize, so the pong proves it arrived under custody.
	ping, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := v.conn.Ping(ping); err != nil {
		t.Fatalf("pinging the view: %v", err)
	}
	gate.taken.Store(false)
	// The first allowance is kept before the next check starts.
	for deadline := time.Now().Add(10 * time.Second); gate.allowed.Load() < 2; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no tick checked custody after the gate released the task")
		}
	}
	v.send(t, websocket.MessageBinary, "size\r")
	v.waitFor(t, "size 80x24")

	v.send(t, websocket.MessageText, `{"type":"resize","cols":100,"rows":30}`)
	v.send(t, websocket.MessageBinary, "size\r")
	v.waitFor(t, "size 100x30")
}

// takingGate has no run for the branch until taken is set, and then owns the
// task, counting each custody check it refused and each it allowed.
type takingGate struct {
	taken   atomic.Bool
	refused atomic.Int32
	allowed atomic.Int32
}

func (g *takingGate) Progress(context.Context, string, string) (pipeline.Progress, error) {
	return pipeline.Progress{}, pipeline.ErrNoProgress
}

func (g *takingGate) CanSteer(context.Context, string, string, string) error {
	if g.taken.Load() {
		g.refused.Add(1)
		return errors.New("pipeline owns this task")
	}
	g.allowed.Add(1)
	return nil
}

// A reason too long for a close frame is cut on a character boundary, since
// a close frame refused for its length would reach the view with no reason.
func TestACloseReasonFitsACloseFrame(t *testing.T) {
	for _, reason := range []string{"pipeline retains custody", strings.Repeat("é", 100)} {
		fitted := closeReason(reason)

		if len(fitted) > 123 || !utf8.ValidString(fitted) || (len(reason) <= 123 && fitted != reason) {
			t.Errorf("closeReason(%d bytes) = %d bytes %q, want the reason itself or a valid cut of at most 123", len(reason), len(fitted), fitted)
		}
	}
}
