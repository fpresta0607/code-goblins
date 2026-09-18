package wake

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAppendAssignsSequence(t *testing.T) {
	dir := t.TempDir()
	for want := 1; want <= 3; want++ {
		rec, err := Append(dir, "signal", "signal", "goblin g1 finished")
		if err != nil {
			t.Fatalf("Append %d: %v", want, err)
		}
		if rec.Seq != want {
			t.Errorf("Seq = %d, want %d", rec.Seq, want)
		}
	}
}

func TestPendingReturnsAllInOrder(t *testing.T) {
	dir := t.TempDir()
	kinds := []string{"signal", "stale", "check"}
	for _, k := range kinds {
		if _, err := Append(dir, k, k, "detail of "+k); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Pending(dir)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, k := range kinds {
		if got[i].Kind != k || got[i].Seq != i+1 {
			t.Errorf("record %d = %+v, want kind %s seq %d", i, got[i], k, i+1)
		}
	}
}

func TestAckThroughDropsHandledKeepsRest(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if _, err := Append(dir, "signal", "signal", "d"); err != nil {
			t.Fatal(err)
		}
	}
	if err := AckThrough(dir, 2); err != nil {
		t.Fatalf("AckThrough: %v", err)
	}
	got, err := Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Seq != 3 {
		t.Errorf("pending = %+v, want only seq 3", got)
	}
}

func TestSequenceNeverReusedAfterAck(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if _, err := Append(dir, "signal", "signal", "d"); err != nil {
			t.Fatal(err)
		}
	}
	if err := AckThrough(dir, 3); err != nil {
		t.Fatal(err)
	}
	rec, err := Append(dir, "signal", "signal", "after ack")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Seq != 4 {
		t.Errorf("Seq = %d, want 4 (sequences are never reused)", rec.Seq)
	}
}

func TestAckThroughIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Append(dir, "signal", "signal", "d"); err != nil {
		t.Fatal(err)
	}
	if err := AckThrough(dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := AckThrough(dir, 1); err != nil {
		t.Fatalf("second identical ack must succeed, got %v", err)
	}
	got, err := Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("pending = %+v, want empty", got)
	}
}

