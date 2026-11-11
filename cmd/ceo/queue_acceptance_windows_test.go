package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// TestQueuedBoardMessagesReachAWorkingCEOOnce is the end-to-end proof that
// the messages the Overlord queues in Modern Chat while a session works are
// each submitted to it once and never left typed in its terminal's composer
// (#2306). A Claude Code CEO in a scratch home under `ceo serve --example`
// runs a turn of many tool calls, and while it runs the board is sent
// messages as its page sends them:
//
//   - working: five messages one after another, short, long enough to go as
//     a paste, and ending as a path, a question and a file mention do;
//   - stalled: one message typed while the CEO's harness reads nothing for
//     twelve seconds, as the live CEO's did for seconds at a time on
//     2026-11-10 (its terminal's log: "input sat unread in the console for
//     3.178s"), then one more after it. A delivery that gave up on seeing its
//     text after five seconds left the first typed and unsubmitted, and the
//     second waited behind it for good.
//
// Each message must reach the CEO's conversation exactly once, the terminal's
// composer must end empty, and the board must count every one delivered.
//
// It runs a real harness on the Overlord's subscription, so it runs only when
// asked:
//
//	CEO_QUEUE_REAL=1 CEO_QUEUE_BINARY=<ceo.exe> CEO_QUEUE_RESULTS=<a directory for the record>
//	[CEO_QUEUE_ONLY=working|stalled]
func TestQueuedBoardMessagesReachAWorkingCEOOnce(t *testing.T) {
	if os.Getenv("CEO_QUEUE_REAL") != "1" {
		t.Skip("set CEO_QUEUE_REAL=1 with CEO_QUEUE_BINARY and CEO_QUEUE_RESULTS to prove queued messages reach a real working CEO")
	}
	binary, results := os.Getenv("CEO_QUEUE_BINARY"), os.Getenv("CEO_QUEUE_RESULTS")
	if binary == "" || results == "" {
		t.Fatal("CEO_QUEUE_BINARY and CEO_QUEUE_RESULTS are both required")
	}
	if root, state := home.Inherited(); root != "" || state != "" {
		t.Fatalf("this process inherited the fleet home %s (state %s); unset CEO_HOME and CEO_STATE_OVERRIDE first", root, state)
	}
	for i, scenario := range []string{"working", "stalled"} {
		if only := os.Getenv("CEO_QUEUE_ONLY"); only != "" && only != scenario {
			continue
		}
		t.Run(scenario, func(t *testing.T) {
			proveQueuedMessages(t, &wakeProof{binary: binary, root: filepath.Join(results, scenario), ceo: "claude", port: 4432 + i}, scenario)
		})
	}
}

// queueSteps is how many tool calls the proof's CEO makes in its turn, more
// than the messages take to reach it.
const queueSteps = 30

// queueStall is how long the stalled proof's harness reads nothing: longer
// than the five seconds a delivery to the CEO once waited to see its text
// shown, and well inside the time it waits now.
const queueStall = 12 * time.Second

// queuedProofMessage is one message the proof sends on the board.
type queuedProofMessage struct{ id, marker, text string }

