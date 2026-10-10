package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
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
//   - second-start: the candidate, which exits at once the first time it
//     serves and serves from its second start on;
//   - tampered: a changed copy of the previous build, which leaves
//     state/test-tampered-ran behind if it ever runs.
//
// A supervisor started with CFO_TEST_SERVE_DEAF never honours a stop request,
// as one busy with work of its own does not, so whoever stops it ends it.
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
	// A stand-in carries its board unless its test says it was built from a
	// checkout that never built one.
	if os.Getenv("CFO_TEST_NO_BOARD") != "" {
		boardBuilt = func() bool { return false }
	}
	switch os.Args[1] {
	case "serve":
		return standInServe(build), true
	case "update":
		return standInUpdate(), true
	case "install":
		return standInInstall(), true
	case "hold":
		return standInHold(), true
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

// standInServe behaves as the build's supervisor in the home the test names,
// listening where --listen says, as serve does.
func standInServe(build string) int {
	stateDir := standInHome().State
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	address := flags.String("listen", "127.0.0.1:0", "")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return 2
	}
	if build == "flaky" || build == "second-start" {
		starts := filepath.Join(stateDir, "test-serve-starts")
		data, _ := os.ReadFile(starts)
		count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		_ = os.WriteFile(starts, []byte(strconv.Itoa(count+1)), 0o600)
		first, later := "silent", "previous"
		if build == "second-start" {
			first, later = "crash", "candidate"
		}
		build = later
		if count == 0 {
			build = first
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
	// The console it was given, which a test reads to prove the supervisor an
	// update or an install starts has one of its own with no window.
	probeConsole(filepath.Join(stateDir, "test-serve-console-"+strconv.Itoa(os.Getpid())))
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := writeBoardRecord(stateDir, boardRecord{PID: os.Getpid(), URL: "http://" + listener.Addr().String()}); err != nil {
		return 1
	}
	defer removeBoardRecord(stateDir, os.Getpid())
	defer listener.Close()
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
		if err == nil && json.Unmarshal(data, &request) == nil && request.PID == os.Getpid() && os.Getenv("CFO_TEST_SERVE_DEAF") == "" {
			_ = os.Remove(filepath.Join(stateDir, "serve.stop"))
			stamp(filepath.Join(stateDir, servedUntilFile))
			return 0
		}
	}
	return 0
}

// servedUntilFile and heldUntilFile are where a stand-in supervisor leaves the
// time it honoured a stop request, and a stand-in for lifecycle work the time
// it finished, under the home's state.
const (
	servedUntilFile = "test-serve-stopped"
	heldUntilFile   = "test-held-released"
)

// stamp writes the time now to path.
func stamp(path string) {
	_ = os.WriteFile(path, []byte(strconv.FormatInt(time.Now().UnixNano(), 10)), 0o600)
}

// standInHold stands in for lifecycle work in flight, a clean-up, a pause or
// a resume: it holds the lock os.Args[2] names in the home's state, as that
// work holds its own, until os.Args[3] after an update's journal says it is
// prepared, or, for "forever", until it is ended.
func standInHold() int {
	if len(os.Args) != 4 {
		return 2
	}
	stateDir := standInHome().State
	if _, err := lock.AcquireExclusiveNamed(stateDir, os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if os.Args[3] == "forever" {
		time.Sleep(3 * time.Minute)
		return 0
	}
	after, err := time.ParseDuration(os.Args[3])
	if err != nil {
		return 2
	}
	for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if journal, err := update.ReadJournal(stateDir); err == nil && journal.Phase == update.Prepared {
			break
		}
	}
	time.Sleep(after)
	stamp(filepath.Join(stateDir, heldUntilFile))
	if err := lock.ReleaseExclusiveNamed(stateDir, os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// holdFailedExit is the exit of a stand-in update whose test asked it to hold
// an alias it could not hold, so the test fails on its seam, distinctly.
const holdFailedExit = 7

// holdWait bounds how long the seam waits to hold an alias: Windows can keep
// a candidate's image held for a moment after its process exits.
const holdWait = 10 * time.Second

// holdExclusively opens path with no sharing, trying again until wait runs
// out while another handle still has it, and returns the handle it holds.
func holdExclusively(path string, wait time.Duration) (syscall.Handle, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	deadline := time.Now().Add(wait)
	for {
		handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			return handle, nil
		}
		if time.Now().After(deadline) {
			return syscall.InvalidHandle, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// lateServeFile is where startLateServe leaves the pid of the supervisor it
// started, under the home's state.
const lateServeFile = "test-late-serve"

// startLateServe starts the home's goblins.exe as a supervisor of this home
// and waits until it serves the board or has exited, so the update goes on
// with that supervisor either holding the watcher lock or refused it.
func startLateServe(h home.Home) {
	late := exec.Command(filepath.Join(h.Bin(), "goblins.exe"), "serve", "--listen", "127.0.0.1:0")
	late.Dir = h.Root
	if err := late.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "stand-in update: the test's late supervisor did not start: %v\n", err)
		os.Exit(8)
	}
	exited := make(chan struct{})
	go func() {
		_ = late.Wait()
		close(exited)
	}()
	_ = os.WriteFile(filepath.Join(h.State, lateServeFile), []byte(strconv.Itoa(late.Process.Pid)), 0o600)
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		select {
		case <-exited:
			return
		default:
		}
		if record, err := readBoardRecord(h.State); err == nil && record.PID == late.Process.Pid {
			return
		}
	}
}

// standInUpdate runs the real cfo update in the home the test names, with
// the seams the test asks for: CFO_TEST_UPDATE_INTERRUPT ends the process at
// a step, CFO_TEST_UPDATE_FAIL_RECORD fails the journal write of a phase,
// CFO_TEST_UPDATE_BREAK_SWAP removes a staged alias before the swap,
// CFO_TEST_UPDATE_HOLD holds an alias open from the rollback on,
// CFO_TEST_UPDATE_TAMPER replaces the named verified copies with the home's
// tampered.exe as the rollback begins, CFO_TEST_UPDATE_PAUSE waits at a
// step, CFO_TEST_UPDATE_LATE_SERVE starts the home's goblins.exe as a
// supervisor at a step, as a goblins opened while the board is away does,
// CFO_TEST_UPDATE_BOUND bounds the update from prepared to done, and
// CFO_TEST_UPDATE_QUIET_WAIT bounds its wait for lifecycle work in flight.
// standInInstall runs the real cfo install as the build, with the waits a test
// names, so a supervisor the install restarts is this build's stand-in.
func standInInstall() int {
	if wait, err := time.ParseDuration(os.Getenv("CFO_TEST_UPDATE_SERVE_WAIT")); err == nil {
		updateServeWait = wait
	}
	if wait, err := time.ParseDuration(os.Getenv("CFO_TEST_HANDOVER_WAIT")); err == nil {
		watch.HandoverWait = wait
	}
	updateStopWait = 5 * time.Second
	// A test of the refresh's bound has the install never end.
	if os.Getenv("CFO_TEST_INSTALL_HANGS") != "" {
		time.Sleep(3 * time.Minute)
	}
	return runInstall(os.Args[2:], os.Stdout, os.Stderr)
}

func standInUpdate() int {
	h := standInHome()
	// A home's installed build updates from a release as the version the
	// test names, and passes the proof that the Overlord runs it unless the
	// test asks for the proof.
	if named := os.Getenv("CFO_TEST_VERSION"); named != "" {
		version = named
	}
	if os.Getenv("CFO_TEST_UPDATE_PROOF") == "" {
		updaterRefusal = func(home.Home) error { return nil }
	}
	if wait, err := time.ParseDuration(os.Getenv("CFO_TEST_UPDATE_SERVE_WAIT")); err == nil {
		updateServeWait = wait
	}
	if wait, err := time.ParseDuration(os.Getenv("CFO_TEST_HANDOVER_WAIT")); err == nil {
		watch.HandoverWait = wait
	}
	updateStopWait = 5 * time.Second
	// A stand-in build is some hundred megabytes, which a loaded machine
	// copies slowly, so only a test of a bound sets one.
	updatePrepareBound, updateBound, updateRecoverBound = 10*time.Minute, 10*time.Minute, 10*time.Minute
	if bound, err := time.ParseDuration(os.Getenv("CFO_TEST_UPDATE_BOUND")); err == nil {
		updateBound = bound
	}
	if wait, err := time.ParseDuration(os.Getenv("CFO_TEST_UPDATE_QUIET_WAIT")); err == nil {
		updateQuietWait = wait
	}
	if wait, err := time.ParseDuration(os.Getenv("CFO_TEST_RELEASE_REFRESH_WAIT")); err == nil {
		releaseRefreshWait = wait
	}
	interrupt := os.Getenv("CFO_TEST_UPDATE_INTERRUPT")
	pause := os.Getenv("CFO_TEST_UPDATE_PAUSE")
	hold := os.Getenv("CFO_TEST_UPDATE_HOLD")
	tamper := os.Getenv("CFO_TEST_UPDATE_TAMPER")
	breakSwap := os.Getenv("CFO_TEST_UPDATE_BREAK_SWAP") != ""
	late := os.Getenv("CFO_TEST_UPDATE_LATE_SERVE")
	updateInterrupt = func(step string) {
		if step == interrupt {
			os.Exit(9)
		}
		if step == late {
			startLateServe(h)
		}
		if step == pause {
			time.Sleep(20 * time.Second)
		}
		if step == "stopped" && breakSwap {
			_ = os.Remove(filepath.Join(h.Bin(), "goblins.exe.update-new"))
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
			// Held for the rest of the process, as a program reading it
			// would, so it can be neither moved nor replaced. A test whose
			// alias could not be held fails on this seam, not the product.
			if _, err := holdExclusively(filepath.Join(h.Bin(), hold), holdWait); err != nil {
				fmt.Fprintf(os.Stderr, "stand-in update: the test's alias %s could not be held: %v\n", hold, err)
				os.Exit(holdFailedExit)
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
