package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/update"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// A build stand-in is this test binary with a marker appended naming how it
// behaves as serve. Appending to an executable leaves it runnable and gives
// every build its own hash, as distinct builds have.
const buildMarker = "\x00CFO-TEST-BUILD:"

// The builds an update test installs:
//   - previous: a supervisor from before /api/alive and the handover, which
//     refuses a held watcher lock and answers only its board page;
//   - candidate: a supervisor that takes the lock over and answers /api/alive;
//   - silent: a supervisor that holds the lock and its record but never
//     answers;
//   - crash: a supervisor that exits at once;
//   - flaky: the previous build, silent the first time it serves;
//   - tampered: a changed copy of the previous build, which leaves
//     state/test-tampered-ran behind if it ever runs.
func standInBuild() (string, bool) {
	program, err := os.Executable()
	if err != nil {
		return "", false
	}
	file, err := os.Open(program)
	if err != nil {
		return "", false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() < 64 {
		return "", false
	}
	tail := make([]byte, 64)
	if _, err := file.ReadAt(tail, info.Size()-64); err != nil && err != io.EOF {
		return "", false
	}
	at := bytes.LastIndex(tail, []byte(buildMarker))
	if at < 0 {
		return "", false
	}
	mode := tail[at+len(buildMarker):]
	return string(bytes.TrimRight(mode, "\x00")), true
}

// runStandInBuild runs this binary as the build its marker names, when it
// has one.
func runStandInBuild() (int, bool) {
	build, ok := standInBuild()
	if !ok {
		return 0, false
	}
	if len(os.Args) < 2 {
		return 2, true
	}
	switch os.Args[1] {
	case "serve":
		return standInServe(build), true
	case "update":
		return standInUpdate(), true
	}
	// Anything else, a terminal's host or a watcher, idles until ended.
	time.Sleep(3 * time.Minute)
	return 0, true
}

// standInHome is the home the test names with CFO_TEST_UPDATE_ROOT, or, when
// the test sets CFO_TEST_UPDATE_RESOLVE instead, the one cfo resolves from its
// environment, as a pasted recovery line resolves it. With neither, a stand-in
// refuses rather than fall back on a home it inherited.
func standInHome() home.Home {
	if os.Getenv("CFO_TEST_UPDATE_RESOLVE") != "" {
		h, err := home.Resolve()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return h
	}
	root := os.Getenv("CFO_TEST_UPDATE_ROOT")
	if root == "" {
		fmt.Fprintln(os.Stderr, "stand-in build: the test named no home")
		os.Exit(2)
	}
	return home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
}

// standInServe behaves as the build's supervisor in the home the test names.
func standInServe(build string) int {
	stateDir := standInHome().State
	if build == "flaky" {
		starts := filepath.Join(stateDir, "test-serve-starts")
		data, _ := os.ReadFile(starts)
		count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		_ = os.WriteFile(starts, []byte(strconv.Itoa(count+1)), 0o600)
		build = "previous"
		if count == 0 {
			build = "silent"
		}
	}
	switch build {
	case "crash":
		return 1
	case "tampered":
		_ = os.WriteFile(filepath.Join(stateDir, "test-tampered-ran"), nil, 0o600)
		return 1
	case "previous":
		if _, err := lock.AcquireExclusiveNamed(stateDir, ".watch.lock"); err != nil {
			fmt.Fprintln(os.Stderr, "existing watch owner must finish before serve:", err)
			return 1
		}
	default:
		if err := supervisor.AcquireWatchLock(stateDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	defer lock.ReleaseExclusiveNamed(stateDir, ".watch.lock")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 1
	}
	if err := writeBoardRecord(stateDir, boardRecord{PID: os.Getpid(), URL: "http://" + listener.Addr().String()}); err != nil {
		return 1
	}
	defer removeBoardRecord(stateDir, os.Getpid())
	hang := make(chan struct{})
	go func() {
		_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case build == "silent":
				<-hang
			case r.URL.Path == "/":
				_, _ = w.Write([]byte("board"))
			case r.URL.Path == "/api/alive" && build == "candidate":
				_, _ = fmt.Fprintf(w, `{"pid":%d}`, os.Getpid())
			default:
				http.NotFound(w, r)
			}
		}))
	}()
	for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		var request struct {
			PID int `json:"pid"`
		}
		data, err := os.ReadFile(filepath.Join(stateDir, "serve.stop"))
		if err == nil && json.Unmarshal(data, &request) == nil && request.PID == os.Getpid() {
			_ = os.Remove(filepath.Join(stateDir, "serve.stop"))
			return 0
		}
	}
	return 0
}

// standInUpdate runs the real cfo update in the home the test names, with
// the seams the test asks for: CFO_TEST_UPDATE_INTERRUPT ends the process at
// a step, CFO_TEST_UPDATE_FAIL_RECORD fails the journal write of a phase,
// CFO_TEST_UPDATE_BREAK_SWAP removes a staged alias before the swap,
// CFO_TEST_UPDATE_HOLD holds an alias open from the rollback on,
// CFO_TEST_UPDATE_TAMPER replaces the named verified copies with the home's
// tampered.exe as the rollback begins, and CFO_TEST_UPDATE_PAUSE waits at a
// step.
func standInUpdate() int {
	h := standInHome()
	if wait, err := time.ParseDuration(os.Getenv("CFO_TEST_UPDATE_SERVE_WAIT")); err == nil {
		updateServeWait = wait
	}
	if wait, err := time.ParseDuration(os.Getenv("CFO_TEST_HANDOVER_WAIT")); err == nil {
		watch.HandoverWait = wait
	}
	updateStopWait = 5 * time.Second
	interrupt := os.Getenv("CFO_TEST_UPDATE_INTERRUPT")
	pause := os.Getenv("CFO_TEST_UPDATE_PAUSE")
	hold := os.Getenv("CFO_TEST_UPDATE_HOLD")
	tamper := os.Getenv("CFO_TEST_UPDATE_TAMPER")
	breakSwap := os.Getenv("CFO_TEST_UPDATE_BREAK_SWAP") != ""
	updateInterrupt = func(step string) {
		if step == interrupt {
			os.Exit(9)
		}
		if step == pause {
			time.Sleep(20 * time.Second)
		}
		if step == "stopped" && breakSwap {
			_ = os.Remove(filepath.Join(h.Root, "goblins.exe.update-new"))
		}
		if step == "rolling-back" && tamper != "" {
			tampered, err := os.ReadFile(filepath.Join(h.Root, "tampered.exe"))
			if err != nil {
				os.Exit(8)
			}
			for _, name := range strings.Split(tamper, ",") {
				if err := os.WriteFile(filepath.Join(update.Dir(h.State), name), tampered, 0o755); err != nil {
					os.Exit(8)
				}
			}
		}
		if step == "rolling-back" && hold != "" {
			name, err := syscall.UTF16PtrFromString(filepath.Join(h.Root, hold))
			if err == nil {
				// Held for the rest of the process, as a program reading
				// it would, so it can be neither moved nor replaced.
				_, _ = syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
			}
		}
	}
	if failing := update.Phase(os.Getenv("CFO_TEST_UPDATE_FAIL_RECORD")); failing != "" {
		recordUpdate = func(stateDir string, journal *update.Journal, phase update.Phase, outcome string) error {
			if phase == failing {
				return fmt.Errorf("the journal cannot be written at %s", phase)
			}
			return update.Record(stateDir, journal, phase, outcome)
		}
	}
	runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}
	return runUpdate(os.Args[2:], os.Stdout, os.Stderr, runtime)
}