func proveQueuedMessages(t *testing.T, p *wakeProof, scenario string) {
	if err := os.MkdirAll(p.root, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(p.root, "proof.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	p.log = logFile
	p.home = home.Home{Root: filepath.Join(p.root, "home"), State: filepath.Join(p.root, "home", "state"), Data: filepath.Join(p.root, "home", "data")}
	for _, dir := range []string{p.home.State, p.home.Data} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeProofFile(t, filepath.Join(p.home.Root, "AGENTS.md"), "# Scratch CEO home for the queued message proof\n")
	writeProofFile(t, filepath.Join(p.home.Root, home.InstalledMarker), "")
	p.say("scratch home %s; CEO %s; scenario %s", p.home.Root, p.ceo, scenario)
	p.env = p.environment()

	serve := exec.Command(p.binary, "serve", "--example", "--listen", fmt.Sprintf("127.0.0.1:%d", p.port))
	serve.Env = p.env
	serve.Stdout, serve.Stderr = logFile, logFile
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serve.Process.Kill(); _, _ = serve.Process.Wait() })
	p.say("ceo serve --example pid %d", serve.Process.Pid)
	p.await(t, "the board record", time.Minute, func() bool { return exists(filepath.Join(p.home.State, "board.json")) })

	prompt := "You stand in for the CEO of a scratch Code Goblins home in an automated test. Use the shell only as told. " +
		fmt.Sprintf("Run the shell command  sleep 6; echo step N done  %d times, for N from 1 to %d, strictly one at a time:", queueSteps, queueSteps) + " one shell tool call per step, wait for each to finish before the next, never two calls in one message, and write no text between steps. " +
		"Lines that are not from a tool may reach you while you work. Each is a note that needs no reply and no action: keep running the steps. " +
		"After the last step reply finished and end your turn. Run no other command and change no file."
	// The CEO runs its shell steps under a permission rule of this proof's
	// own, so it starts with no warning only the Overlord accepts.
	arguments := slices.DeleteFunc(p.claudeInputArguments(t), func(argument string) bool { return argument == "--dangerously-skip-permissions" })
	args, err := nativeCEOProgram("claude", append(arguments, "--permission-mode", "acceptEdits", "--allowedTools", "Bash", "--model", "sonnet", prompt)...)
	if err != nil {
		t.Fatal(err)
	}
	ceo, err := host.Launch(p.home.State, []string{p.binary, "host"}, p.env, host.Spec{ID: supervisor.NativeCEOTerminal, Args: args, Dir: p.home.Root, Cols: 120, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	// Found while it runs, so the end below reaches it and no later process
	// that took its pid.
	hostProcess, err := os.FindProcess(ceo.HostPID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = hostProcess.Kill()
		_ = hostProcess.Release()
	})
	p.ceoTerminal = &ceo
	p.say("CEO %s runs in native terminal ceo, host pid %d, harness pid %d", p.ceo, ceo.HostPID, ceo.ChildPID)
	p.settle(t, ceo)
	p.await(t, "the CEO to register", 5*time.Minute, func() bool {
		agent, live := livePrimaryFile(p.home.State)
		return live && agent == p.ceo
	})
	p.await(t, "two steps done", 5*time.Minute, func() bool { return len(stepsDone(p.ceoRecord())) >= 2 })
	screens, _ := harness.NativeScreens(harness.Kind(p.ceo))

	// Every change of the terminal's composer and of each message's state on
	// the board goes on the record, look by look.
	watching, stopWatching := context.WithCancel(context.Background())
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		p.watchQueuedDelivery(watching, ceo)
	}()
	defer func() { stopWatching(); <-watched }()

	// Act: the Overlord sends messages while the CEO works.
	stamp := time.Now().UnixNano() % 1000000
	var messages []queuedProofMessage
	send := func(text string) queuedProofMessage {
		number := len(messages) + 1
		marker := fmt.Sprintf("queue-proof-%d-%d", stamp, number)
		message := queuedProofMessage{id: fmt.Sprintf("queue-proof-message-%d", number), marker: marker, text: strings.ReplaceAll(text, "MARKER", marker)}
		messages = append(messages, message)
		p.post(t, map[string]string{"id": message.id, "kind": "message", "task_id": "", "generation": "", "text": message.text})
		p.say("sent message %d (%d characters): %s", number, len([]rune(message.text)), message.text)
		return message
	}
	switch scenario {
	case "working":
		for _, text := range queuedProofMessages() {
			send(text)
			time.Sleep(2 * time.Second)
		}
	case "stalled":
		// The board holds a message to the CEO for ten seconds before it
		// types it. The harness stops reading a moment before that, so the
		// text is typed into a terminal whose program reads none of it.
		first := send("first note MARKER, typed while the harness read nothing")
		time.Sleep(9 * time.Second)
		resume, err := stallProcess(ceo.ChildPID)
		if err != nil {
			t.Fatal(err)
		}
		stalled := time.Now()
		p.say("the harness, pid %d, reads nothing from now", ceo.ChildPID)
		// stall-accepted: it is how long the harness reads nothing, which the proof means to run out.
		for time.Since(stalled) < queueStall {
			time.Sleep(250 * time.Millisecond)
		}
		if action, found := boardAction(p.snapshot(t), first.id); found {
			p.say("after %s of the harness reading nothing, the first message's action is %s awaiting=%t: %s", time.Since(stalled).Round(time.Millisecond), action.Status, action.Awaiting != nil, action.Message)
		}
		if err := resume(); err != nil {
			t.Fatal(err)
		}
		p.say("the harness reads again")
		time.Sleep(2 * time.Second)
		send("second note MARKER, sent after the harness read again")
	}

	// Assert: each reaches the CEO's conversation, the turn ends, and the
	// composer is empty.
	deadline := time.Duration(queueSteps)*20*time.Second + 5*time.Minute
	p.awaitQuiet(t, "every message in the CEO's conversation and its turn ended", deadline, func() bool {
		screen, err := host.ReadScreen(ceo)
		if err != nil || !screens.IsReady(screen) || screens.IsWorking(screen) || len(stepsDone(p.ceoRecord())) < queueSteps {
			return false
		}
		// The turn ended. A message still not in the conversation a while
		// after that never arrives.
		for wait := time.Now().Add(time.Minute); time.Now().Before(wait); time.Sleep(time.Second) {
			if !slices.ContainsFunc(messages, func(message queuedProofMessage) bool {
				held, err := p.promptsHolding(message.marker)
				return err != nil || held == 0
			}) {
				break
			}
		}
		return true
	})
	// A message its harness handed back or took late settles a moment after
	// the turn ends.
	time.Sleep(20 * time.Second)
	stopWatching()
	<-watched
	screen, _ := host.ReadScreen(ceo)
	p.say("the CEO's screen at the end:\n%s", host.ScreenTail(screen, 40))
	if !screens.ComposerEmpty(screen) {
		t.Errorf("the CEO's composer is not empty at the end; see %s", filepath.Join(p.root, "proof.log"))
	}
	snapshot := p.snapshot(t)
	for i, message := range messages {
		held, err := p.promptsHolding(message.marker)
		action, found := boardAction(snapshot, message.id)
		p.say("message %d: in the CEO's conversation %d times (error %v); board action found=%t status=%s awaiting=%t: %s", i+1, held, err, found, action.Status, action.Awaiting != nil, action.Message)
		if err != nil || held != 1 {
			t.Errorf("message %d is in the CEO's conversation %d times (error %v), want once", i+1, held, err)
		}
		if !found || action.Status != "succeeded" {
			t.Errorf("message %d's board action is %q (found %t), want succeeded: %s", i+1, action.Status, found, action.Message)
		}
	}
	if log, err := os.ReadFile(filepath.Join(p.home.State, "hosts", supervisor.NativeCEOTerminal+".log")); err == nil {
		p.say("the CEO terminal's host log:\n%s", log)
	}
}