func TestPendingEmptyWhenNoQueueFile(t *testing.T) {
	got, err := Pending(t.TempDir())
	if err != nil {
		t.Fatalf("missing queue must mean empty, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("pending = %+v, want empty", got)
	}
}

func TestAppendRejectsUnknownKind(t *testing.T) {
	dir := t.TempDir()
	_, err := Append(dir, "bogus", "k", "d")
	if err == nil {
		t.Fatal("Append with unknown kind must error")
	}
	for _, kind := range []string{"signal", "stale", "check", "heartbeat"} {
		if !strings.Contains(err.Error(), kind) {
			t.Errorf("error %q does not name legal kind %q", err.Error(), kind)
		}
	}
}

// The seq 502 loss, reproduced. A blocked escalation from a task was followed
// 71 seconds later by another notify from the SAME task; the listing showed
// only the second, while the ack line it printed covered both, so the
// escalation was retired unread and the goblin sat blocked believing it had
// asked. Two records from one task must both be listed, both counted, and the
// ack must reach no further than what was listed.
func TestRenderListsEveryRecordFromOneTask(t *testing.T) {
	dir := t.TempDir()
	first, err := Append(dir, "notify", "gb-pd-pr-review", "blocked: rule on PR #1140")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Append(dir, "notify", "gb-pd-pr-review", "done: PR https://example.test/pull/1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Seq == second.Seq {
		t.Fatalf("both notifies took seq %d, so this cannot test a fold", first.Seq)
	}

	pending, err := Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Render(&out, pending, Episode{}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	if !strings.Contains(got, "WAKE QUEUE: 2 pending") {
		t.Errorf("render = %q, want both records counted", got)
	}
	if !strings.Contains(got, "question: rule on PR #1140") {
		t.Errorf("render = %q, want the blocked escalation listed, not folded behind the later notify", got)
	}
	if !strings.Contains(got, "done: PR https://example.test/pull/1") {
		t.Errorf("render = %q, want the later notify listed too", got)
	}
	if want := fmt.Sprintf("--ack-through %d", second.Seq); !strings.Contains(got, want) {
		t.Errorf("render = %q, want the ack line to name %s", got, want)
	}
}

// The ack sequence must come from what was shown. This drives the derivation
// directly with a narrowed listing, because Render passing its own rows makes
// the sets match by construction - and a guard that can only be observed when
// it is impossible to violate is not a guard.
func TestAckSequenceRefusesToOutrunTheListing(t *testing.T) {
	records := []Record{
		{Seq: 502, Kind: "notify", Key: "gb-pd-pr-review", Detail: "blocked: rule on PR #1140"},
		{Seq: 503, Kind: "notify", Key: "gb-pd-pr-review", Detail: "done: shipped"},
	}
	if seq, ok := ackSequence(records, records); !ok || seq != 503 {
		t.Errorf("ackSequence(all shown) = %d, %v, want 503, true", seq, ok)
	}

	narrowed := records[1:]
	if len(narrowed) == len(records) {
		t.Fatal("the narrowed listing is not narrower, so this asserts nothing")
	}
	if seq, ok := ackSequence(records, narrowed); ok {
		t.Errorf("ackSequence(record hidden) = %d, true, want a refusal: acking 503 here retires the unread 502", seq)
	}
}

// The field that was missing. On 2026-09-18 two goblins waited 8h47m on a CFO
// decision and the Overlord noticed before the fleet did, because the question
// read as one more status line. An outstanding decision is rendered as a
// decision: which goblin, how long it has waited, what it asked, and the
// options it offered.
func TestRenderShowsAnOutstandingDecisionWithItsWaitAndOptions(t *testing.T) {
	now := time.Date(2026, 9, 18, 11, 27, 49, 0, time.UTC)
	records := []Record{
		{Seq: 1, Time: now.Add(-8*time.Hour - 47*time.Minute), Kind: "notify", Key: "siteplan-r2",
			Detail: "blocked: rebaseline or fix? options: rebaseline the snapshot | fix the renderer"},
		{Seq: 2, Time: now.Add(-4 * time.Minute), Kind: "notify", Key: "runtime-truth",
			Detail: "done: PR https://example.test/pull/24"},
	}

	var out bytes.Buffer
	if err := Render(&out, records, Episode{}, now); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	for _, want := range []string{
		"DECISION  siteplan-r2  blocked, waiting 8h47m",
		"question: rebaseline or fix?",
		"1) rebaseline the snapshot",
		"2) fix the renderer",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render = %q, want it to contain %q", got, want)
		}
	}
	if !strings.Contains(got, "  2  notify  runtime-truth: done: PR https://example.test/pull/24") {
		t.Errorf("render = %q, want an informational notify left as a plain line", got)
	}
	if !strings.Contains(got, "--ack-through 2") {
		t.Errorf("render = %q, want the ack line still derived from every row shown", got)
	}
}

// A goblin that offered no options still gets a decision block: the renderer
// says none were offered rather than inventing choices it never named.
func TestRenderSaysWhenADecisionOfferedNoOptions(t *testing.T) {
	now := time.Date(2026, 9, 18, 11, 27, 49, 0, time.UTC)
	records := []Record{
		{Seq: 7, Time: now.Add(-90 * time.Second), Kind: "notify", Key: "ocr-eval", Detail: "blocked: which tokenizer?"},
	}

	var out bytes.Buffer
	if err := Render(&out, records, Episode{}, now); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	if !strings.Contains(got, "DECISION  ocr-eval  blocked, waiting 2m") {
		t.Errorf("render = %q, want the decision block with its wait", got)
	}
	if !strings.Contains(got, "options:  none offered") {
		t.Errorf("render = %q, want the absence of options stated out loud", got)
	}
}

func TestWaitedReadsToTheMinuteAndNeverNegative(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{8*time.Hour + 47*time.Minute, "8h47m"},
		{time.Hour + 20*time.Second, "1h00m"},
		{time.Hour + 40*time.Second, "1h01m"},
		{45 * time.Minute, "45m"},
		{30 * time.Second, "under a minute"},
		{-2 * time.Hour, "under a minute"},
	} {
		if got := waited(tc.in); got != tc.want {
			t.Errorf("waited(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
