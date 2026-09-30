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
	record := host.Record{ID: id, Pipe: `\\.\pipe\code-goblins-host-` + hex.EncodeToString(name[:]), Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: os.Getppid(), Started: time.Now().UTC()}
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
		{"a codex turn that ended saying Working", "codex", []string{"• Working tree is clean and all tests pass.", "› ", "100% context left"}, herdr.AgentDone, herdr.BusyIdle, true},
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

// On 2026-09-28 the monitor woke the CFO because a live, working native
// goblin's screen read failed once ("the screen reader failed (exit status
// 1)"). One failed read is read again before the terminal is judged
// unreadable or gone.
func TestANativeProberReadsAgainWhenOneScreenReadFails(t *testing.T) {
	stateDir := t.TempDir()
	recordNativeHost(t, stateDir, "g1")
	reads := 0
	read := func(host.Record) ([]string, error) {
		reads++
		if reads == 1 {
			return nil, errors.New("the screen reader failed (exit status 1)")
		}
		return []string{"✽ Churning… (29m 30s · esc to interrupt)", "", "⏵⏵ bypass permissions on (shift+tab to cycle)"}, nil
	}

	sample, err := NativeProber{StateDir: stateDir, ReadScreen: read}.Inspect(context.Background(), nativeMeta("g1", "claude"))

	if err != nil {
		t.Fatal(err)
	}
	if sample.Verdict != ProbePresent || sample.Busy != herdr.BusyWorking {
		t.Errorf("sample = %+v after %d reads, want the working goblin read on the second", sample, reads)
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

	if noHost.Verdict != ProbeMissing || !strings.Contains(noHost.Detail, "no running host") || !strings.Contains(noHost.Detail, "cfo switch g1") {
		t.Errorf("no host = %+v, want missing", noHost)
	}
	if deadHost.Verdict != ProbeMissing || !strings.Contains(deadHost.Detail, "does not answer") {
		t.Errorf("a host that does not answer = %+v, want missing", deadHost)
	}
	if kimi.Verdict != ProbeUnknown || !strings.Contains(kimi.Detail, "cannot read") {
		t.Errorf("an unreadable harness = %+v, want unknown", kimi)
	}
}

// The monitor supervises native goblins too, where before every native task
// was passed over. Within the launch budget a goblin whose host has not
// started, or whose harness shows its trust dialog or composer before the
// brief is submitted, is still launching and stays quiet. Past the budget a
// terminal with no host wakes as missing and one whose turn ended wakes as
// waiting on input.
func TestScanSupervisesANativeGoblinOnceItsLaunchIsOver(t *testing.T) {
	composer := []string{"> ", "⏵⏵ bypass permissions on (shift+tab to cycle)"}
	dialog := []string{"Is this a project you created or one you trust?", "❯ 1. No, exit", "  2. Yes, I trust this folder"}
	for _, test := range []struct {
		name       string
		age        time.Duration
		hasHost    bool
		screen     []string
		reason     Reason
		shouldWake bool
	}{
		{"no host within the launch budget", time.Minute, false, nil, None, false},
		{"a trust dialog within the launch budget", time.Minute, true, dialog, None, false},
		{"a ready composer within the launch budget", time.Minute, true, composer, None, false},
		{"no host after the launch budget", 6 * time.Minute, false, nil, EndpointMissing, true},
		{"a ready composer after the launch budget", 6 * time.Minute, true, composer, AwaitingAnswer, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
			meta := nativeMeta("g1", "claude")
			writeTask(t, stateDir, meta)
			spawned := now.Add(-test.age)
			if err := os.Chtimes(filepath.Join(stateDir, "g1.meta"), spawned, spawned); err != nil {
				t.Fatal(err)
			}
			if test.hasHost {
				recordNativeHost(t, stateDir, "g1")
			}
			probe := BackendProber{Herdr: &fakeProber{}, Native: NativeProber{StateDir: stateDir, ReadScreen: screenOf(test.screen...)}}

			result, err := testService(stateDir, probe, &now).Scan(context.Background())

			if err != nil {
				t.Fatal(err)
			}
			if len(result.Observations) != 1 || result.Observations[0].TaskID != "g1" {
				t.Fatalf("observations = %+v, want the native goblin observed", result.Observations)
			}
			observation := result.Observations[0]
			if observation.Reason != test.reason || (result.Event != nil) != test.shouldWake {
				t.Errorf("reason %q event %+v, want reason %q and wake %v", observation.Reason, result.Event, test.reason, test.shouldWake)
			}
			if (observation.Health == HealthLaunching) == test.shouldWake {
				t.Errorf("health %q, want launching only while the goblin stays quiet", observation.Health)
			}
		})
	}
}