// queuedProofMessages are the messages the working proof queues, MARKER
// standing for each one's own mark: plain words, words long enough to go as
// one paste, and words ending as a path, a question and a file mention do.
func queuedProofMessages() []string {
	return []string{
		"first note MARKER, keep going",
		"second note MARKER with a picture path C:\\dev\\example\\composer-files\\picture-1.png",
		"third note MARKER. " + strings.Repeat("Keep the ünïcødé café ✓ notes about the queue in mind. ", 14),
		"fourth note MARKER, is it sent?",
		"fifth note MARKER, see @AGENTS",
	}
}

var (
	ntdll            = windows.NewLazySystemDLL("ntdll.dll")
	ntSuspendProcess = ntdll.NewProc("NtSuspendProcess")
	ntResumeProcess  = ntdll.NewProc("NtResumeProcess")
)

// stallProcess stops every thread of process pid, as a program whose loop is
// held stops reading its input, until the function it returns is called.
func stallProcess(pid int) (func() error, error) {
	const processSuspendResume = 0x0800
	process, err := windows.OpenProcess(processSuspendResume, false, uint32(pid))
	if err != nil {
		return nil, fmt.Errorf("open process %d to stall it: %w", pid, err)
	}
	if status, _, _ := ntSuspendProcess.Call(uintptr(process)); status != 0 {
		_ = windows.CloseHandle(process)
		return nil, fmt.Errorf("stall process %d: status %#x", pid, status)
	}
	return func() error {
		defer windows.CloseHandle(process)
		if status, _, _ := ntResumeProcess.Call(uintptr(process)); status != 0 {
			return fmt.Errorf("let process %d go on: status %#x", pid, status)
		}
		return nil
	}, nil
}

