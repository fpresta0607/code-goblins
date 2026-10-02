package supervisor

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
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
	first, firstErr := store.claimAnnounced([]string{"alert:question:q1", "open:question:q1", "alert:question:q1"}, nil, now)
	again, againErr := store.claimAnnounced([]string{"alert:question:q1", "alert:question:q2"}, nil, now.Add(time.Second))
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	restarted, restartedErr := reopened.claimAnnounced([]string{"open:question:q1", "alert:question:q2", "alert:question:q3"}, nil, now.Add(2*time.Second))

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
	if _, err := store.claimAnnounced([]string{"alert:question:old", "alert:question:recent"}, nil, now.Add(-announcedFor-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.claimAnnounced([]string{"alert:question:recent"}, nil, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Act
	claimed, err := store.claimAnnounced([]string{"alert:question:old", "alert:question:fresh"}, nil, now)

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

// A goblin's news has no id of its own: its key is what it says. The same
// words within five minutes are one event, in whichever tab and through a
// restart, and the same words later are a new event, such as a gate asking
// for a second decision. An item's key is announced once however long ago.
func TestAGoblinsNewsIsAnnouncedAgainOnlyAfterItsWindow(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	now := time.Now().UTC()
	news := []string{"alert:task:a:g1:blocked:Pipeline decision required at review"}
	item := []string{"alert:question:q1"}

	// Act
	first, firstErr := store.claimAnnounced(item, news, now)
	flicker, flickerErr := store.claimAnnounced(item, news, now.Add(4*time.Minute))
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	restarted, restartedErr := reopened.claimAnnounced(nil, news, now.Add(sameEventFor-time.Second))
	later, laterErr := reopened.claimAnnounced(item, news, now.Add(sameEventFor))
	soonAfter, soonAfterErr := reopened.claimAnnounced(nil, news, now.Add(sameEventFor+time.Minute))

	// Assert
	if firstErr != nil || flickerErr != nil || restartedErr != nil || laterErr != nil || soonAfterErr != nil {
		t.Fatal(firstErr, flickerErr, restartedErr, laterErr, soonAfterErr)
	}
	if !slices.Equal(first, append(slices.Clone(item), news...)) {
		t.Errorf("first claim = %q, want the item and the news", first)
	}
	if len(flicker) != 0 || len(restarted) != 0 {
		t.Errorf("the same news within its window = %q, then %q after a restart, want neither announced again", flicker, restarted)
	}
	if !slices.Equal(later, news) {
		t.Errorf("the same news after its window = %q, want it announced as a new event and the item not", later)
	}
	if len(soonAfter) != 0 {
		t.Errorf("the news a minute after it was announced again = %q, want one event", soonAfter)
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
		{"a goblin's news with an item", `{"keys":["alert:question:q1"],"news":["alert:task:a:g1:done:pr7"]}`, 200, []string{"alert:task:a:g1:done:pr7"}},
		{"news with a tab, a non-breaking space, a line break and a joined emoji", `{"news":["alert:task:a:g1:failed:tests\tfailed twice\nsaid 👩‍💻"]}`, 200, []string{"alert:task:a:g1:failed:tests\tfailed twice\nsaid 👩‍💻"}},
		{"no keys", `{"keys":[]}`, 200, []string{}},
		{"an empty key", `{"keys":[""]}`, 400, nil},
		{"a key too long", `{"keys":["` + strings.Repeat("k", maxAnnounceKey+1) + `"]}`, 400, nil},
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
