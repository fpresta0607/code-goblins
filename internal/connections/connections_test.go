package connections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	for _, test := range []struct{ runtime, auth, want string }{
		{"connected", "bearerToken", "connected"}, {"", "oAuth", "unverified"},
		{"failed", "oAuth", "failed"}, {"authenticationRequired", "notLoggedIn", "unauthorized"},
		{"starting", "bearerToken", "checking"}, {"disabled", "unknown", "disabled"},
	} {
		t.Run(test.runtime+"-"+test.auth, func(t *testing.T) {
			entry := codexEntry(codexStatus{Name: "sample", RuntimeStatus: test.runtime, AuthStatus: test.auth})
			if entry.Status != test.want {
				t.Fatalf("status = %s, want %s", entry.Status, test.want)
			}
		})
	}
}

func TestCacheReturnsWhileCheckingAndSharesOneCheck(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	cache := newCache(time.Minute, time.Second, func(ctx context.Context, key string) Snapshot {
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
	cache := newCache(time.Minute, 20*time.Millisecond, func(ctx context.Context, _ string) Snapshot {
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
