package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// boardRecord says where the running supervisor serves its board. cfo serve
// writes it once it listens and removes it when it exits, because
// .watch.lock alone cannot tell the supervisor from the legacy watcher, and
// the launcher needs the address a supervisor actually serves.
type boardRecord struct {
	PID int    `json:"pid"`
	URL string `json:"url"`
}

func boardRecordPath(stateDir string) string {
	return filepath.Join(stateDir, "board.json")
}

func serveLogPath(stateDir string) string {
	return filepath.Join(stateDir, "serve.log")
}

func writeBoardRecord(stateDir string, record boardRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(boardRecordPath(stateDir), data)
}

// readBoardRecord reads the record and refuses one whose URL is not a plain
// loopback board address, since the launcher fetches it and may open it in
// the browser, or that names no process.
func readBoardRecord(stateDir string) (boardRecord, error) {
	var record boardRecord
	data, err := fsx.ReadFile(boardRecordPath(stateDir))
	if err != nil {
		return boardRecord{}, err
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return boardRecord{}, err
	}
	if record.PID <= 0 {
		return boardRecord{}, fmt.Errorf("board record names pid %d, not a process", record.PID)
	}
	parsed, err := url.Parse(record.URL)
	if err != nil || parsed.Scheme != "http" || parsed.Opaque != "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return boardRecord{}, fmt.Errorf("board record names %q, not a board address", record.URL)
	}
	if ip := net.ParseIP(parsed.Hostname()); ip == nil || !ip.IsLoopback() || parsed.Port() == "" {
		return boardRecord{}, fmt.Errorf("board record names %q, not a loopback address", record.URL)
	}
	return record, nil
}

// removeBoardRecord removes the record only while it still names pid, so a
// supervisor never removes its successor's.
func removeBoardRecord(stateDir string, pid int) {
	if record, err := readBoardRecord(stateDir); err == nil && record.PID == pid {
		_ = os.Remove(boardRecordPath(stateDir))
	}
}

// launcherSnapshot is the part of the board's snapshot the status line reads.
type launcherSnapshot struct {
	Registration string `json:"registration"`
	Tasks        []struct {
		Phase string `json:"phase"`
	} `json:"tasks"`
	Questions []struct {
		Status string `json:"status"`
		Page   string `json:"page"`
	} `json:"questions"`
	Reviews []struct {
		State string `json:"state"`
	} `json:"reviews"`
	Runs []struct {
		State string `json:"state"`
	} `json:"runs"`
}

// launcherStartTimeout bounds the wait for a supervisor goblins started,
// which can include a watcher handing it the lock, and launcherPoll is how
// often it looks.
var (
	launcherStartTimeout = time.Minute
	launcherPoll         = 250 * time.Millisecond
)

// aliveTimeout bounds the liveness probe, and snapshotTimeout the fetch of
// the fleet's snapshot for the status line, which never decides whether the
// supervisor runs.
var (
	aliveTimeout    = 3 * time.Second
	snapshotTimeout = 3 * time.Second
)

// runWindowLauncher is goblins --window, which the desktop window runs when
// it is started alone, with --background at login: it finds or starts the
// supervisor as goblins does and shows
// the desktop window, in the tray alone with background, and starts or shows
// no CFO.
func runWindowLauncher(stdout, stderr io.Writer, runtime commandRuntime, background bool) int {
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	board, _, ok := launchBoard(context.Background(), runtime, h, stdout, stderr)
	if !ok {
		return 1
	}
	if err := runtime.openWindow(board, h.State, background); err != nil {
		fmt.Fprintf(stderr, "goblins: the desktop window did not start: %v\n", err)
		return 1
	}
	return 0
}

// runBoardLauncher is goblins --board. It finds or starts the supervisor as
// goblins does and opens the board in the browser every time, and starts or
// shows no CFO in this terminal: the board shows the CFO, and its first-run
// screen while none is registered.
func runBoardLauncher(stdout, stderr io.Writer, runtime commandRuntime) int {
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	board, _, ok := launchBoard(context.Background(), runtime, h, stdout, stderr)
	if !ok {
		return 1
	}
	if err := runtime.openURL(board); err != nil {
		fmt.Fprintf(stderr, "goblins: open the board at %s yourself (%v)\n", board, err)
		return 1
	}
	return 0
}

