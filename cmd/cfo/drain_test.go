package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// The blindness that lost seq 502: the guard used to iterate the same folded
// view the listing did, so a later notify from the same task hid the blocked
// record from the guard as well. --ack-blocking was never needed to lose an
// escalation - the safety net simply could not see it.
func TestRunDrainRefusesBlockedNotifyFollowedByALaterNotify(t *testing.T) {
	h := buildDrainFixture(t)
	blocked, err := wake.Append(h.State, "notify", "gb-pd-pr-review", "blocked: rule on PR #1140")
	if err != nil {
		t.Fatal(err)
	}
	later, err := wake.Append(h.State, "notify", "gb-pd-pr-review", "done: PR https://example.test/pull/1")
	if err != nil {
		t.Fatal(err)
	}

	// Assert the premise. Without a LATER record from the SAME task there is no
	// fold to be blind to, and this test would pass while proving nothing.
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	var sawBlocked, sawLater bool
	for _, rec := range pending {
		if rec.Seq == blocked.Seq && rec.Key == "gb-pd-pr-review" {
			sawBlocked = true
		}
		if rec.Seq == later.Seq && rec.Key == "gb-pd-pr-review" {
			sawLater = true
		}
	}
	if !sawBlocked || !sawLater || blocked.Seq >= later.Seq {
		t.Fatalf("premise broken: blocked=%d(%v) later=%d(%v); the test needs both records from one task, blocked first", blocked.Seq, sawBlocked, later.Seq, sawLater)
	}

	var stdout, stderr bytes.Buffer
	if exit := runDrain(h, []string{"--ack-through", strconv.Itoa(later.Seq)}, &stdout, &stderr); exit != 1 {
		t.Fatalf("exit = %d, want 1: the guard must see a blocked record hidden behind a later notify; stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stderr.String(), "rule on PR #1140") {
		t.Errorf("stderr = %q, want it to name the escalation it refused to ack", stderr.String())
	}

	after, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(pending) {
		t.Errorf("pending = %d records, want the refusal to retire nothing (was %d)", len(after), len(pending))
	}
}

// Both records must also be listed and counted, so the operator can read the
// escalation the fold used to hide.
func TestRunDrainListsBothNotifiesFromOneTask(t *testing.T) {
	h := buildDrainFixture(t)
	if _, err := wake.Append(h.State, "notify", "gb-pd-pr-review", "blocked: rule on PR #1140"); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.Append(h.State, "notify", "gb-pd-pr-review", "done: PR https://example.test/pull/1"); err != nil {
		t.Fatal(err)
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if exit := runDrain(h, nil, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	got := stdout.String()

	// The count is every unacknowledged record, not a folded subset. Comparing
	// against the queue rather than a literal keeps this honest if the fixture
	// grows.
	if want := fmt.Sprintf("WAKE QUEUE: %d pending", len(pending)); !strings.Contains(got, want) {
		t.Errorf("drain output = %q, want %q", got, want)
	}
	for _, rec := range pending {
		if !strings.Contains(got, rec.Detail) {
			t.Errorf("drain output = %q, want it to list seq %d (%s)", got, rec.Seq, rec.Detail)
		}
	}
	if !strings.Contains(got, "blocked: rule on PR #1140") {
		t.Errorf("drain output = %q, want the escalation the fold used to hide", got)
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

// A later done from the same goblin does NOT retire an unanswered question.
// This reverses 4dc3d30, which judged the guard on the folded queue so that a
// finished goblin would not block its own ack. That removed the friction and
// took the protection with it: the fold hid the blocked record from the guard
// as well as from the listing, so an escalation could be acked unread, which
// is how seq 502 was lost.
//
// The friction is now the intended cost, and it is no longer blind: the
// listing shows the blocked record, so an operator reaching for --ack-blocking
// has read the question first, which is exactly what that flag is documented
// to mean. A goblin reporting done is not evidence that its question was
// answered - only the answer is, and the queue does not record one.
func TestRunDrainRefusesASupersededBlockWithoutFlag(t *testing.T) {
	h := buildDrainFixture(t)
	blocked, err := wake.Append(h.State, "notify", "gb-x", "blocked: which page?")
	if err != nil {
		t.Fatal(err)
	}
	done, err := wake.Append(h.State, "notify", "gb-x", "done: PR https://example.test/2")
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Seq >= done.Seq {
		t.Fatalf("premise broken: blocked %d must precede done %d", blocked.Seq, done.Seq)
	}

	var stdout, stderr bytes.Buffer
	if exit := runDrain(h, []string{"--ack-through", "99"}, &stdout, &stderr); exit != 1 {
		t.Fatalf("exit = %d, want 1: an unanswered question is not retired by a later done; stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stderr.String(), "which page?") {
		t.Errorf("stderr = %q, want the refused question named", stderr.String())
	}

	// And --ack-blocking still retires it, so the operator is never stuck.
	stdout.Reset()
	stderr.Reset()
	if exit := runDrain(h, []string{"--ack-through", "99", "--ack-blocking"}, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d, want 0 with --ack-blocking; stderr=%s", exit, stderr.String())
	}
}