// awaitQuiet is await without the failure: a proof whose wait ends still
// records what it found.
func (p *wakeProof) awaitQuiet(t *testing.T, what string, within time.Duration, done func() bool) {
	deadline := time.Now().Add(within)
	for !done() {
		if time.Now().After(deadline) {
			p.say("gave up waiting for %s after %s", what, within)
			t.Errorf("gave up waiting for %s after %s; see %s", what, within, filepath.Join(p.root, "proof.log"))
			return
		}
		time.Sleep(time.Second)
	}
}

var screenDigits = regexp.MustCompile(`\d+`)

// watchQueuedDelivery records the end of the CEO terminal's screen each time
// it changes, the turn's own clock and spinner aside, and each message's
// state on the board each time that changes, until ctx ends.
func (p *wakeProof) watchQueuedDelivery(ctx context.Context, ceo host.Record) {
	screens, _ := harness.NativeScreens(harness.Kind(p.ceo))
	lastScreen, lastActions := "", ""
	for ctx.Err() == nil {
		if screen, err := host.ReadScreen(ceo); err == nil {
			tail := host.ScreenTail(screen, 12)
			var shape []string
			for _, row := range strings.Split(tail, "\n") {
				if !screens.IsWorking([]string{strings.TrimSpace(row)}) {
					shape = append(shape, screenDigits.ReplaceAllString(row, "#"))
				}
			}
			if joined := strings.Join(shape, "\n"); joined != lastScreen {
				lastScreen = joined
				p.say("screen:\n%s", tail)
			}
		}
		if snapshot, err := p.snapshotQuiet(); err == nil {
			var lines []string
			for _, action := range snapshot.Actions {
				if action.Kind == "message" {
					lines = append(lines, fmt.Sprintf("  %s %s awaiting=%t can_send_now=%t why=%q: %s", action.ID, action.Status, action.Awaiting != nil, action.CanSendNow, action.SendNowWhy, action.Message))
				}
			}
			if joined := strings.Join(lines, "\n"); joined != lastActions {
				lastActions = joined
				p.say("board messages:\n%s", joined)
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(200 * time.Millisecond): // stall-accepted: it spaces the looks at the screen and the board, and times nothing.
		}
	}
}

// snapshotQuiet reads the scratch board's snapshot and fails no test.
func (p *wakeProof) snapshotQuiet() (supervisor.Snapshot, error) {
	var snapshot supervisor.Snapshot
	response, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/snapshot", p.port))
	if err != nil {
		return snapshot, err
	}
	defer response.Body.Close()
	return snapshot, json.NewDecoder(response.Body).Decode(&snapshot)
}

// ceoRecord reads, oldest file first, the record the proof's Claude Code CEO
// keeps of its conversation in the scratch home.
func (p *wakeProof) ceoRecord() []recordEntry {
	profile, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	folder := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(p.home.Root, "-")
	files, _ := filepath.Glob(filepath.Join(profile, ".claude", "projects", folder+"*", "*.jsonl"))
	sort.Slice(files, func(i, j int) bool {
		one, errOne := os.Stat(files[i])
		other, errOther := os.Stat(files[j])
		return errOne == nil && errOther == nil && one.ModTime().Before(other.ModTime())
	})
	var entries []recordEntry
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		lines := bufio.NewScanner(bytes.NewReader(data))
		lines.Buffer(make([]byte, 1<<20), 64<<20)
		for lines.Scan() {
			entries = append(entries, readRecordEntry("claude", lines.Bytes())...)
		}
	}
	return slices.Clip(entries)
}