// launchBoard finds the supervisor, or starts one detached from this
// terminal, and prints the banner with the board's link and the fleet's
// status. started says whether this launch started the supervisor.
func launchBoard(ctx context.Context, runtime commandRuntime, h home.Home, stdout, stderr io.Writer) (board string, started, ok bool) {
	board, status, running := liveBoard(ctx, h.State)
	if !running {
		exited, startErr := runtime.startServe(h)
		var taken boardAddressTaken
		switch {
		case errors.As(startErr, &taken):
			// The board's address is the same every time, so a board is
			// never started on another one because this one is in use. Only
			// this home's own supervisor, started a moment ago by another
			// goblins and not yet recorded, is waited for below.
			if !sameHomePath(taken.home, h.Root) && !anotherSupervisorStarting(h.State) {
				fmt.Fprintf(stderr, "goblins: %v\n", taken)
				return "", false, false
			}
		case startErr != nil && !errors.Is(startErr, errorSharingViolation):
			fmt.Fprintf(stderr, "goblins: the supervisor could not be started: %v\n", startErr)
			return "", false, false
		}
		// A serve.log another process holds open is the supervisor another
		// goblins started a moment ago, writing it: this one waits for that
		// board, and says why it started none when no board comes.
		if board, status, running = waitForBoard(ctx, h.State, exited); !running {
			switch {
			case errors.As(startErr, &taken) && sameHomePath(taken.home, h.Root):
				fmt.Fprintf(stderr, "goblins: this home's supervisor (pid %d) holds the board's address %s but recorded no board. End that process in Windows PowerShell, then run goblins again:\n  Stop-Process -Id %d\n", taken.pid, taken.address, taken.pid)
				return "", false, false
			case errors.As(startErr, &taken):
				fmt.Fprintf(stderr, "goblins: %v\n", taken)
			case startErr != nil:
				fmt.Fprintf(stderr, "goblins: the supervisor was not started, because another process held %s (%v)\n", serveLogPath(h.State), startErr)
			}
			fmt.Fprintf(stderr, "goblins: the supervisor did not start; the end of %s says:\n%s", serveLogPath(h.State), logTail(serveLogPath(h.State), 12))
			return "", false, false
		}
		started = true
	}
	fmt.Fprint(stdout, renderBanner(bannerColor(stdout), board, status))
	return board, started, true
}

// liveBoard returns the board a supervisor serves at the address its record
// names, with the status line for its snapshot. A record whose supervisor
// does not answer is stale: its supervisor ended without removing it.
func liveBoard(ctx context.Context, stateDir string) (string, string, bool) {
	record, err := readBoardRecord(stateDir)
	if err != nil {
		return "", "", false
	}
	if err := boardAlive(ctx, record); err != nil {
		return "", "", false
	}
	return record.URL, boardStatus(ctx, record.URL), true
}

// boardAlive reports whether the supervisor the record names answers as
// itself, asking only for its pid. It never waits on the fleet's snapshot: on
// 2026-10-01 a healthy board took eight seconds to build one, the launcher
// read the supervisor as dead, and goblins started a second one over it. Only
// a successful answer naming the recorded pid is that supervisor; any other
// listener at the address, or an answer that names no pid or another, means
// the record is stale.
func boardAlive(ctx context.Context, record boardRecord) error {
	ctx, cancel := context.WithTimeout(ctx, aliveTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, record.URL+"/api/alive", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("the board at %s answered HTTP %d, not as a supervisor", record.URL, response.StatusCode)
	}
	var alive struct {
		PID int `json:"pid"`
	}
	if err := json.NewDecoder(response.Body).Decode(&alive); err != nil {
		return fmt.Errorf("the board at %s did not say which supervisor it is: %w", record.URL, err)
	}
	if record.PID <= 0 || alive.PID != record.PID {
		return fmt.Errorf("the board at %s is pid %d, not the recorded pid %d", record.URL, alive.PID, record.PID)
	}
	return nil
}

// boardStatus fetches the board's snapshot and returns its status line, or
// says why there is none. A snapshot that does not come within
// snapshotTimeout leaves the line saying so; the board is up either way.
func boardStatus(ctx context.Context, board string) string {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, board+"/api/snapshot", nil)
	if err != nil {
		return "the board is up but its status could not be asked for"
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "the board is up; the fleet's status is still loading"
	}
	defer response.Body.Close()
	var snapshot launcherSnapshot
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&snapshot) != nil {
		return fmt.Sprintf("the board is up but could not read the fleet's state (HTTP %d)", response.StatusCode)
	}
	return statusLine(snapshot)
}

