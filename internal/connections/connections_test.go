package connections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

func TestServiceStatusRequiresAProbeBeforeConnected(t *testing.T) {
	for _, test := range []struct {
		name      string
		state     auth.State
		probe     []string
		unchecked string
		want      string
	}{
		{"working", auth.StateGreen, []string{"probe"}, "", "connected"},
		{"credential only", auth.StateGreen, nil, "", "provided"},
		{"probe program not on this machine", auth.StateUnverified, []string{"probe"}, "probe is not installed", "provided"},
		{"probe ran and did not confirm", auth.StateUnverified, []string{"probe"}, "", "unverified"},
		{"wrong target", auth.StateWrongTarget, []string{"probe"}, "", "wrong_target"},
		{"missing", auth.StateMissing, nil, "", "missing"},
		{"rejected", auth.StateUnauthorized, []string{"probe"}, "", "unauthorized"},
		{"expired", auth.StateExpired, []string{"probe"}, "", "expired"},
		{"offline", auth.StateUnreachable, []string{"probe"}, "", "unreachable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := serviceEntry(auth.Service{Name: "sample", Probe: test.probe}, auth.Status{State: test.state, Detail: "private diagnostic", Unchecked: test.unchecked})
			if entry.Status != test.want {
				t.Fatalf("status = %q, want %q", entry.Status, test.want)
			}
			if test.unchecked != "" && !strings.Contains(entry.Detail, "Not checked on this machine: "+test.unchecked) {
				t.Fatalf("detail = %q does not say why no check ran", entry.Detail)
			}
			data, _ := json.Marshal(entry)
			if strings.Contains(string(data), "private diagnostic") {
				t.Fatal("raw probe detail escaped")
			}
		})
	}
}

func TestClaudeStatusNeverExposesCommandsOrURLs(t *testing.T) {
	output := "{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"1\",\"response\":{}}}\n" + `{"type":"control_response","response":{"subtype":"success","request_id":"2","response":{"mcpServers":[{"name":"repo","status":"connected","config":{"command":"private-command"}},{"name":"broken","status":"failed","error":"synthetic-secret"},{"name":"login","status":"needs-auth","config":{"url":"https://example.invalid"}},{"name":"unknown","status":"unknown"}]}}}` + "\n"
	var input bytes.Buffer
	entries, err := claudeReport(context.Background(), strings.NewReader(output), &input)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %v", entries)
	}
	for index, want := range []string{"connected", "failed", "unauthorized", "unverified"} {
		if entries[index].Status != want {
			t.Errorf("entry %d = %s", index, entries[index].Status)
		}
	}
	data, _ := json.Marshal(entries)
	for _, secret := range []string{"synthetic-secret", "private-command", "https://"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
}

func TestCodexNeedsRuntimeHealthNotAnAuthMethodOrCachedTools(t *testing.T) {
	for _, test := range []struct {
		runtime, auth, want string
		isSignIn            bool
	}{
		{"connected", "bearerToken", "connected", false}, {"", "oAuth", "unverified", false},
		{"failed", "oAuth", "failed", false}, {"authenticationRequired", "notLoggedIn", "unauthorized", true},
		{"starting", "bearerToken", "checking", false}, {"disabled", "unknown", "disabled", false},
		{"connected", "notLoggedIn", "connected", false}, {"disabled", "notLoggedIn", "disabled", false},
	} {
		t.Run(test.runtime+"-"+test.auth, func(t *testing.T) {
			entry := codexEntry(codexStatus{Name: "sample", RuntimeStatus: test.runtime, AuthStatus: test.auth})
			if entry.Status != test.want || slices.Contains(entry.Actions, "login") != test.isSignIn {
				t.Fatalf("status = %s actions = %v, want %s sign-in %v", entry.Status, entry.Actions, test.want, test.isSignIn)
			}
		})
	}
}

func TestCacheReturnsWhileCheckingAndSharesOneCheck(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	cache := NewCache(time.Minute, time.Minute, func(ctx context.Context, key string) Snapshot {
		calls.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return Snapshot{Entries: []Entry{{Name: key, Status: "connected"}}}
	})
	defer cache.Close()
	first := cache.Get("one", false)
	if !first.Checking {
		t.Fatal("initial request waited")
	}
	for range 20 {
		cache.Get("one", false)
	}
	close(release)
	result := waitChecked(t, cache, "one")
	if calls.Load() != 1 || len(result.Entries) != 1 || result.Entries[0].Status != "connected" {
		t.Fatalf("calls=%d result=%+v", calls.Load(), result)
	}
	cache.Get("one", false)
	if calls.Load() != 1 {
		t.Fatal("render reran a check")
	}
	cache.Get("two", false)
	waitChecked(t, cache, "two")
	if calls.Load() != 2 {
		t.Fatal("generation shared old result")
	}
}

