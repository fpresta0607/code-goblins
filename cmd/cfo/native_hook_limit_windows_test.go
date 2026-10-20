package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// heldStart is how long the test holds a native hook before its code runs,
// as a busy machine held cfo.exe's start: longer than the 3 seconds the
// hooks were allowed, and than the 5.7 seconds the slowest start that still
// finished took on the fleet (issue #1211).
const heldStart = 6 * time.Second

var (
	setProcessAffinityMask = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetProcessAffinityMask")
	setThreadAffinityMask  = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadAffinityMask")
)

// A native hook that the machine holds at its start, then runs beside busy
// threads on its one processor, still records its event by the limit its
// harness holds it to, where the harness ends whatever still runs.
func TestANativeHookHeldAtItsStartRecordsItsEventByItsLimit(t *testing.T) {
	for _, harnessName := range []string{"claude", "codex"} {
		t.Run(harnessName, func(t *testing.T) {
			t.Parallel()
			// Arrange
			root := t.TempDir()
			stateDir := filepath.Join(root, "state")
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": "held-" + harnessName, "cwd": root, "prompt": "a prompt"})
			if err != nil {
				t.Fatal(err)
			}
			hook := execx.Command(self, "native-hook", harnessName, "--home", root, "--state", stateDir)
			// The CFO's own hook: no goblin's or gate's markers, and hidden as
			// Claude Code starts a hook.
			for _, variable := range os.Environ() {
				if !strings.HasPrefix(strings.ToUpper(variable), "CFO_") {
					hook.Env = append(hook.Env, variable)
				}
			}
			hook.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW | windows.CREATE_SUSPENDED
			hook.Stdin = bytes.NewReader(payload)
			var output bytes.Buffer
			hook.Stdout, hook.Stderr = &output, &output

			// Act
			began := time.Now()
			if err := hook.Start(); err != nil {
				t.Fatal(err)
			}
			// The harness ends the hook at its limit from its start, held or not.
			limit := time.NewTimer(nativehook.TimeoutSeconds * time.Second)
			defer limit.Stop()
			exited := make(chan error, 1)
			go func() { exited <- hook.Wait() }()
			if err := pinProcessToOneProcessor(hook.Process.Pid); err != nil {
				_ = hook.Process.Kill()
				t.Fatal(err)
			}
			// stall-accepted: the hold waits for nothing, it is the start a busy
			// machine makes the hook wait out, and a pause in it only lengthens it.
			held := time.NewTimer(heldStart)
			defer held.Stop()
			isEnded := false
		wait:
			for {
				select {
				case <-held.C:
					stopBusy := spinBesideOnOneProcessor(2)
					defer stopBusy()
					if err := proc.Resume(hook.Process.Pid); err != nil {
						_ = hook.Process.Kill()
						t.Fatal(err)
					}
				case <-limit.C:
					isEnded = true
					_ = hook.Process.Kill()
					<-exited
					break wait
				case <-exited:
					break wait
				}
			}
			took := time.Since(began)

			// Assert
			entries, err := os.ReadDir(nativehook.SpoolDir(stateDir))
			if err != nil {
				t.Fatalf("no spool after %s (ended at the limit: %v, output %q): %v", took, isEnded, output.String(), err)
			}
			var events []nativehook.Event
			for _, entry := range entries {
				data, err := os.ReadFile(filepath.Join(nativehook.SpoolDir(stateDir), entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				var event nativehook.Event
				if err := json.Unmarshal(data, &event); err != nil {
					t.Fatalf("spool entry %s is no event: %v", entry.Name(), err)
				}
				events = append(events, event)
			}
			if len(events) != 1 || !events[0].Prompt || events[0].SessionID != "held-"+harnessName || events[0].Role != "cfo" {
				t.Fatalf("spool holds %+v after %s (ended at the limit: %v, output %q), want the one prompt event of session held-%s", events, took, isEnded, output.String(), harnessName)
			}
			if isEnded {
				t.Errorf("the hook still ran at its %d-second limit, %s after it was started held for %s", nativehook.TimeoutSeconds, took, heldStart)
			}
		})
	}
}

// pinProcessToOneProcessor keeps process pid to the first processor, which
// spinBesideOnOneProcessor keeps busy.
func pinProcessToOneProcessor(pid int) error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_INFORMATION|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if ok, _, err := setProcessAffinityMask.Call(uintptr(process), 1); ok == 0 {
		return err
	}
	return nil
}

// spinBesideOnOneProcessor keeps threads busy threads spinning on the first
// processor until the function it returns is called. Each spins on a thread
// of its own that it never unlocks, so the thread, pinned, ends with it.
func spinBesideOnOneProcessor(threads int) (stop func()) {
	done := make(chan struct{})
	for range threads {
		go func() {
			runtime.LockOSThread()
			_, _, _ = setThreadAffinityMask.Call(uintptr(windows.CurrentThread()), 1)
			for {
				select {
				case <-done:
					return
				default:
				}
				for i := 0; i < 1e5; i++ {
				}
			}
		}()
	}
	return func() { close(done) }
}