// waitForBoard waits for a supervisor this launch started to answer, and
// gives up when it exits first or the wait runs out. When two goblins start
// at once, the other's supervisor can be taking the watcher lock over from a
// watcher, or already hold it, when this launch's exits, before it has
// written its board record: this launch waits for that board instead.
func waitForBoard(ctx context.Context, stateDir string, exited <-chan struct{}) (string, string, bool) {
	deadline := time.After(launcherStartTimeout)
	for {
		if board, status, ok := liveBoard(ctx, stateDir); ok {
			return board, status, true
		}
		select {
		case <-exited:
			if !anotherSupervisorStarting(stateDir) {
				return "", "", false
			}
			exited = nil
		case <-deadline:
			return "", "", false
		case <-time.After(launcherPoll):
		}
	}
}

// anotherSupervisorStarting reports whether a live supervisor is waiting for
// a watcher to hand it the watcher lock, or holds the lock.
func anotherSupervisorStarting(stateDir string) bool {
	if watch.HandoverPending(stateDir) {
		return true
	}
	holder, err := lock.ReadNamed(stateDir, ".watch.lock")
	return err == nil && holder.Session != watch.WatcherSession && holder.VerifiedAlive()
}

// statusLine says, in the board's words, what the CFO is doing, how many
// goblins are working, and how much waits on the Overlord: the header badge's
// count of pending questions, open review items and run items ready or
// running, where a question its goblin asked about its own open review page
// is that page's one item.
func statusLine(snapshot launcherSnapshot) string {
	cfo := "CFO supervising"
	if snapshot.Registration != "" {
		cfo = "CFO not connected"
	}
	working := 0
	for _, task := range snapshot.Tasks {
		if task.Phase == "working" {
			working++
		}
	}
	waiting := 0
	for _, question := range snapshot.Questions {
		if question.Status == "pending" && question.Page == "" {
			waiting++
		}
	}
	for _, review := range snapshot.Reviews {
		if review.State == "open" {
			waiting++
		}
	}
	for _, run := range snapshot.Runs {
		if run.State == "ready" || run.State == "running" {
			waiting++
		}
	}
	goblins := "goblins"
	if working == 1 {
		goblins = "goblin"
	}
	return fmt.Sprintf("%s · %d %s working · %d waiting on you", cfo, working, goblins, waiting)
}

// logTail returns the last lines of a log, or says there is none.
func logTail(path string, lines int) string {
	data, err := fsx.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return "(nothing)\n"
	}
	all := strings.Split(strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n") + "\n"
}

// Windows process creation flags for a supervisor that outlives the terminal
// that started it: a console of its own with no window, its own process
// group, and outside the terminal's job where the job allows it.
const (
	createNoWindow         = 0x08000000
	createNewProcessGroup  = 0x00000200
	createBreakawayFromJob = 0x01000000
)

// errorSharingViolation is Windows' ERROR_SHARING_VIOLATION, which opening a
// file another process holds without sharing fails with.
const errorSharingViolation = syscall.Errno(32)

// startDetachedServe starts this binary's serve in the home on the board's
// address, detached from this terminal. An address already in use starts
// nothing and is a boardAddressTaken. The returned channel closes when the
// supervisor exits.
func startDetachedServe(h home.Home) (<-chan struct{}, error) {
	address := boardAddress()
	if !loopbackAddress(address) {
		return nil, fmt.Errorf("%s is %q, not a numeric loopback address such as %s", boardAddressVariable, address, defaultBoardAddress)
	}
	if err := boardAddressFree(context.Background(), address); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	command, err := startDetached(executable, h.Root, serveLogPath(h.State), "serve", "--listen", address)
	if err != nil {
		return nil, errors.Join(errors.New("start cfo serve"), err)
	}
	exited := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(exited)
	}()
	return exited, nil
}

// boardAddressTaken says the board's address cannot be listened on, and
// which Code Goblins supervisor holds it when one answers there: its pid, and
// its home when it says. Another program holding it leaves both empty.
type boardAddressTaken struct {
	address string
	cause   error
	pid     int
	home    string
}

func (e boardAddressTaken) Error() string {
	own := fmt.Sprintf("give this home's board an address of its own by setting %s, for example to 127.0.0.1:4311", boardAddressVariable)
	switch {
	case e.pid <= 0:
		return fmt.Sprintf("the board's address %s is in use by another program, so no supervisor was started (%v). Close that program, or %s", e.address, e.cause, own)
	case e.home == "":
		return fmt.Sprintf("the board's address %s is in use by another Code Goblins supervisor (pid %d), so no second one was started. Stop that one with goblins stop in its home, or %s", e.address, e.pid, own)
	default:
		return fmt.Sprintf("the board's address %s is in use by the Code Goblins fleet in %s (supervisor pid %d), so no second one was started. To use that fleet, set CFO_HOME to its folder; to run this home beside it, %s", e.address, e.home, e.pid, own)
	}
}