func TestCacheRefreshPreservesEvidenceAndTimeoutIsVisible(t *testing.T) {
	var calls atomic.Int32
	cache := NewCache(time.Minute, 20*time.Millisecond, func(ctx context.Context, _ string) Snapshot {
		if calls.Add(1) == 1 {
			return Snapshot{Entries: []Entry{{Name: "sample", Status: "connected"}}}
		}
		<-ctx.Done()
		return Snapshot{Error: ctx.Err().Error()}
	})
	defer cache.Close()
	cache.Get("one", false)
	waitChecked(t, cache, "one")
	refreshed := cache.Get("one", true)
	if !refreshed.Checking || len(refreshed.Entries) != 1 {
		t.Fatal("refresh discarded old evidence")
	}
	result := waitChecked(t, cache, "one")
	if result.Error != "Connection check timed out." {
		t.Fatalf("timeout = %q", result.Error)
	}
	if len(result.Entries) != 1 || result.Entries[0].Status != "unverified" {
		t.Fatalf("stale success after timeout: %+v", result)
	}
}

// A check that waits for one of the cache's two slots starts its time limit
// only once it has one, so a page that asks for every goblin at once queues
// the checks past the first two instead of timing them out unrun (#2394).
func TestCheckWaitingForItsSlotIsNotTimedOutByTheWait(t *testing.T) {
	// Arrange
	release := make(chan struct{})
	started := make(chan string, 3)
	cache := NewCache(time.Minute, 50*time.Millisecond, func(ctx context.Context, key string) Snapshot {
		started <- key
		if key != "third" {
			<-release
		}
		return Snapshot{Entries: []Entry{{Name: key, Status: "connected"}}}
	})
	defer cache.Close()
	cache.Get("first", false)
	cache.Get("second", false)
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Minute):
			t.Fatal("the first two checks never took their slots")
		}
	}

	// Act
	cache.Get("third", false)
	time.Sleep(150 * time.Millisecond) // stall-accepted: longer than the time limit, which a check waiting for its slot must outlive.
	close(release)
	result := waitChecked(t, cache, "third")

	// Assert
	if result.Error != "" || len(result.Entries) != 1 || result.Entries[0].Status != "connected" {
		t.Fatalf("queued check = %+v, want its own connected result", result)
	}
}

func TestCacheStartsACheckOnReadOnlyAfterItsResultIsStale(t *testing.T) {
	var calls atomic.Int32
	cache := NewCache(time.Minute, time.Second, func(context.Context, string) Snapshot {
		calls.Add(1)
		return Snapshot{Entries: []Entry{}}
	})
	defer cache.Close()
	// The cache's clock moves only when the test moves it, so a result is
	// fresh until then however long a busy machine takes between reads.
	var ahead atomic.Int64
	cache.Now = func() time.Time { return time.Now().Add(time.Duration(ahead.Load())) } // stall-accepted: the offset the test moves the cache's clock by, not a wait, and a result stays fresh a minute.
	cache.Get("one", false)
	waitChecked(t, cache, "one")
	if cache.Get("one", false).Checking || calls.Load() != 1 {
		t.Fatal("a fresh result started another check")
	}
	ahead.Store(int64(time.Minute))
	if !cache.Get("one", false).Checking {
		t.Fatal("a stale result did not start a check")
	}
	waitChecked(t, cache, "one")
	if calls.Load() != 2 {
		t.Fatalf("checks = %d", calls.Load())
	}
}

func TestCachedReadNeverStartsACheck(t *testing.T) {
	var calls atomic.Int32
	cache := NewCache(200*time.Millisecond, time.Minute, func(context.Context, string) Snapshot {
		calls.Add(1)
		return Snapshot{Entries: []Entry{{Name: "sample", Status: "connected"}}}
	})
	defer cache.Close()
	if missing := cache.Cached("one"); missing.Checking || !missing.CheckedAt.IsZero() || calls.Load() != 0 {
		t.Fatalf("absent result = %+v after %d checks", missing, calls.Load())
	}
	cache.Get("one", false)
	waitChecked(t, cache, "one")
	time.Sleep(250 * time.Millisecond)
	stale := cache.Cached("one")
	if stale.Checking || len(stale.Entries) != 1 || calls.Load() != 1 {
		t.Fatalf("stale read = %+v after %d checks", stale, calls.Load())
	}
}

