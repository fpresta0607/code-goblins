package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

type fakePolls struct {
	polls []Poll
	err   error
	calls int
}

func (f *fakePolls) Polls(context.Context) ([]Poll, error) {
	f.calls++
	return f.polls, f.err
}

func TestPollPageRecognisesOnlyALavishAxiPoll(t *testing.T) {
	script := `C:\Users\op\AppData\Roaming\npm\node_modules\lavish-axi\dist\cli.mjs`
	for name, c := range map[string]struct {
		args   []string
		page   string
		isPoll bool
	}{
		"node running the package script": {[]string{"node", script, "poll", `.lavish\plan.html`}, `.lavish\plan.html`, true},
		"a timeout before the page":       {[]string{"node", script, "poll", "--timeout-ms", "600000", `C:\w\plan.html`}, `C:\w\plan.html`, true},
		"a timeout after the page":        {[]string{"node", script, "poll", `C:\w\plan.html`, "--timeout-ms", "600000"}, `C:\w\plan.html`, true},
		"lavish-axi itself":               {[]string{`C:\tools\lavish-axi.exe`, "poll", "notes.md"}, "notes.md", true},
		"the shim by its bare name":       {[]string{"lavish-axi", "poll", `C:\my work\plan.html`}, `C:\my work\plan.html`, true},
		"a poll naming no page":           {[]string{"lavish-axi.cmd", "poll"}, "", true},
		"the review server":               {[]string{"node", script, "server", "--port", "4387"}, "", false},
		"opening a page":                  {[]string{"node", script, `.lavish\plan.html`, "--no-open"}, "", false},
		"another program's poll":          {[]string{"node", `C:\tools\other\cli.mjs`, "poll", "plan.html"}, "", false},
		"no arguments at all":             {nil, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			page, isPoll := pollPage(c.args)
			if page != c.page || isPoll != c.isPoll {
				t.Errorf("pollPage(%q) = %q, %v; want %q, %v", c.args, page, isPoll, c.page, c.isPoll)
			}
		})
	}
}

func TestWorktreeTaskNamesTheGoblinWhoseWorktreeHoldsADirectory(t *testing.T) {
	for dir, want := range map[string]string{
		`C:\dev\app\.worktrees\gb-task-1`:                           "task-1",
		`C:\dev\app\.worktrees\gb-task-1\web\.lavish`:               "task-1",
		`c:\DEV\app\.WORKTREES\GB-Task-2`:                           "Task-2",
		`C:\dev\app\.worktrees\gb-outer\vendor\.worktrees\gb-inner`: "inner",
		`C:\dev\app`:                                                  "",
		`C:\dev\app\.worktrees\feature-branch`:                        "",
		`C:\dev\app\.worktrees\gb-`:                                   "",
		`C:\dev\app\gb-task-1`:                                        "",
		`C:\dev\app\.worktrees\gb-bad id`:                             "",
		`C:\Users\op\AppData\Local\CodeGoblins`:                       "",
		`C:\dev\code-goblins\.worktrees\gb-cfo-native-board\internal`: "cfo-native-board",
	} {
		if got := worktreeTask(dir); got != want {
			t.Errorf("worktreeTask(%q) = %q, want %q", dir, got, want)
		}
	}
}

func pollScanService(t *testing.T, now *time.Time, polls PollProber, live ...string) Service {
	t.Helper()
	stateDir := t.TempDir()
	probe := &fakeProber{samples: map[string]EndpointSample{}}
	for _, id := range live {
		meta := metaFor(id)
		writeTask(t, stateDir, meta)
		probe.samples[id] = sampleFor(meta, herdr.BusyWorking, "working")
	}
	service := testService(stateDir, probe, now)
	service.Polls = polls
	return service
}

func scanOnce(t *testing.T, service Service) *Event {
	t.Helper()
	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return result.Event
}

