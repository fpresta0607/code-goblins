package connections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

func TestServiceStatusRequiresAProbeBeforeConnected(t *testing.T) {
	for _, test := range []struct {
		name  string
		state auth.State
		probe []string
		want  string
	}{
		{"working", auth.StateGreen, []string{"probe"}, "connected"},
		{"credential only", auth.StateGreen, nil, "unverified"},
		{"wrong target", auth.StateWrongTarget, []string{"probe"}, "wrong_target"},
		{"missing", auth.StateMissing, nil, "missing"},
		{"rejected", auth.StateUnauthorized, []string{"probe"}, "unauthorized"},
		{"expired", auth.StateExpired, []string{"probe"}, "expired"},
		{"offline", auth.StateUnreachable, []string{"probe"}, "unreachable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := serviceEntry(auth.Service{Name: "sample", Probe: test.probe}, auth.Status{State: test.state, Detail: "private diagnostic"})
			if entry.Status != test.want {
				t.Fatalf("status = %q, want %q", entry.Status, test.want)
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
	cache := NewCache(time.Minute, time.Second, func(ctx context.Context, key string) Snapshot {
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

func TestCacheStartsACheckOnReadOnlyAfterItsResultIsStale(t *testing.T) {
	var calls atomic.Int32
	cache := NewCache(50*time.Millisecond, time.Second, func(context.Context, string) Snapshot {
		calls.Add(1)
		return Snapshot{Entries: []Entry{}}
	})
	defer cache.Close()
	cache.Get("one", false)
	waitChecked(t, cache, "one")
	if cache.Get("one", false).Checking || calls.Load() != 1 {
		t.Fatal("a fresh result started another check")
	}
	time.Sleep(60 * time.Millisecond)
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
	cache := NewCache(200*time.Millisecond, time.Second, func(context.Context, string) Snapshot {
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

func TestRefreshesDuringACheckCoalesceIntoOneFollowingCheck(t *testing.T) {
	var calls atomic.Int32
	releases := []chan struct{}{make(chan struct{}), make(chan struct{})}
	cache := NewCache(time.Minute, time.Second, func(context.Context, string) Snapshot {
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
	deadline := time.After(2 * time.Second)
	for calls.Load() < 2 {
		if !cache.Cached("one").Checking {
			t.Fatal("pending refresh went idle between checks")
		}
		select {
		case <-deadline:
			t.Fatal("pending refresh never ran")
		case <-time.After(time.Millisecond):
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
	cache := NewCache(time.Minute, time.Second, func(ctx context.Context, _ string) Snapshot {
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
	deadline := time.After(2 * time.Second)
	for {
		result := cache.Get(key, false)
		if !result.Checking {
			return result
		}
		select {
		case <-deadline:
			t.Fatal(errors.New("check did not finish"))
		case <-time.After(time.Millisecond):
		}
	}
}
