package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// writeDrainQueueFixture writes the exact live-home queue the drain output
// block in the task brief was written against: ack floor pre-seeded to 1 so
// the lowest pending sequence is 2, three records at seqs 2/5/7 (the gaps
// standing for records already acked away). It does not touch the episode
// marker, so callers decide separately whether an episode is published.
func writeDrainQueueFixture(t *testing.T, state string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(state, ".wake-ack"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	records := []wake.Record{
		{Seq: 2, Kind: "signal", Key: "g1.status", Detail: "signal:g1.status"},
		{Seq: 5, Kind: "stale", Key: "w1", Detail: "stale: w1 (idle 300s)"},
		{Seq: 7, Kind: "heartbeat", Key: "heartbeat", Detail: "heartbeat"},
	}
	var b []byte
	for _, r := range records {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	if err := os.WriteFile(filepath.Join(state, ".wake-queue"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildDrainFixture builds writeDrainQueueFixture's queue plus four episode
// publishes, so the generation is 4.
func buildDrainFixture(t *testing.T) home.Home {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	writeDrainQueueFixture(t, state)
	for i := 0; i < 4; i++ {
		if _, err := wake.PublishEpisode(state); err != nil {
			t.Fatal(err)
		}
	}
	return home.Home{Root: root, State: state, Data: filepath.Join(root, "data")}
}

func assertLines(t *testing.T, stdout string, want []string) {
	t.Helper()
	got := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d\nfull output:\n%s", len(got), len(want), stdout)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRunDrainRendersExactBlock(t *testing.T) {
	h := buildDrainFixture(t)
	var stdout, stderr bytes.Buffer
	if exit := runDrain(h, nil, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	assertLines(t, stdout.String(), []string{
		"WAKE QUEUE: 3 pending",
		"  2  signal  g1.status: signal:g1.status",
		"  5  stale   w1: stale: w1 (idle 300s)",
		"  7  heartbeat  heartbeat",
		"RECOVERY EPISODE: pending, generation 4",
		"WAKE_ACK_REQUIRED: cfo drain --ack-through 7 --recovery-generation 4",
	})
}

func TestRunDrainAckThroughAndRecoveryGeneration(t *testing.T) {
	h := buildDrainFixture(t)
	var stdout, stderr bytes.Buffer
	if exit := runDrain(h, []string{"--ack-through", "7", "--recovery-generation", "4"}, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %+v, want empty", pending)
	}
	ep, err := wake.ReadEpisode(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Pending || ep.Gen != 4 {
		t.Errorf("episode = %+v, want acked generation 4", ep)
	}
}

func TestRunDrainStaleGenerationMismatch(t *testing.T) {
	h := buildDrainFixture(t)
	var stdout, stderr bytes.Buffer
	if exit := runDrain(h, []string{"--ack-through", "7", "--recovery-generation", "3"}, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "recovery generation moved, re-run: cfo drain") {
		t.Errorf("stdout = %q, want the mismatch message", stdout.String())
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %+v, want empty (ack-through still applies on mismatch)", pending)
	}
	ep, err := wake.ReadEpisode(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if !ep.Pending || ep.Gen != 4 {
		t.Errorf("episode = %+v, want still pending generation 4", ep)
	}
}

func TestRunDrainPartialAckLeavesEpisodePending(t *testing.T) {
	h := buildDrainFixture(t)
	var stdout, stderr bytes.Buffer
	if exit := runDrain(h, []string{"--ack-through", "5", "--recovery-generation", "4"}, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Seq != 7 {
		t.Errorf("pending = %+v, want only seq 7", pending)
	}
	ep, err := wake.ReadEpisode(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if !ep.Pending || ep.Gen != 4 {
		t.Errorf("episode = %+v, want still pending generation 4 (partial ack must not retire it)", ep)
	}
}

func TestRunDrainSingleFlagSemantics(t *testing.T) {
	h := buildDrainFixture(t)

	// --ack-through alone retires every queue row and must not touch the episode.
	var stdout1, stderr1 bytes.Buffer
	if exit := runDrain(h, []string{"--ack-through", "7"}, &stdout1, &stderr1); exit != 0 {
		t.Fatalf("ack-through alone: exit = %d, want 0; stderr=%s", exit, stderr1.String())
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %+v, want empty", pending)
	}
	ep, err := wake.ReadEpisode(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if !ep.Pending || ep.Gen != 4 {
		t.Errorf("episode = %+v, want still pending generation 4", ep)
	}

	// A following bare drain renders the second output shape.
	var stdout2, stderr2 bytes.Buffer
	if exit := runDrain(h, nil, &stdout2, &stderr2); exit != 0 {
		t.Fatalf("bare drain: exit = %d, want 0; stderr=%s", exit, stderr2.String())
	}
	assertLines(t, stdout2.String(), []string{
		"WAKE QUEUE: 0 pending",
		"RECOVERY EPISODE: pending, generation 4",
		"WAKE_ACK_REQUIRED: cfo drain --ack-through 0 --recovery-generation 4",
	})

	// --recovery-generation alone acks the episode because the queue is
	// already empty.
	var stdout3, stderr3 bytes.Buffer
	if exit := runDrain(h, []string{"--recovery-generation", "4"}, &stdout3, &stderr3); exit != 0 {
		t.Fatalf("recovery-generation alone: exit = %d, want 0; stderr=%s", exit, stderr3.String())
	}
	ep, err = wake.ReadEpisode(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Pending {
		t.Errorf("episode still pending after recovery-generation-alone ack: %+v", ep)
	}
}

func TestRunDrainEmptyQueueWithPendingEpisode(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.PublishEpisode(state); err != nil {
		t.Fatal(err)
	}
	h := home.Home{Root: root, State: state, Data: filepath.Join(root, "data")}

	var stdout, stderr bytes.Buffer
	if exit := runDrain(h, nil, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	assertLines(t, stdout.String(), []string{
		"WAKE QUEUE: 0 pending",
		"RECOVERY EPISODE: pending, generation 1",
		"WAKE_ACK_REQUIRED: cfo drain --ack-through 0 --recovery-generation 1",
	})

	var stdout2, stderr2 bytes.Buffer
	if exit := runDrain(h, []string{"--ack-through", "0", "--recovery-generation", "1"}, &stdout2, &stderr2); exit != 0 {
		t.Fatalf("running the printed ack command: exit = %d, want 0; stderr=%s", exit, stderr2.String())
	}
	ep, err := wake.ReadEpisode(state)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Pending {
		t.Errorf("episode still pending after ack: %+v", ep)
	}
}

func TestRunDrainNonEmptyQueueNoPendingEpisode(t *testing.T) {
	// A watcher's Run appends its wake record before it calls PublishEpisode
	// (or the episode marker is truncated), so a non-empty queue with no
	// pending episode is a reachable shape, not just a hand-edited one.
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	writeDrainQueueFixture(t, state)
	h := home.Home{Root: root, State: state, Data: filepath.Join(root, "data")}

	var stdout, stderr bytes.Buffer
	if exit := runDrain(h, nil, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	assertLines(t, stdout.String(), []string{
		"WAKE QUEUE: 3 pending",
		"  2  signal  g1.status: signal:g1.status",
		"  5  stale   w1: stale: w1 (idle 300s)",
		"  7  heartbeat  heartbeat",
		"WAKE_ACK_REQUIRED: cfo drain --ack-through 7",
	})

	var stdout2, stderr2 bytes.Buffer
	if exit := runDrain(h, []string{"--ack-through", "7"}, &stdout2, &stderr2); exit != 0 {
		t.Fatalf("running the printed ack command: exit = %d, want 0; stderr=%s", exit, stderr2.String())
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %+v, want empty", pending)
	}
}

// A blocked notify is a goblin parked on a question only the CFO can answer.
// Acking it destroys the only durable record of that question, which is how a
// drain whose output was truncated (piped through tail, say) silently strands a
// goblin. --ack-through must refuse it, and say which goblin is waiting.
func TestRunDrainRefusesToAckBlockedNotify(t *testing.T) {
	h := buildDrainFixture(t)
	if _, err := wake.Append(h.State, "notify", "gb-x", "blocked: which remedy, (a) or (b)?"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if exit := runDrain(h, []string{"--ack-through", "99"}, &stdout, &stderr); exit != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stderr.String(), "gb-x") {
		t.Errorf("stderr does not name the waiting goblin: %s", stderr.String())
	}
	// The refusal must leave the queue intact, or the question is lost anyway.
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, rec := range pending {
		if rec.Key == "gb-x" {
			found = true
		}
	}
	if !found {
		t.Error("blocked record was retired despite the refusal")
	}
}

// --ack-blocking is the operator saying they have read the question and are
// retiring it deliberately.
func TestRunDrainAckBlockingRetiresBlockedNotify(t *testing.T) {
	h := buildDrainFixture(t)
	if _, err := wake.Append(h.State, "notify", "gb-x", "blocked: which remedy?"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if exit := runDrain(h, []string{"--ack-through", "99", "--ack-blocking"}, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %+v, want empty", pending)
	}
}

// An ordinary done notify must still ack without ceremony — the guard is for
// questions, not for every notify.
func TestRunDrainAcksDoneNotifyWithoutFlag(t *testing.T) {
	h := buildDrainFixture(t)
	if _, err := wake.Append(h.State, "notify", "gb-y", "done: PR https://example.test/1"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if exit := runDrain(h, []string{"--ack-through", "99"}, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %+v, want empty", pending)
	}
}

// A block that a later done for the same goblin has superseded is no longer
// waiting on anyone; the guard must read the folded queue, not the raw log, or
// every finished goblin that ever asked a question blocks its own ack.
func TestRunDrainAcksSupersededBlockWithoutFlag(t *testing.T) {
	h := buildDrainFixture(t)
	if _, err := wake.Append(h.State, "notify", "gb-x", "blocked: which page?"); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.Append(h.State, "notify", "gb-x", "done: PR https://example.test/2"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if exit := runDrain(h, []string{"--ack-through", "99"}, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
}
