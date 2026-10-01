package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
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
// the browser.
func readBoardRecord(stateDir string) (boardRecord, error) {
	var record boardRecord
	data, err := fsx.ReadFile(boardRecordPath(stateDir))
	if err != nil {
		return boardRecord{}, err
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return boardRecord{}, err
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

// runLauncher is goblins with no arguments, or with --native or --harness. It finds
// the supervisor, or starts one detached from this terminal, prints the banner
// with the board's link and the fleet's status, and opens the board in the
// browser when this launch started the supervisor; a later goblins only
// prints the link. native starts a new CFO in a native terminal rather than in
// Herdr, and harness, when set, is the harness goblins starts the CFO as from
// now on.
func runLauncher(stdout, stderr io.Writer, runtime commandRuntime, native bool, harness string) int {
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx := context.Background()
	board, started, ok := launchBoard(ctx, runtime, h, stdout, stderr)
	if !ok {
		return 1
	}
	if started {
		if err := runtime.openURL(board); err != nil {
			fmt.Fprintf(stderr, "goblins: open the board at %s yourself (%v)\n", board, err)
		}
	}
	return startCFOSession(ctx, runtime, h, native, harness, stdout, stderr)
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
		exited, err := runtime.startServe(h)
		if err != nil && !errors.Is(err, errorSharingViolation) {
			fmt.Fprintf(stderr, "goblins: the supervisor could not be started: %v\n", err)
			return "", false, false
		}
		// A serve.log another process holds open is the supervisor another
		// goblins started a moment ago, writing it: this one waits for that
		// board.
		if board, status, running = waitForBoard(ctx, h.State, exited); !running {
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
	if alive.PID != record.PID {
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
// running.
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
		if question.Status == "pending" {
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

// startDetachedServe starts this binary's serve in the home, detached from
// this terminal. The returned channel closes when the supervisor exits.
func startDetachedServe(h home.Home) (<-chan struct{}, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	command, err := startDetached(executable, h.Root, serveLogPath(h.State), serveArguments(defaultBoardAddress)...)
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

// serveArguments is cfo serve on the preferred address, or on a free loopback
// port when another program, such as the supervisor of another CFO home,
// already listens there: goblins finds the board through its record either
// way.
func serveArguments(preferred string) []string {
	listener, err := net.Listen("tcp", preferred)
	if err != nil {
		return []string{"serve", "--listen", "127.0.0.1:0"}
	}
	_ = listener.Close()
	return []string{"serve", "--listen", preferred}
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

// openInBrowser opens url in the default browser through the URL protocol
// handler, with no console window.
func openInBrowser(target string) error {
	return execx.Command("rundll32.exe", "url.dll,FileProtocolHandler", target).Start()
}
