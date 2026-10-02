package supervisor

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// The Overlord, 2026-10-01: "fix the issue of idempotent cleared notification
// so no double fire or display happens from command center". A key the board
// announced is handed out once, to one request, and stays announced through a
// supervisor restart, so a reload, another tab or a restart never toasts,
// notifies or opens the same item again.
func TestAnAnnouncedKeyIsHandedOutOnceAndStaysAnnouncedAcrossARestart(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	now := time.Now().UTC()

	// Act
	first, firstErr := store.claimAnnounced([]string{"alert:question:q1", "open:question:q1", "alert:question:q1"}, now)
	again, againErr := store.claimAnnounced([]string{"alert:question:q1", "alert:question:q2"}, now.Add(time.Second))
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	restarted, restartedErr := reopened.claimAnnounced([]string{"open:question:q1", "alert:question:q2", "alert:question:q3"}, now.Add(2*time.Second))

	// Assert
	if firstErr != nil || againErr != nil || restartedErr != nil {
		t.Fatal(firstErr, againErr, restartedErr)
	}
	if !slices.Equal(first, []string{"alert:question:q1", "open:question:q1"}) {
		t.Errorf("first claim = %q, want each new key once", first)
	}
	if !slices.Equal(again, []string{"alert:question:q2"}) {
		t.Errorf("second claim = %q, want only the key nobody announced", again)
	}
	if !slices.Equal(restarted, []string{"alert:question:q3"}) {
		t.Errorf("claim after a restart = %q, want only the key nobody announced", restarted)
	}
}

// An old announcement is forgotten, so the record stays bounded; an item open
// that long was announced long ago.
func TestAnAnnouncementIsForgottenOnceItIsOld(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	now := time.Now().UTC()
	if _, err := store.claimAnnounced([]string{"alert:question:old", "alert:question:recent"}, now.Add(-announcedFor-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.claimAnnounced([]string{"alert:question:recent"}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Act
	claimed, err := store.claimAnnounced([]string{"alert:question:old", "alert:question:fresh"}, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(claimed, []string{"alert:question:old", "alert:question:fresh"}) {
		t.Errorf("claim = %q, want the forgotten key handed out again", claimed)
	}
	if got := len(store.Snapshot().Announced); got != 3 {
		t.Errorf("record holds %d announcements, want 3 (the old one replaced, the recent one kept)", got)
	}
}

// POST /api/announce answers with the keys this request claimed, and refuses a
// request it cannot take.
func TestTheAnnounceEndpointHandsEachKeyToOneRequest(t *testing.T) {
	store, _ := testStore(t)
	handler := NewHTTP(&Service{Store: store, Instance: "test-instance"}, "board.local", nil)
	post := func(body string) (int, []string) {
		request := httptest.NewRequest("POST", "http://board.local/api/announce", strings.NewReader(body))
		request.Header.Set("Origin", "http://board.local")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CFO-Token", "test-instance")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var result struct {
			Claimed []string `json:"claimed"`
		}
		if response.Code == 200 {
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err, response.Body.String())
			}
		}
		return response.Code, result.Claimed
	}
	tooMany := `{"keys":["k` + strings.Repeat(`","k`, maxAnnounceKeys) + `"]}`
	for _, test := range []struct {
		name    string
		body    string
		code    int
		claimed []string
	}{
		{"new keys", `{"keys":["alert:question:q1","open:question:q1"]}`, 200, []string{"alert:question:q1", "open:question:q1"}},
		{"the same keys from another tab", `{"keys":["alert:question:q1","open:question:q1"]}`, 200, []string{}},
		{"no keys", `{"keys":[]}`, 200, []string{}},
		{"an empty key", `{"keys":[""]}`, 400, nil},
		{"a key too long", `{"keys":["` + strings.Repeat("k", maxAnnounceKey+1) + `"]}`, 400, nil},
		{"a key with a line break", `{"keys":["alert:question:q1\nq2"]}`, 400, nil},
		{"too many keys", tooMany, 400, nil},
		{"not JSON", `keys`, 400, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			code, claimed := post(test.body)

			// Assert
			if code != test.code || test.code == 200 && !slices.Equal(claimed, test.claimed) {
				t.Errorf("POST /api/announce = %d %q, want %d %q", code, claimed, test.code, test.claimed)
			}
		})
	}
}

// A goblin's alert is its report: the snapshot names when the goblin last
// reported, so the board keys a goblin's news on that report and a phase that
// flips back and forth without a new report is never news again.
func TestTheSnapshotNamesWhenAGoblinLastReported(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store}
	reported := func() *time.Time {
		t.Helper()
		snapshot, err := s.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range snapshot.Tasks {
			if task.ID == "task-1" {
				return task.ReportedAt
			}
		}
		t.Fatal("task-1 missing from the snapshot")
		return nil
	}
	before := reported()
	sent := time.Now().UTC().Truncate(time.Second)

	// Act
	if err := state.AppendStatus(h.State, "task-1", "blocked: Which store?"); err != nil {
		t.Fatal(err)
	}
	after := reported()

	// Assert
	if before != nil {
		t.Errorf("before any report: reported_at = %v, want none", before)
	}
	if after == nil || after.Before(sent) || after.After(time.Now().UTC().Add(time.Second)) {
		t.Errorf("after a report at %v: reported_at = %v, want that report's time", sent, after)
	}
}
