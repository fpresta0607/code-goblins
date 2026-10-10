package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// sessionCLI is the name the test binary runs under as the home's cfo, the
// command a stand-in session's harness starts for each hook and each command
// of its tools. It sits beside the stand-in harness, claude.exe.
const sessionCLI = "cfo.exe"

// sessionRequests is the folder, in a stand-in harness's working folder, the
// test asks it to run commands through.
const sessionRequests = "session-requests"

// scratchHomeMarker is the file a test puts in the home its stand-in sessions
// act on. The stand-in cfo runs against no home without it, so a session that
// lost its environment never reaches the home of the machine the tests run on.
const scratchHomeMarker = ".custody-test-home"

// actedLog is the file, in the scratch home, the stand-in cfo records each
// steer and each dispatch in instead of sending or starting anything.
const actedLog = "acted.log"

// sessionRequest is one command a stand-in harness runs as the home's cfo, as
// a harness runs a hook or a command of one of its tools.
type sessionRequest struct {
	Args  []string          `json:"args"`
	Stdin string            `json:"stdin"`
	Env   map[string]string `json:"env,omitempty"`
}

// sessionResponse is how that command ended, and how long the harness waited
// for it, from starting its process to its exit.
type sessionResponse struct {
	Exit   int           `json:"exit"`
	Stdout string        `json:"stdout"`
	Stderr string        `json:"stderr"`
	Took   time.Duration `json:"took"`
}

// serveSessionRequests makes the stand-in harness run each command the test
// asks for, each in a process of its own so a Stop hook that waits on the
// wake queue holds up nothing else.
func serveSessionRequests() {
	cli := filepath.Join(filepath.Dir(os.Args[0]), sessionCLI)
	served := map[string]bool{}
	for ; ; time.Sleep(20 * time.Millisecond) {
		entries, err := os.ReadDir(sessionRequests)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name, isRequest := strings.CutSuffix(entry.Name(), ".request.json")
			if !isRequest || served[name] {
				continue
			}
			served[name] = true
			go serveSessionRequest(cli, name)
		}
	}
}

func serveSessionRequest(cli, name string) {
	respond := func(response sessionResponse) {
		data, _ := json.Marshal(response)
		partial := filepath.Join(sessionRequests, name+".response.partial")
		if err := os.WriteFile(partial, data, 0o600); err == nil {
			_ = os.Rename(partial, filepath.Join(sessionRequests, name+".response.json"))
		}
	}
	// A file just renamed into place can be held a moment by whatever scans
	// new files, so the read waits that out.
	data, err := os.ReadFile(filepath.Join(sessionRequests, name+".request.json"))
	for deadline := time.Now().Add(10 * time.Second); err != nil && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
		data, err = os.ReadFile(filepath.Join(sessionRequests, name+".request.json"))
	}
	var request sessionRequest
	if err == nil {
		err = json.Unmarshal(data, &request)
	}
	if err != nil {
		respond(sessionResponse{Exit: 98, Stderr: err.Error()})
		return
	}
	command := exec.Command(cli, request.Args...)
	command.Stdin = strings.NewReader(request.Stdin)
	command.Env = os.Environ()
	for key, value := range request.Env {
		command.Env = append(command.Env, key+"="+value)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	response := sessionResponse{}
	started := time.Now()
	if err := command.Run(); err != nil {
		var exited *exec.ExitError
		if !errors.As(err, &exited) {
			respond(sessionResponse{Exit: 98, Stderr: err.Error()})
			return
		}
		response.Exit = exited.ExitCode()
	}
	response.Took = time.Since(started)
	response.Stdout, response.Stderr = stdout.String(), stderr.String()
	respond(response)
}