// An observation older than the cache's time to live is shown as it was
// checked, with the time of that check, while the recheck it started runs:
// it is old, which its age says, and no check has said it stopped working.
// A failing verdict keeps reading so as much as a passing one.
func TestExpiredObservationKeepsItsVerdictAndAgeWhileItsRecheckRuns(t *testing.T) {
	for _, verdict := range []string{"probe_succeeded", "failed", "expired", "missing", "unreachable"} {
		t.Run(verdict, func(t *testing.T) {
			// Arrange
			var calls atomic.Int32
			var ahead atomic.Int64
			release := make(chan struct{})
			cache := NewCache(time.Minute, time.Minute, func(ctx context.Context, _ string) Snapshot {
				if calls.Add(1) > 1 {
					select {
					case <-release:
					case <-ctx.Done():
					}
				}
				return Snapshot{Entries: []Entry{{ID: "mcp:tools", Status: verdict, Scope: "separate_probe", LiveStatus: "unknown"}}}
			})
			cache.Now = func() time.Time { return time.Unix(100, 0).Add(time.Duration(ahead.Load())) }
			defer cache.Close()
			defer close(release)
			cache.Get("task\ng1", false)
			waitChecked(t, cache, "task\ng1")
			ahead.Store(int64(time.Minute))

			// Act
			result := cache.Get("task\ng1", false)

			// Assert
			if !result.Checking {
				t.Fatalf("expired observation started no recheck: %+v", result)
			}
			if result.Entries[0].Status != verdict || result.Entries[0].LiveStatus != "unknown" || !result.CheckedAt.Equal(time.Unix(100, 0)) {
				t.Fatalf("expired observation = %+v, want %s checked at %v", result, verdict, time.Unix(100, 0).UTC())
			}
		})
	}
}

func TestRefreshesDuringACheckCoalesceIntoOneFollowingCheck(t *testing.T) {
	var calls atomic.Int32
	releases := []chan struct{}{make(chan struct{}), make(chan struct{})}
	cache := NewCache(time.Minute, time.Minute, func(context.Context, string) Snapshot {
		call := calls.Add(1)
		<-releases[call-1]
		return Snapshot{Entries: []Entry{{Name: "sample", Status: map[int32]string{1: "unauthorized", 2: "connected"}[call]}}}
	})
	defer cache.Close()
	cache.Get("one", false)
	for range 3 {
		if !cache.Get("one", true).Checking {
			t.Fatal("refresh during a check reported idle")
		}
	}
	close(releases[0])
	deadline := time.After(time.Minute)
	for calls.Load() < 2 {
		if !cache.Cached("one").Checking {
			t.Fatal("pending refresh went idle between checks")
		}
		select {
		case <-deadline:
			t.Fatal("pending refresh never ran")
		case <-time.After(time.Millisecond): // stall-accepted: a pause between polls, whose running out only starts the next poll.
		}
	}
	if !cache.Cached("one").Checking {
		t.Fatal("following check is not reported")
	}
	close(releases[1])
	result := waitChecked(t, cache, "one")
	if calls.Load() != 2 || len(result.Entries) != 1 || result.Entries[0].Status != "connected" {
		t.Fatalf("calls=%d result=%+v", calls.Load(), result)
	}
}

func TestClosingDropsAPendingRefresh(t *testing.T) {
	var calls atomic.Int32
	cache := NewCache(time.Minute, time.Minute, func(ctx context.Context, _ string) Snapshot {
		calls.Add(1)
		<-ctx.Done()
		return Snapshot{}
	})
	cache.Get("one", false)
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	cache.Get("one", true)
	cache.Close()
	if calls.Load() != 1 || cache.Cached("one").Checking {
		t.Fatalf("closed cache ran %d checks, checking=%v", calls.Load(), cache.Cached("one").Checking)
	}
}

func waitChecked(t *testing.T, cache *Cache, key string) Snapshot {
	t.Helper()
	deadline := time.After(time.Minute)
	for {
		result := cache.Get(key, false)
		if !result.Checking {
			return result
		}
		select {
		case <-deadline:
			t.Fatal(errors.New("check did not finish"))
		case <-time.After(time.Millisecond): // stall-accepted: a pause between polls, whose running out only starts the next poll.
		}
	}
}

// A check whose time ran out while a server was still starting keeps what it
// saw of every other server, and only the one still starting reads
// unverified, since its check could have run and did not confirm it.
func TestClaudeCheckThatEndsWhileAServerStartsLeavesOnlyThatServerUnverified(t *testing.T) {
	// Arrange
	output := "{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"1\",\"response\":{}}}\n" + `{"type":"control_response","response":{"subtype":"success","request_id":"2","response":{"mcpServers":[{"name":"ready","status":"connected"},{"name":"slow","status":"pending"},{"name":"broken","status":"failed"}]}}}` + "\n"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var input bytes.Buffer

	// Act
	entries, err := claudeReport(ctx, strings.NewReader(output), &input)

	// Assert
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries = %+v, error = %v", entries, err)
	}
	for index, want := range []string{"connected", "unverified", "failed"} {
		if entries[index].Status != want {
			t.Errorf("%s = %s, want %s", entries[index].Name, entries[index].Status, want)
		}
	}
	if entries[1].Detail != "Still starting when the check ended." {
		t.Errorf("slow server says %q", entries[1].Detail)
	}
}

