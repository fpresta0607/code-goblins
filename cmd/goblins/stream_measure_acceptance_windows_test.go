package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// measuredSends is how many messages the measurement sends each goblin once
// it has taken its instruction.
const measuredSends = 3

// TestAStreamGoblinIsMeasuredBesideATerminalGoblin measures a Claude Code
// goblin on stream-json pipes beside one in a terminal on the same task
// (#2374), each spawned in a scratch home of its own under `goblins serve
// --example`, whose CEO is a stub, and the stream one only where that home's
// config/fleet.json turns claude_goblins_on_stream on. Each runs six-second
// shell steps as its instruction and then answers measuredSends messages
// from goblins send, on Haiku at low effort: its start time, from the spawn
// to its instruction taken; its delivery failures, sends refused or never
// answered; its false stalls, stall wakes the monitor raised while its turn
// ran; and the working set of its host and every process under it, sampled
// through the run. The stream goblin's rate-limit events are counted
// from its session's lines; a terminal shows none. Its turn phases are each
// phase its hooks gave the session the supervisor keeps, from which the board
// draws a turn as running and offers Stop: each must run and the last end.
// The figures go to measure.json in each goblin's results folder.
//
// It runs real harnesses on the Overlord's subscription, capped at
// 1+measuredSends model turns a goblin, so it runs only when asked:
//
//	GOBLINS_STREAM_REAL=1 GOBLINS_STREAM_BINARY=<goblins.exe built from this tree>
//	GOBLINS_STREAM_RESULTS=<a directory for the record>
func TestAStreamGoblinIsMeasuredBesideATerminalGoblin(t *testing.T) {
	if os.Getenv("GOBLINS_STREAM_REAL") != "1" {
		t.Skip("set GOBLINS_STREAM_REAL=1 with GOBLINS_STREAM_BINARY and GOBLINS_STREAM_RESULTS to measure a real stream goblin beside a terminal goblin")
	}
	binary, results := os.Getenv("GOBLINS_STREAM_BINARY"), os.Getenv("GOBLINS_STREAM_RESULTS")
	if binary == "" || results == "" {
		t.Fatal("GOBLINS_STREAM_BINARY and GOBLINS_STREAM_RESULTS are both required")
	}
	if root, state := home.Inherited(); root != "" || state != "" {
		t.Fatalf("this process inherited the fleet home %s (state %s); unset GOBLINS_HOME and GOBLINS_STATE_OVERRIDE first", root, state)
	}
	for i, mode := range []string{"terminal", "stream"} {
		t.Run(mode+"-goblin", func(t *testing.T) {
			root := filepath.Join(results, mode+"-goblin")
			measureGoblin(t, &wakeProof{binary: binary, root: root, project: filepath.Join(root, "project"), goblin: "claude", port: 4430 + i}, mode == "stream")
		})
	}
}

// goblinMeasure is what one goblin's measurement found.
type goblinMeasure struct {
	IsStream         bool     `json:"stream"`
	StartSeconds     float64  `json:"start_seconds"`
	Sends            int      `json:"sends"`
	DeliveryFailures []string `json:"delivery_failures"`
	FalseStalls      []string `json:"false_stalls"`
	MemorySamples    int      `json:"memory_samples"`
	PeakMemoryBytes  uint64   `json:"peak_memory_bytes"`
	MeanMemoryBytes  uint64   `json:"mean_memory_bytes"`
	RateLimitEvents  []string `json:"rate_limit_events"`
	TurnPhases       []string `json:"turn_phases"`
}