// runSessionCLI is the test binary run as cfo.exe by a stand-in harness: the
// real commands against the scratch home its environment names, except that a
// steer and a dispatch are recorded in the home rather than made.
func runSessionCLI() int {
	root := os.Getenv("CFO_HOME")
	if _, err := os.Stat(filepath.Join(root, scratchHomeMarker)); root == "" || err != nil {
		fmt.Fprintln(os.Stderr, "the stand-in cfo acts only on a scratch home its test marked, and CFO_HOME names none")
		return 97
	}
	if state := os.Getenv("CFO_STATE_OVERRIDE"); state != "" && !strings.EqualFold(filepath.Clean(state), filepath.Join(root, "state")) {
		fmt.Fprintln(os.Stderr, "the stand-in cfo acts only on its scratch home's own state folder")
		return 97
	}
	acted := func(what string) error {
		file, err := os.OpenFile(filepath.Join(root, actedLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = fmt.Fprintln(file, what)
		return err
	}
	runtime := commandRuntime{
		resolveHome: home.Resolve,
		sendText: func(_ context.Context, _ home.Home, target, text string) error {
			return acted("send " + target + " " + text)
		},
		spawn: func(_ context.Context, _ home.Home, request spawn.Request) (spawn.Result, error) {
			return spawn.Result{Output: "spawned " + request.ID}, acted("spawn " + request.ID)
		},
	}
	return runWithRuntime(os.Args[1:], os.Stdout, os.Stderr, runtime)
}

// standInSession is a harness a test started: the test binary as claude.exe,
// which runs the home's cfo for each hook and command the test asks of it.
type standInSession struct {
	// dir is the harness's working folder, and pid its process.
	dir   string
	pid   int
	asked int
}

// sessionPrograms is the folder holding claude.exe and cfo.exe, both this test
// binary, made once for every test of the run: a program copied afresh starts
// slowly on a loaded machine, and each stand-in session starts both.
var sessionPrograms struct {
	once sync.Once
	dir  string
	err  error
}

// removeSessionPrograms removes that folder once the run's tests are over,
// waiting while Windows still holds a program that ran from it.
func removeSessionPrograms() {
	if sessionPrograms.dir == "" {
		return
	}
	for deadline := time.Now().Add(30 * time.Second); os.RemoveAll(sessionPrograms.dir) != nil && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
	}
}

// scratchHome is a primary home a test's stand-in sessions act on, with
// claude.exe and cfo.exe, both this test binary, first on PATH.
func scratchHome(t *testing.T) home.Home {
	t.Helper()
	root := newPrimaryHome(t)
	if err := os.WriteFile(filepath.Join(root, scratchHomeMarker), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sessionPrograms.once.Do(func() {
		self, err := os.Executable()
		if err != nil {
			sessionPrograms.err = err
			return
		}
		program, err := os.ReadFile(self)
		if err != nil {
			sessionPrograms.err = err
			return
		}
		if sessionPrograms.dir, sessionPrograms.err = os.MkdirTemp("", "cfo-session-programs-"); sessionPrograms.err != nil {
			return
		}
		for _, name := range []string{"claude.exe", sessionCLI} {
			if err := os.WriteFile(filepath.Join(sessionPrograms.dir, name), program, 0o700); err != nil {
				sessionPrograms.err = err
				return
			}
		}
	})
	if sessionPrograms.err != nil {
		t.Fatal(sessionPrograms.err)
	}
	t.Setenv("PATH", sessionPrograms.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
}

// startCFOSession starts the CFO of home h as goblins, the board and the
// comeback after a restart start it: the harness in native terminal cfo, in
// the home.
func startCFOSession(t *testing.T, h home.Home) *standInSession {
	t.Helper()
	if err := startNativeCFO(h, h.Root, "claude", nil); err != nil {
		t.Fatalf("start the CFO in native terminal cfo: %v", err)
	}
	t.Cleanup(func() { closeNativeTerminal(t, h.State, supervisor.NativeCFOTerminal) })
	waitForFakeClaudeEnvironment(t, h.Root)
	record, err := host.ReadRecord(h.State, supervisor.NativeCFOTerminal)
	if err != nil {
		t.Fatal(err)
	}
	return &standInSession{dir: h.Root, pid: record.ChildPID}
}

// startOtherSession starts a Claude Code session that is not the CFO's, as
// the Overlord's desktop app starts one: claude.exe in a folder of another
// project, in no native terminal, with the home `cfo install` set for the
// user in its environment.
func startOtherSession(t *testing.T, h home.Home) *standInSession {
	t.Helper()
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	env := cfoTestEnv(t, h.Root, map[string]string{"CLAUDECODE": "1"})
	assertFleetIsolatedEnv(t, env)
	command := exec.Command(claude, "--project-config-root", dir)
	command.Dir, command.Env = dir, env
	// The harness stays until its input closes, as one in a terminal does.
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = input.Close()
		ended := make(chan struct{})
		go func() { _ = command.Wait(); close(ended) }()
		select {
		case <-ended:
		case <-time.After(15 * time.Second):
			_ = command.Process.Kill()
			<-ended
			t.Error("the stand-in session did not end with its input")
		}
	})
	waitForFakeClaudeEnvironment(t, dir)
	return &standInSession{dir: dir, pid: command.Process.Pid}
}

// ask asks the session's harness to run cfo with args, stdin and env beside
// its own environment, and returns how to wait up to limit for it to end. It
// reports a failure rather than ending the test, for a caller that is not on
// the test's goroutine.
func (s *standInSession) ask(env map[string]string, stdin string, args ...string) (func(limit time.Duration) (sessionResponse, bool, error), error) {
	folder := filepath.Join(s.dir, sessionRequests)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return nil, err
	}
	s.asked++
	name := fmt.Sprintf("%03d", s.asked)
	data, err := json.Marshal(sessionRequest{Args: args, Stdin: stdin, Env: env})
	if err != nil {
		return nil, err
	}
	partial := filepath.Join(folder, name+".request.partial")
	if err := os.WriteFile(partial, data, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(partial, filepath.Join(folder, name+".request.json")); err != nil {
		return nil, err
	}
	return func(limit time.Duration) (sessionResponse, bool, error) {
		for deadline := time.Now().Add(limit); ; time.Sleep(20 * time.Millisecond) {
			data, err := os.ReadFile(filepath.Join(folder, name+".response.json"))
			if err == nil {
				var response sessionResponse
				if err := json.Unmarshal(data, &response); err != nil {
					return sessionResponse{}, true, fmt.Errorf("cfo %s: unreadable response: %w", strings.Join(args, " "), err)
				}
				return response, true, nil
			}
			if time.Now().After(deadline) {
				return sessionResponse{}, false, nil
			}
		}
	}, nil
}

// begin is ask on the test's goroutine: a failure ends the test.
func (s *standInSession) begin(t *testing.T, env map[string]string, stdin string, args ...string) func(limit time.Duration) (sessionResponse, bool) {
	t.Helper()
	wait, err := s.ask(env, stdin, args...)
	if err != nil {
		t.Fatal(err)
	}
	return func(limit time.Duration) (sessionResponse, bool) {
		t.Helper()
		response, ended, err := wait(limit)
		if err != nil {
			t.Fatal(err)
		}
		return response, ended
	}
}

// run is begin, waited to its end.
func (s *standInSession) run(t *testing.T, env map[string]string, stdin string, args ...string) sessionResponse {
	t.Helper()
	response, ended := s.begin(t, env, stdin, args...)(60 * time.Second)
	if !ended {
		t.Fatalf("cfo %s did not end within a minute", strings.Join(args, " "))
	}
	return response
}

// hook runs one of the session's Claude Code hooks with its payload.
func (s *standInSession) hook(t *testing.T, name, payload string) sessionResponse {
	t.Helper()
	return s.run(t, nil, payload, "hook", name)
}