// The launch budget quiets only a goblin never seen alive: one seen working
// whose turn then ends or whose host then dies wakes at once, as a Herdr
// goblin does, rather than reading as launching until the budget runs out.
func TestScanWakesANativeGoblinSeenWorkingWithinItsLaunchBudget(t *testing.T) {
	working := []string{"✽ Reticulating… (12s · esc to interrupt)", "", "⏵⏵ bypass permissions on (shift+tab to cycle)"}
	composer := []string{"> ", "⏵⏵ bypass permissions on (shift+tab to cycle)"}
	for _, test := range []struct {
		name           string
		shouldKeepHost bool
		reason         Reason
	}{
		{"its turn ends", true, AwaitingAnswer},
		{"its host dies", false, EndpointMissing},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
			meta := nativeMeta("g1", "claude")
			writeTask(t, stateDir, meta)
			if err := os.Chtimes(filepath.Join(stateDir, "g1.meta"), now, now); err != nil {
				t.Fatal(err)
			}
			recordNativeHost(t, stateDir, "g1")
			screen := working
			readScreen := func(host.Record) ([]string, error) { return screen, nil }
			service := testService(stateDir, BackendProber{Herdr: &fakeProber{}, Native: NativeProber{StateDir: stateDir, ReadScreen: readScreen}}, &now)
			now = now.Add(time.Minute)
			if seen, err := service.Scan(context.Background()); err != nil || seen.Observations[0].Health == HealthLaunching {
				t.Fatalf("working scan = %+v, %v; want the goblin seen alive", seen, err)
			}
			screen = composer
			if !test.shouldKeepHost {
				if err := os.Remove(filepath.Join(stateDir, "hosts", "g1.json")); err != nil {
					t.Fatal(err)
				}
			}
			now = now.Add(time.Minute)

			result, err := service.Scan(context.Background())

			if err != nil {
				t.Fatal(err)
			}
			observation := result.Observations[0]
			if observation.Health == HealthLaunching || observation.Reason != test.reason || result.Event == nil {
				t.Errorf("health %q reason %q event %+v, want a %q wake", observation.Health, observation.Reason, result.Event, test.reason)
			}
		})
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

// A native goblin's own processes are those its terminal's program started,
// not its host's: a native task has no pane for Herdr to name its harness.
func TestHostProgressReadsANativeHarnessFromItsTerminal(t *testing.T) {
	stateDir := t.TempDir()
	meta := nativeMeta("g1", "claude")
	prober := HostProgress{StateDir: stateDir}

	_, missing := prober.InspectProgress(context.Background(), meta, EndpointSample{Harness: "claude"})
	record := recordNativeHost(t, stateDir, "g1")
	harnessPID, _, err := prober.harness(context.Background(), meta, EndpointSample{Harness: "claude"})

	if missing == nil {
		t.Error("a native task with no host gave progress evidence")
	}
	if err != nil || harnessPID != record.ChildPID {
		t.Errorf("harness pid = %d, %v; want the terminal's program, pid %d, not its host, pid %d", harnessPID, err, record.ChildPID, record.HostPID)
	}
}
