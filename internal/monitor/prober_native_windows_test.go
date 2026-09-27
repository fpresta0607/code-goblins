package monitor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// nativeMeta is the record of a native task, which names no Herdr terminal.
func nativeMeta(id, harness string) state.TaskMeta {
	return state.TaskMeta{ID: id, Worktree: `C:\work\` + id, Harness: harness, Backend: "native"}
}

// recordNativeHost writes the record of a host for terminal id whose pipe
// nothing serves, as a host that was killed leaves behind.
func recordNativeHost(t *testing.T, stateDir, id string) host.Record {
	t.Helper()
	var name [16]byte
	if _, err := rand.Read(name[:]); err != nil {
		t.Fatal(err)
	}
	record := host.Record{ID: id, Pipe: `\\.\pipe\code-goblins-host-` + hex.EncodeToString(name[:]), Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: os.Getpid(), Started: time.Now().UTC()}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", id+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return record
}

// screenOf reads the same rows for every host.
func screenOf(rows ...string) func(host.Record) ([]string, error) {
	return func(host.Record) ([]string, error) { return rows, nil }
}

// A native terminal's screen says what its harness is doing, read with the
// harness's own markers: a turn in progress, a dialog waiting on a person, or
// the composer waiting for input.
func TestANativeProberReadsTheHarnessFromItsScreen(t *testing.T) {
	for _, test := range []struct {
		name, harness string
		screen        []string
		status        string
		busy          herdr.BusyState
		ready         bool
	}{
		{"a claude turn in progress", "claude", []string{"✽ Reticulating… (12s · esc to interrupt)", "", "⏵⏵ bypass permissions on (shift+tab to cycle)"}, herdr.AgentWorking, herdr.BusyWorking, false},
		{"a claude composer waiting", "claude", []string{"> ", "⏵⏵ bypass permissions on (shift+tab to cycle)"}, herdr.AgentDone, herdr.BusyIdle, true},
		{"a claude trust dialog", "claude", []string{"Is this a project you created or one you trust?", "❯ 1. No, exit", "  2. Yes, I trust this folder"}, herdr.AgentBlocked, herdr.BusyIdle, false},
		{"a codex turn in progress", "codex", []string{"• Working (5s • esc to interrupt)", "› ", "100% context left"}, herdr.AgentWorking, herdr.BusyWorking, false},
		{"a screen with no marker", "claude", []string{"Loading..."}, herdr.AgentUnknown, herdr.BusyUnknown, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			meta := nativeMeta("g1", test.harness)
			recordNativeHost(t, stateDir, "g1")

			sample, err := NativeProber{StateDir: stateDir, ReadScreen: screenOf(test.screen...)}.Inspect(context.Background(), meta)

			if err != nil {
				t.Fatal(err)
			}
			if sample.Verdict != ProbePresent || sample.Agent != herdr.AgentAlive || sample.TabLabel != "gb-g1" || sample.Harness != test.harness {
				t.Errorf("sample = %+v, want a present live harness in terminal gb-g1", sample)
			}
			if sample.Status != test.status || sample.Busy != test.busy || sample.InteractiveReady != test.ready {
				t.Errorf("status %q busy %q ready %v, want %q %q %v", sample.Status, sample.Busy, sample.InteractiveReady, test.status, test.busy, test.ready)
			}
			if !strings.Contains(string(sample.Capture), test.screen[0]) {
				t.Errorf("capture %q leaves out the screen", sample.Capture)
			}
			if !validSample(meta, sample) {
				t.Error("the monitor refuses the sample as a different endpoint")
			}
		})
	}
}

// A native terminal with no host, or whose host no longer answers after a
// failed read, is missing; a harness whose screens the monitor cannot read is
// unknown rather than guessed.
func TestANativeProberSaysWhenItCannotSeeTheTerminal(t *testing.T) {
	stateDir := t.TempDir()
	unreadable := func(host.Record) ([]string, error) { return nil, errors.New("the console is gone") }

	noHost, _ := NativeProber{StateDir: stateDir, ReadScreen: screenOf("> ")}.Inspect(context.Background(), nativeMeta("g1", "claude"))
	recordNativeHost(t, stateDir, "g2")
	deadHost, _ := NativeProber{StateDir: stateDir, ReadScreen: unreadable}.Inspect(context.Background(), nativeMeta("g2", "claude"))
	recordNativeHost(t, stateDir, "g3")
	kimi, _ := NativeProber{StateDir: stateDir, ReadScreen: screenOf("> ")}.Inspect(context.Background(), nativeMeta("g3", "kimi"))

	if noHost.Verdict != ProbeMissing || !strings.Contains(noHost.Detail, "no running host") {
		t.Errorf("no host = %+v, want missing", noHost)
	}
	if deadHost.Verdict != ProbeMissing || !strings.Contains(deadHost.Detail, "does not answer") {
		t.Errorf("a host that does not answer = %+v, want missing", deadHost)
	}
	if kimi.Verdict != ProbeUnknown || !strings.Contains(kimi.Detail, "cannot read") {
		t.Errorf("an unreadable harness = %+v, want unknown", kimi)
	}
}

// The monitor supervises native goblins too: one whose turn ended is woken
// on, where before every native task was passed over.
func TestScanWakesWhenANativeGoblinsTurnEnds(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	meta := nativeMeta("g1", "claude")
	writeTask(t, stateDir, meta)
	recordNativeHost(t, stateDir, "g1")
	probe := BackendProber{Herdr: &fakeProber{}, Native: NativeProber{StateDir: stateDir, ReadScreen: screenOf("> ", "⏵⏵ bypass permissions on (shift+tab to cycle)")}}

	result, err := testService(stateDir, probe, &now).Scan(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if len(result.Observations) != 1 || result.Observations[0].TaskID != "g1" {
		t.Fatalf("observations = %+v, want the native goblin observed", result.Observations)
	}
	if result.Event == nil || result.Observations[0].Reason != AwaitingAnswer {
		t.Errorf("scan = %+v, want an awaiting-input wake for the native goblin", result)
	}
}

// Each task is inspected by the prober of the backend it runs in, and one
// scan starts the Herdr prober's cycle once.
func TestABackendProberSendsEachTaskToItsBackend(t *testing.T) {
	herdrProbe, nativeProbe := &cycleProber{}, &fakeProber{}
	probe := BackendProber{Herdr: herdrProbe, Native: nativeProbe}

	probe.BeginScan(context.Background())
	_, _ = probe.Inspect(context.Background(), metaFor("h1"))
	_, _ = probe.Inspect(context.Background(), nativeMeta("n1", "claude"))

	if herdrProbe.cycles != 1 || strings.Join(herdrProbe.calls, ",") != "h1" || strings.Join(nativeProbe.calls, ",") != "n1" {
		t.Errorf("herdr cycles %d calls %q, native calls %q; want one cycle, h1 to Herdr and n1 native", herdrProbe.cycles, herdrProbe.calls, nativeProbe.calls)
	}
}

type cycleProber struct {
	fakeProber
	cycles int
}

func (c *cycleProber) BeginScan(context.Context) { c.cycles++ }

// A native goblin's own processes are those its terminal's program started:
// a native task has no pane for Herdr to name its harness.
func TestHostProgressReadsANativeHarnessFromItsTerminal(t *testing.T) {
	stateDir := t.TempDir()
	meta := nativeMeta("g1", "claude")

	_, missing := HostProgress{StateDir: stateDir}.InspectProgress(context.Background(), meta, EndpointSample{Harness: "claude"})
	recordNativeHost(t, stateDir, "g1")
	progress, err := HostProgress{StateDir: stateDir}.InspectProgress(context.Background(), meta, EndpointSample{Harness: "claude"})

	if missing == nil {
		t.Error("a native task with no host gave progress evidence")
	}
	if err != nil {
		t.Fatalf("InspectProgress: %v", err)
	}
	if len(progress.Jobs) != 0 {
		t.Errorf("jobs = %q, want none: this test process started nothing after its launch", progress.Jobs)
	}
}