// boardAddressFree reports whether address can be listened on, and returns a
// boardAddressTaken when it cannot. It asks whoever listens there whether it
// is a Code Goblins supervisor, which answers with its pid and its home.
func boardAddressFree(ctx context.Context, address string) error {
	listener, err := net.Listen("tcp", address)
	if err == nil {
		return listener.Close()
	}
	taken := boardAddressTaken{address: address, cause: err}
	ctx, cancel := context.WithTimeout(ctx, aliveTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/api/alive", nil)
	if err != nil {
		return taken
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return taken
	}
	defer response.Body.Close()
	var alive struct {
		PID  int    `json:"pid"`
		Home string `json:"home"`
	}
	if response.StatusCode == http.StatusOK && json.NewDecoder(response.Body).Decode(&alive) == nil && alive.PID > 0 {
		taken.pid, taken.home = alive.PID, alive.Home
	}
	return taken
}

// startDetached starts executable in dir with a hidden console of its own,
// which the console programs it runs inherit instead of each opening a
// window, and its output appended to logPath. A job that forbids breakaway
// refuses that flag, so the start is retried inside the job rather than not
// made.
func startDetached(executable, dir, logPath string, args ...string) (*exec.Cmd, error) {
	log, err := fsx.OpenAppend(logPath, 0o600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	var command *exec.Cmd
	for _, flags := range []uint32{createNoWindow | createNewProcessGroup | createBreakawayFromJob, createNoWindow | createNewProcessGroup} {
		command = execx.Command(executable, args...)
		command.Dir = dir
		command.Stdout, command.Stderr = log, log
		command.SysProcAttr.CreationFlags |= flags
		command.SysProcAttr.HideWindow = true
		if err = command.Start(); err == nil {
			return command, nil
		}
	}
	return nil, err
}

// errNoWindow says no desktop window sits beside this binary, as in a build
// from source, where the browser shows the board instead.
var errNoWindow = errors.New("no desktop window beside goblins")

// windowProgram is the desktop window's executable, beside this one.
const windowProgram = "goblins-window.exe"

// windowLauncherVariable names this goblins to the window it starts, as
// cmd/goblins-window reads it: its Start at login then starts the window
// alone, which runs the goblins beside it, so the supervisor starts before
// the window and no terminal shows.
const windowLauncherVariable = "CODE_GOBLINS_LAUNCHER"

// openWindow starts the desktop window beside this binary on board, in the
// tray alone with background. A window already running takes the launch as
// its second instance and comes to the front. Its output goes to
// state/window.log, and it outlives this terminal.
func openWindow(board, stateDir string, background bool) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	window := filepath.Join(filepath.Dir(self), windowProgram)
	if _, err := os.Stat(window); errors.Is(err, fs.ErrNotExist) {
		return errNoWindow
	}
	args := []string{"--board", board, "--state", stateDir}
	if background {
		args = append(args, "--background")
	}
	log, err := fsx.OpenAppend(filepath.Join(stateDir, "window.log"), 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	// HideWindow stays unset, unlike the supervisor's start: it would hide
	// the window's first show as well.
	for _, flags := range []uint32{createNewProcessGroup | createBreakawayFromJob, createNewProcessGroup} {
		command := execx.Command(window, args...)
		command.Dir = filepath.Dir(self)
		command.Env = windowEnvironment(os.Environ(), self)
		command.Stdout, command.Stderr = log, log
		command.SysProcAttr.CreationFlags |= flags
		if err = command.Start(); err == nil {
			return command.Process.Release()
		}
	}
	return err
}

// windowEnvironment is what the window starts with: environment, naming
// launcher as the goblins that started it, and without the id and proof value
// of a native terminal that goblins ran in. The window outlives the terminal,
// and the proof value proves a process runs in it, so the window forgets both
// as the supervisor does.
func windowEnvironment(environment []string, launcher string) []string {
	kept := slices.DeleteFunc(slices.Clone(environment), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return strings.EqualFold(name, host.IDVariable) || strings.EqualFold(name, host.ProofVariable)
	})
	return append(kept, windowLauncherVariable+"="+launcher)
}

// openInBrowser opens url in the default browser through the URL protocol
// handler, with no console window.
func openInBrowser(target string) error {
	return execx.Command("rundll32.exe", "url.dll,FileProtocolHandler", target).Start()
}