func publish(t *testing.T, service Service, event *Event) {
	t.Helper()
	if event == nil {
		t.Fatal("no event to publish")
	}
	if _, err := service.Publish(*event); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func reviewRecords(t *testing.T, stateDir string) []wake.Record {
	t.Helper()
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	var reviews []wake.Record
	for _, record := range records {
		if record.Kind == "review" {
			reviews = append(reviews, record)
		}
	}
	return reviews
}

func flaggedPolls(t *testing.T, stateDir string) []Poll {
	t.Helper()
	return readPollRecord(stateDir).Flagged
}

// The incident this exists for: a goblin blocked on its own lavish-axi poll
// told nobody, and the Overlord's answers on its page reached no one else.
// The CFO hears of the poll once, with what to tell the goblin, and hears of
// the goblin's next poll too.
func TestScanFlagsAGoblinsOwnPollOnceForAsLongAsItRuns(t *testing.T) {
	now := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	poll := Poll{PID: 4242, Start: now.Add(-time.Minute), Task: "g1", Page: `C:\work\g1\.lavish\plan.html`}
	polls := &fakePolls{polls: []Poll{poll}}
	service := pollScanService(t, &now, polls, "g1")

	event := scanOnce(t, service)

	if event == nil || event.Kind != "review" || event.Key != "g1" || event.TaskID != "g1" {
		t.Fatalf("event = %+v, want a review event for g1", event)
	}
	for _, want := range []string{"goblin g1 is polling " + poll.Page + " itself", "pid 4242", "cfo notify g1 --waiting-on overlord <why> --lavish <page>"} {
		if !strings.Contains(event.Detail, want) {
			t.Errorf("detail = %q, want %q", event.Detail, want)
		}
	}
	publish(t, service, event)
	if records := reviewRecords(t, service.StateDir); len(records) != 1 || records[0].Key != "g1" || records[0].Detail != event.Detail {
		t.Fatalf("review wakes = %+v, want the one event", records)
	}

	if event := scanOnce(t, service); event != nil {
		t.Fatalf("the same poll, still running, raised %+v again", event)
	}

	polls.polls = nil
	if event := scanOnce(t, service); event != nil {
		t.Fatalf("an ended poll raised %+v", event)
	}
	if flagged := flaggedPolls(t, service.StateDir); len(flagged) != 0 {
		t.Fatalf("flags = %+v after the poll ended, want none", flagged)
	}
	polls.polls = []Poll{{PID: 5151, Start: now, Task: "g1", Page: poll.Page}}
	event = scanOnce(t, service)
	if event == nil || event.Key != "g1" || !strings.Contains(event.Detail, "pid 5151") {
		t.Fatalf("event = %+v, want the goblin's next poll flagged", event)
	}
}

// A retired goblin's poll has nobody left to tell, so the CFO is told to
// collect the questions itself and stop the poll. That is the PocketPiggies
// page that waited behind a poll whose goblin was already gone.
func TestScanFlagsARetiredGoblinsPollForTheCFOToAnswerItself(t *testing.T) {
	now := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	polls := &fakePolls{polls: []Poll{{PID: 77, Start: now, Task: "old-1", Page: `C:\work\old-1\.lavish\mockups.html`}}}
	service := pollScanService(t, &now, polls)
	if err := state.AppendStatus(service.StateDir, "old-1", "done: returned worktree via cfo cleanup"); err != nil {
		t.Fatal(err)
	}

	event := scanOnce(t, service)

	if event == nil || event.Kind != "review" || event.Key != "old-1" {
		t.Fatalf("event = %+v, want a review event for old-1", event)
	}
	for _, want := range []string{`still waits on C:\work\old-1\.lavish\mockups.html for goblin old-1, which is retired`, "pid 77", "read the page's open questions, put them to him, and stop the poll"} {
		if !strings.Contains(event.Detail, want) {
			t.Errorf("detail = %q, want %q", event.Detail, want)
		}
	}
}

// A poll in a worktree whose goblin this home never had is somebody else's,
// a test fixture's or another home's, and waking the CFO for it would be noise.
func TestScanNeverFlagsAPollOfAGoblinThisHomeNeverHad(t *testing.T) {
	now := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	polls := &fakePolls{polls: []Poll{{PID: 88, Start: now, Task: "stranger", Page: `C:\elsewhere\.lavish\p.html`}}}
	service := pollScanService(t, &now, polls, "g1")

	event := scanOnce(t, service)

	if polls.calls != 1 {
		t.Fatalf("the scan listed polls %d times, want once", polls.calls)
	}
	if event != nil {
		t.Fatalf("event = %+v, want none for a goblin this home never had", event)
	}
	if flagged := flaggedPolls(t, service.StateDir); len(flagged) != 0 {
		t.Fatalf("flags = %+v, want none", flagged)
	}
}

// One event leaves a scan, as every monitor event does, and a second poll
// waits for the next scan rather than being dropped.
func TestScanFlagsOnePollAtATimeAndLosesNone(t *testing.T) {
	now := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	polls := &fakePolls{polls: []Poll{
		{PID: 200, Start: now, Task: "g2", Page: `C:\work\g2\b.html`},
		{PID: 100, Start: now.Add(-time.Hour), Task: "g1", Page: `C:\work\g1\a.html`},
	}}
	service := pollScanService(t, &now, polls, "g1", "g2")

	first := scanOnce(t, service)
	if first == nil || first.Key != "g1" {
		t.Fatalf("first event = %+v, want the older poll, g1's", first)
	}
	if again := scanOnce(t, service); again == nil || *again != *first {
		t.Fatalf("unpublished event = %+v, want %+v again until it is published", again, first)
	}
	publish(t, service, first)
	second := scanOnce(t, service)
	if second == nil || second.Key != "g2" {
		t.Fatalf("second event = %+v, want g2's poll", second)
	}
	publish(t, service, second)
	if records := reviewRecords(t, service.StateDir); len(records) != 2 {
		t.Fatalf("review wakes = %+v, want one for each poll", records)
	}
}

// The poll's event and a stall of the same goblin are different events, so
// publishing one must not clear the other; both reach the CFO, once each.
func TestAPollFlagAndAStallOfTheSameGoblinBothReachTheCFO(t *testing.T) {
	now := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	polls := &fakePolls{polls: []Poll{{PID: 300, Start: now, Task: "g1", Page: `C:\work\g1\a.html`}}}
	service := pollScanService(t, &now, polls, "g1")
	flag := scanOnce(t, service)
	if flag == nil || flag.Kind != "review" {
		t.Fatalf("event = %+v, want the poll flagged", flag)
	}

	service.Probe.(*fakeProber).samples["g1"] = EndpointSample{Verdict: ProbeMissing, Detail: "pane missing"}
	pending := scanOnce(t, service)
	if pending == nil || *pending != *flag {
		t.Fatalf("event = %+v, want the unpublished poll flag first", pending)
	}
	publish(t, service, pending)
	stall := scanOnce(t, service)
	if stall == nil || stall.Kind != "stale" || stall.Key != "g1" {
		t.Fatalf("event = %+v, want the stall still pending after the poll flag was published", stall)
	}
	publish(t, service, stall)
	service.Probe.(*fakeProber).samples["g1"] = sampleFor(metaFor("g1"), herdr.BusyWorking, "working")
	if again := scanOnce(t, service); again != nil {
		t.Fatalf("event = %+v, want nothing left", again)
	}

	records, err := wake.Pending(service.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, record := range records {
		kinds[record.Kind]++
	}
	if kinds["review"] != 1 || kinds["stale"] != 1 {
		t.Fatalf("wakes = %+v, want one review and one stale", records)
	}
}

// A process table that cannot be read says nothing about which polls ended,
// so the flags stand and nothing is flagged a second time once it reads again.
func TestScanKeepsItsFlagsWhilePollsCannotBeListed(t *testing.T) {
	now := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	polls := &fakePolls{polls: []Poll{{PID: 400, Start: now, Task: "g1", Page: `C:\work\g1\a.html`}}}
	service := pollScanService(t, &now, polls, "g1")
	publish(t, service, scanOnce(t, service))

	polls.err = errors.New("process table unreadable")
	if event := scanOnce(t, service); event != nil {
		t.Fatalf("event = %+v while polls could not be listed", event)
	}
	if flagged := flaggedPolls(t, service.StateDir); len(flagged) != 1 || flagged[0].PID != 400 {
		t.Fatalf("flags = %+v, want the running poll's flag kept", flagged)
	}
	polls.err = nil
	if event := scanOnce(t, service); event != nil {
		t.Fatalf("the same poll was flagged again: %+v", event)
	}
}

// An older cfo decodes the heartbeat and every observation strictly and knows
// no review event in either, so a poll's flag and its pending event live in
// a record of their own that only this build reads.
func TestAPendingPollFlagLeavesTheRecordsAnOlderBinaryReadsUntouched(t *testing.T) {
	now := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	polls := &fakePolls{polls: []Poll{{PID: 500, Start: now, Task: "g1", Page: `C:\work\g1\a.html`}}}
	service := pollScanService(t, &now, polls, "g1")

	event := scanOnce(t, service)

	if event == nil || event.Kind != "review" {
		t.Fatalf("event = %+v, want the poll flagged", event)
	}
	heartbeat, err := ReadHeartbeat(service.StateDir)
	if err != nil {
		t.Fatalf("ReadHeartbeat: %v", err)
	}
	if heartbeat.PendingEvent != nil {
		t.Errorf("heartbeat pending event = %+v, want none", heartbeat.PendingEvent)
	}
	observation, err := ReadObservation(service.StateDir, "g1")
	if err != nil {
		t.Fatalf("ReadObservation: %v", err)
	}
	if observation.PendingEvent != nil {
		t.Errorf("observation pending event = %+v, want none", observation.PendingEvent)
	}
}
