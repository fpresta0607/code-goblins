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
	"syscall"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
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
	data, err := os.ReadFile(boardRecordPath(stateDir))
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

// launcherStartTimeout bounds the wait for a supervisor goblins started, and
// launcherPoll is how often it looks.
var (
	launcherStartTimeout = 30 * time.Second
	launcherPoll         = 250 * time.Millisecond
)

// runLauncher is goblins with no arguments. It finds the supervisor, or
// starts one detached from this terminal, prints the banner with the board's
// link and the fleet's status, and opens the board in the browser when this
// launch started the supervisor; a later goblins only prints the link.
func runLauncher(stdout, stderr io.Writer, runtime commandRuntime) int {
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx := context.Background()
	board, answer, running := liveBoard(ctx, h.State)
	started := false
	if !running {
		exited, err := runtime.startServe(h)
		if err != nil {
			fmt.Fprintf(stderr, "goblins: the supervisor could not be started: %v\n", err)
			return 1
		}
		if board, answer, running = waitForBoard(ctx, h.State, exited); !running {
			fmt.Fprintf(stderr, "goblins: the supervisor did not start; the end of %s says:\n%s", serveLogPath(h.State), logTail(serveLogPath(h.State), 12))
			return 1
		}
		started = true
	}
	fmt.Fprint(stdout, renderBanner(bannerColor(stdout), board, answer.status))
	if started {
		if err := runtime.openURL(board); err != nil {
			fmt.Fprintf(stderr, "goblins: open the board at %s yourself (%v)\n", board, err)
		}
	}
	return startCFOSession(ctx, runtime, answer.cfoLive, stdout, stderr)
}

// boardAnswer is what a running supervisor's board said: the status line, and
// whether the board reaches a live CFO, which a snapshot that could not be
// read cannot deny, so goblins never starts a second CFO when it cannot tell.
type boardAnswer struct {
	status  string
	cfoLive bool
}

// liveBoard returns the board a supervisor serves at the address its record
// names, with its answer. Any answer from that address
// is the supervisor, even one that could not read the fleet's state; a record
// whose address does not answer is stale: its supervisor ended without
// removing it.
func liveBoard(ctx context.Context, stateDir string) (string, boardAnswer, bool) {
	record, err := readBoardRecord(stateDir)
	if err != nil {
		return "", boardAnswer{}, false
	}
	answer, err := boardStatus(ctx, record.URL)
	if err != nil {
		return "", boardAnswer{}, false
	}
	return record.URL, answer, true
}

// boardStatus fetches the board's snapshot and returns its status line, or
// says the board could not read the fleet's state. It fails only when the
// board does not answer.
func boardStatus(ctx context.Context, board string) (boardAnswer, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, board+"/api/snapshot", nil)
	if err != nil {
		return boardAnswer{}, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return boardAnswer{}, err
	}
	defer response.Body.Close()
	var snapshot launcherSnapshot
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&snapshot) != nil {
		return boardAnswer{status: fmt.Sprintf("the board is up but could not read the fleet's state (HTTP %d)", response.StatusCode), cfoLive: true}, nil
	}
	return boardAnswer{status: statusLine(snapshot), cfoLive: snapshot.Registration == ""}, nil
}

// waitForBoard waits for a supervisor this launch started to answer, and
// gives up when it exits first or the wait runs out.
func waitForBoard(ctx context.Context, stateDir string, exited <-chan struct{}) (string, boardAnswer, bool) {
	deadline := time.After(launcherStartTimeout)
	for {
		if board, answer, ok := liveBoard(ctx, stateDir); ok {
			return board, answer, true
		}
		select {
		case <-exited:
			return "", boardAnswer{}, false
		case <-deadline:
			return "", boardAnswer{}, false
		case <-time.After(launcherPoll):
		}
	}
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
	data, err := os.ReadFile(path)
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

// startDetachedServe starts this binary's serve in the home, detached from
// this terminal. The returned channel closes when the supervisor exits.
func startDetachedServe(h home.Home) (<-chan struct{}, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	command, err := startDetached(executable, h.Root, serveLogPath(h.State), "serve")
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

// startDetached starts executable in dir with a hidden console of its own,
// which the console programs it runs inherit instead of each opening a
// window, and its output appended to logPath. A job that forbids breakaway
// refuses that flag, so the start is retried inside the job rather than not
// made.
func startDetached(executable, dir, logPath string, args ...string) (*exec.Cmd, error) {
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	var command *exec.Cmd
	for _, flags := range []uint32{createNoWindow | createNewProcessGroup | createBreakawayFromJob, createNoWindow | createNewProcessGroup} {
		command = exec.Command(executable, args...)
		command.Dir = dir
		command.Stdout, command.Stderr = log, log
		command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
		if err = command.Start(); err == nil {
			return command, nil
		}
	}
	return nil, err
}

// openInBrowser opens url in the default browser through the URL protocol
// handler, with no console window.
func openInBrowser(target string) error {
	return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", target).Start()
}