// A check whose time runs out while Claude has yet to answer its next report,
// after one with a server still starting, ends with the report it had: Claude
// is ended with the check, the servers that connected or failed keep their
// verdicts, and only the one still starting reads unverified.
func TestClaudeCheckThatEndsMidReportKeepsTheReportItHad(t *testing.T) {
	// Arrange
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output, harness := io.Pipe()
	requests, input := io.Pipe()
	defer requests.Close()
	go func() {
		defer harness.Close()
		decoder := json.NewDecoder(requests)
		for _, reply := range []string{
			`{"type":"control_response","response":{"subtype":"success","request_id":"1","response":{}}}`,
			`{"type":"control_response","response":{"subtype":"success","request_id":"2","response":{"mcpServers":[{"name":"ready","status":"connected"},{"name":"slow","status":"pending"},{"name":"broken","status":"failed"}]}}}`,
		} {
			var request json.RawMessage
			if decoder.Decode(&request) != nil {
				return
			}
			fmt.Fprintln(harness, reply)
		}
		var next json.RawMessage
		if decoder.Decode(&next) == nil {
			cancel()
		}
	}()

	// Act
	entries, err := claudeReport(ctx, output, input)

	// Assert
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries = %+v, error = %v", entries, err)
	}
	for index, want := range []string{"connected", "unverified", "failed"} {
		if entries[index].Status != want {
			t.Errorf("%s = %s, want %s", entries[index].Name, entries[index].Status, want)
		}
	}
	if entries[1].Detail != "Still starting when the check ended." {
		t.Errorf("slow server says %q", entries[1].Detail)
	}
}

func TestClaudeStatusListsEachServersToolsByNameWithItsReadOnlyMark(t *testing.T) {
	// Arrange: the report as Claude Code 2.1.322 sends it, each tool with its
	// name and annotations, in the server's own order.
	output := "{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"1\",\"response\":{}}}\n" + `{"type":"control_response","response":{"subtype":"success","request_id":"2","response":{"mcpServers":[{"name":"claude.ai Supabase","status":"connected","tools":[{"name":"list_projects","annotations":{"readOnly":true}},{"name":"execute_sql","annotations":{"destructive":true}},{"name":"apply_migration","annotations":{}},{"name":"https://example.invalid/tool","annotations":{"readOnly":true}},{"name":"","annotations":{}}]},{"name":"login","status":"needs-auth"}]}}}` + "\n"
	var input bytes.Buffer

	// Act
	entries, err := claudeReport(context.Background(), strings.NewReader(output), &input)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []Tool{{Name: "apply_migration"}, {Name: "execute_sql"}, {Name: "list_projects", ReadOnly: true}}
	if len(entries) != 2 || !slices.Equal(entries[0].Tools, want) {
		t.Fatalf("tools = %+v, want %+v", entries, want)
	}
	if entries[1].Tools != nil {
		t.Fatalf("a server that reported no tools lists %+v", entries[1].Tools)
	}
	data, _ := json.Marshal(entries[1])
	if strings.Contains(string(data), "tools") {
		t.Fatalf("an entry without tools carries the field: %s", data)
	}
}

func TestCodexStatusListsEachServersToolsByNameWithItsReadOnlyMark(t *testing.T) {
	// Arrange: one server of mcpServerStatus/list as Codex 0.160.0 sends it,
	// its tools keyed by name with the protocol's own annotations.
	var status codexStatus
	report := `{"name":"docs","runtimeStatus":"connected","authStatus":"unsupported","tools":{"search_docs":{"name":"search_docs","annotations":{"readOnlyHint":true,"destructiveHint":false}},"write_page":{"name":"write_page","annotations":{"readOnlyHint":false}},"run":{"name":"run"}}}`
	if err := json.Unmarshal([]byte(report), &status); err != nil {
		t.Fatal(err)
	}

	// Act
	entry := codexEntry(status)

	// Assert
	want := []Tool{{Name: "run"}, {Name: "search_docs", ReadOnly: true}, {Name: "write_page"}}
	if !slices.Equal(entry.Tools, want) {
		t.Fatalf("tools = %+v, want %+v", entry.Tools, want)
	}
}

func TestAServersToolListIsBounded(t *testing.T) {
	// Arrange
	reported := make([]Tool, toolLimit+40)
	for index := range reported {
		reported[index] = Tool{Name: fmt.Sprintf("tool_%05d", index)}
	}

	// Act
	listed := listedTools(reported)

	// Assert
	if len(listed) != toolLimit || listed[0].Name != "tool_00000" || listed[toolLimit-1].Name != fmt.Sprintf("tool_%05d", toolLimit-1) {
		t.Fatalf("listed %d tools, first %q, last %q", len(listed), listed[0].Name, listed[len(listed)-1].Name)
	}
}