func measureGoblin(t *testing.T, p *wakeProof, isStream bool) {
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
	writeProofFile(t, filepath.Join(p.home.Root, "AGENTS.md"), "# Scratch CEO home for the stream measurement\n")
	writeProofFile(t, filepath.Join(p.home.Root, home.InstalledMarker), "")
	writeProofFile(t, filepath.Join(p.home.Root, "config", "fleet.json"), fmt.Sprintf(`{"claude_goblins_on_stream": %t}`+"\n", isStream))
	p.say("scratch home %s; stream %t", p.home.Root, isStream)
	p.setUpProject(t)
	p.env = p.environment()
	// An example board's home lies under the temporary folder serve runs
	// with, this test's own, and the home gets an empty projects root of its
	// own, since its goblin is spawned by its project's path.
	projects := filepath.Join(p.root, "projects")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, entry := range p.env {
		name, _, _ := strings.Cut(entry, "=")
		switch {
		case strings.EqualFold(name, "TEMP"), strings.EqualFold(name, "TMP"):
			p.env[i] = name + "=" + os.TempDir()
		case strings.EqualFold(name, install.ProjectsRootVariable):
			p.env[i] = name + "=" + projects
		}
	}
	p.seen = map[int]wake.Record{}
	watching, stopWatching := context.WithCancel(context.Background())
	watched := make(chan struct{})
	go func() { defer close(watched); p.watchQueue(watching) }()
	defer func() { stopWatching(); <-watched }()

	serve := exec.Command(p.binary, "serve", "--example", "--listen", fmt.Sprintf("127.0.0.1:%d", p.port))
	serve.Env = p.env
	serve.Stdout, serve.Stderr = logFile, logFile
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serve.Process.Kill(); _, _ = serve.Process.Wait() })
	p.await(t, "the board record", time.Minute, func() bool { return exists(filepath.Join(p.home.State, "board.json")) })

	goblin := "measure-" + map[bool]string{true: "stream", false: "terminal"}[isStream]
	defer p.endGoblin(goblin)
	task := "This is a measurement fixture in an automated test. Run the shell command  sleep 6; echo step N done  4 times, for N from 1 to 4, strictly one at a time, one shell tool call per step, waiting for each to finish before the next, and write no text between steps. Then reply with exactly the word ready and end your turn. " +
		"Lines starting with CEO: reach you later: do exactly what each says. Change no file and run no other command."
	measure := goblinMeasure{IsStream: isStream, Sends: measuredSends}
	started := time.Now()
	p.ceoCommand(t, "spawn", goblin, "--project", p.project, "--brief", p.brief(t, goblin, task), "--mode", "local-only", "--harness", "claude", "--model", "haiku", "--effort", "low")
	measure.StartSeconds = time.Since(started).Seconds()
	record, err := host.ReadRecord(p.home.State, goblin)
	if err != nil || record.Stream != isStream {
		t.Fatalf("%s's host = %+v, %v; want stream %t", goblin, record, err, isStream)
	}
	meta, err := state.ReadTaskMeta(p.home.State, goblin)
	if err != nil {
		t.Fatal(err)
	}

	// The stream session's rate-limit events, from the lines its host keeps
	// and each after.
	rateLimits := make(chan []string, 1)
	var viewer *host.StreamViewer
	if isStream {
		viewer, err = host.WatchStream(record)
		if err != nil {
			t.Fatal(err)
		}
		defer viewer.Close()
		go func() {
			var events []string
			for {
				event, err := viewer.Next()
				if err != nil || event.IsExited {
					rateLimits <- events
					return
				}
				var line struct {
					Type string          `json:"type"`
					Info json.RawMessage `json:"rate_limit_info"`
				}
				if json.Unmarshal(event.Line.Line, &line) == nil && line.Type == "rate_limit_event" {
					events = append(events, string(line.Info))
				}
			}
		}()
	}

	// The memory of its host's process tree, sampled every 15 seconds.
	sampling, stopSampling := context.WithCancel(context.Background())
	sampled := make(chan []uint64, 1)
	go func() {
		var samples []uint64
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			if size, err := treeWorkingSet(record.HostPID); err == nil {
				samples = append(samples, size)
			} else {
				p.say("memory sample: %v", err)
			}
			select {
			case <-sampling.Done():
				sampled <- samples
				return
			case <-tick.C:
			}
		}
	}()

	// samplePhase records the phase of the goblin's session in the
	// supervisor's own record whenever it changes.
	samplePhase := func() string {
		var db struct {
			Sessions     map[string]struct{ Phase string } `json:"sessions"`
			TaskSessions map[string]string                 `json:"task_sessions"`
		}
		if data, err := os.ReadFile(filepath.Join(p.home.State, ".supervisor.json")); err != nil || json.Unmarshal(data, &db) != nil {
			return ""
		}
		phase := db.Sessions[db.TaskSessions[goblin]].Phase
		if phase != "" && (len(measure.TurnPhases) == 0 || measure.TurnPhases[len(measure.TurnPhases)-1] != phase) {
			measure.TurnPhases = append(measure.TurnPhases, phase)
		}
		return phase
	}
	awaitReply := func(marker string) bool {
		deadline := time.Now().Add(8 * time.Minute)
		for time.Now().Before(deadline) {
			samplePhase()
			entries := p.conversation(meta)
			if slices.ContainsFunc(entries, func(e recordEntry) bool { return e.kind == "reply" && strings.Contains(e.text, marker) }) {
				return true
			}
			time.Sleep(2 * time.Second)
		}
		p.say("no reply holding %s; the record:\n%s", marker, describeRecord(p.conversation(meta)))
		return false
	}
	if !awaitReply("ready") {
		t.Fatalf("%s never replied ready to its instruction", goblin)
	}
	for i := 1; i <= measuredSends; i++ {
		marker := "MEASURED" + strconv.Itoa(i) + strconv.FormatInt(time.Now().UnixNano()%100000, 10)
		command := exec.Command(p.binary, "send", goblin, "Reply with exactly the word "+marker+" and end your turn.")
		command.Env = p.commandEnv("send")
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		sent := time.Now()
		err := command.Run()
		p.say("goblins send %d took %s, exit %v\nstdout: %s\nstderr: %s", i, time.Since(sent).Round(time.Millisecond), err, stdout.String(), stderr.String())
		switch {
		case err != nil:
			measure.DeliveryFailures = append(measure.DeliveryFailures, fmt.Sprintf("send %d refused: %v %s", i, err, strings.TrimSpace(stderr.String())))
		case !awaitReply(marker):
			measure.DeliveryFailures = append(measure.DeliveryFailures, fmt.Sprintf("send %d never answered", i))
		}
	}
	// The last turn's end reaches the supervisor from the Stop hook, after
	// the reply it ended with.
	for settle := time.Now().Add(time.Minute); samplePhase() != "settled" && time.Now().Before(settle); {
		time.Sleep(2 * time.Second)
	}

	stopSampling()
	samples := <-sampled
	measure.MemorySamples = len(samples)
	var total uint64
	for _, sample := range samples {
		total += sample
		measure.PeakMemoryBytes = max(measure.PeakMemoryBytes, sample)
	}
	if len(samples) > 0 {
		measure.MeanMemoryBytes = total / uint64(len(samples))
	}
	// A stall the monitor raised while the goblin worked: every turn it ran
	// answered, so any is false.
	for _, record := range p.records() {
		if record.Key != goblin {
			continue
		}
		p.say("wake %d %s %s: %s", record.Seq, record.Kind, record.Key, record.Detail)
		if record.Kind == "stale" && (strings.Contains(record.Detail, "no liveness signal") || strings.HasPrefix(record.Detail, "busy_turn_over_age")) {
			measure.FalseStalls = append(measure.FalseStalls, record.Detail)
		}
	}
	if viewer != nil {
		_ = viewer.Close()
		measure.RateLimitEvents = <-rateLimits
	}

	// Assert
	data, _ := json.MarshalIndent(measure, "", "  ")
	writeProofFile(t, filepath.Join(p.root, "measure.json"), string(data)+"\n")
	p.say("measured: %s", data)
	if len(measure.DeliveryFailures) > 0 {
		t.Errorf("%s's delivery failures: %q", goblin, measure.DeliveryFailures)
	}
	if len(measure.FalseStalls) > 0 {
		t.Errorf("%s's false stalls: %q", goblin, measure.FalseStalls)
	}
	if !slices.Contains(measure.TurnPhases, "active") || measure.TurnPhases[len(measure.TurnPhases)-1] != "settled" {
		t.Errorf("%s's turn phases = %q; want a turn active and the last settled", goblin, measure.TurnPhases)
	}
}

// treeWorkingSet is the working set of process pid and every process under
// it, as Windows reports each.
func treeWorkingSet(pid int) (uint64, error) {
	script := fmt.Sprintf(`$all = Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,WorkingSetSize
$tree = @(%d); $grew = $true
while ($grew) { $grew = $false; foreach ($p in $all) { if ($tree -contains $p.ParentProcessId -and -not ($tree -contains $p.ProcessId)) { $tree += $p.ProcessId; $grew = $true } } }
[uint64](($all | Where-Object { $tree -contains $_.ProcessId } | Measure-Object -Property WorkingSetSize -Sum).Sum)`, pid)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
}
