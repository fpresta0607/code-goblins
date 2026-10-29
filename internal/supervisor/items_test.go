package supervisor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

// itemsBoard is a board whose store holds items.
func itemsBoard(t *testing.T, items Items) *HTTP {
	t.Helper()
	handler, _ := orderBoard(t)
	store := handler.Service.Store
	store.mu.Lock()
	store.db.Questions = append(store.db.Questions, items.Questions...)
	store.db.Reviews = append(store.db.Reviews, items.Reviews...)
	store.db.Runs = append(store.db.Runs, items.Runs...)
	store.db.Credentials = append(store.db.Credentials, items.Credentials...)
	store.db.BrowserSignIns = append(store.db.BrowserSignIns, items.BrowserSignIns...)
	err := store.save()
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

// itemKeys names each item as the board's Command Center does.
func itemKeys(items Items) []string {
	keys := []string{}
	for _, question := range items.Questions {
		keys = append(keys, "question:"+question.ID)
	}
	for _, review := range items.Reviews {
		keys = append(keys, "review:"+review.ID)
	}
	for _, run := range items.Runs {
		keys = append(keys, "run:"+run.ID)
	}
	for _, request := range items.Credentials {
		keys = append(keys, "credential:"+request.ID)
	}
	for _, signin := range items.BrowserSignIns {
		keys = append(keys, "browser-signin:"+signin.ID)
	}
	slices.Sort(keys)
	return keys
}

func readClosedItems(t *testing.T, handler *HTTP, query string) Items {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/items/closed"+query, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/items/closed%s answered %d: %s", query, response.Code, response.Body)
	}
	var read Items
	if err := json.Unmarshal(response.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	return read
}

// #1611: every board's first snapshot carried each item the Command Center
// ever closed, 362 KB of a 501 KB snapshot on the live fleet, though History
// lists only its newest 20 and only once it opens.
func TestTheBoardIsSentTheItemsOpenOrClosedInTheLastHour(t *testing.T) {
	// Arrange: of every kind, an item open, one closed half an hour ago and
	// one closed two hours ago, and a credential card's terminal.
	now := time.Now().UTC()
	recent, old := now.Add(-30*time.Minute), now.Add(-2*time.Hour)
	handler := itemsBoard(t, Items{
		Questions: []Question{
			{ID: "open", Status: "pending", CreatedAt: old},
			{ID: "recent", Status: "succeeded", CreatedAt: old, AnsweredAt: &recent},
			{ID: "old", Status: "succeeded", CreatedAt: old, AnsweredAt: &old},
		},
		Reviews: []Review{
			{ID: "open", State: "open", CreatedAt: old, UpdatedAt: old},
			{ID: "recent", State: "answered", CreatedAt: old, UpdatedAt: recent},
			{ID: "old", State: "withdrawn", CreatedAt: old, UpdatedAt: old},
		},
		Runs: []Run{
			{ID: "open", State: "running", CreatedAt: old, RanAt: &old},
			{ID: "recent", State: "succeeded", CreatedAt: old, FinishedAt: &recent},
			{ID: "old", State: "failed", CreatedAt: old, FinishedAt: &old},
			{ID: "terminal", State: "succeeded", CreatedAt: old, FinishedAt: &recent, CredentialRequest: "recent"},
		},
		Credentials: []CredentialRequest{
			{ID: "open", State: "open", CreatedAt: old},
			{ID: "recent", State: "saved", CreatedAt: old, ClosedAt: &recent},
			{ID: "old", State: "done", CreatedAt: old, ClosedAt: &old},
		},
		BrowserSignIns: []BrowserSignIn{
			{ID: "open", State: "open", CreatedAt: old},
			{ID: "recent", State: "verified", CreatedAt: old, ClosedAt: &recent},
			{ID: "old", State: "expired", CreatedAt: old, ClosedAt: &old},
		},
	})

	// Act
	items, _ := handler.Service.Items()
	snapshot, err := handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	want := []string{"browser-signin:open", "browser-signin:recent", "credential:open", "credential:recent", "question:open", "question:recent", "review:open", "review:recent", "run:open", "run:recent", "run:terminal"}
	sent := Items{Questions: snapshot.Questions, Reviews: snapshot.Reviews, Runs: snapshot.Runs, Credentials: snapshot.Credentials, BrowserSignIns: snapshot.BrowserSignIns}
	for name, got := range map[string]Items{"items": items, "snapshot": sent} {
		if keys := itemKeys(got); !slices.Equal(keys, want) {
			t.Errorf("the board's %s carry %v, want %v", name, keys, want)
		}
	}
	if items.HistoryCount != 10 || snapshot.HistoryCount != 10 {
		t.Errorf("History counts %d in the items and %d in the snapshot, want the 10 closed items it lists", items.HistoryCount, snapshot.HistoryCount)
	}
}

func TestHistoryReadsTheNewestTwentyClosedItemsOfEveryKind(t *testing.T) {
	// Arrange: 26 items closed an hour apart, of three kinds in turn, beside
	// an open question and a credential card's terminal that ended last.
	now := time.Now().UTC()
	var items Items
	var want []string
	for i := range 26 {
		closed := now.Add(-time.Duration(i+1) * time.Hour)
		id := fmt.Sprintf("closed-%02d", i)
		switch i % 3 {
		case 0:
			items.Questions = append(items.Questions, Question{ID: id, Status: "succeeded", CreatedAt: closed.Add(-time.Minute), AnsweredAt: &closed})
			id = "question:" + id
		case 1:
			items.Reviews = append(items.Reviews, Review{ID: id, State: "answered", CreatedAt: closed.Add(-time.Minute), UpdatedAt: closed})
			id = "review:" + id
		default:
			items.Runs = append(items.Runs, Run{ID: id, State: "succeeded", CreatedAt: closed.Add(-time.Minute), FinishedAt: &closed})
			id = "run:" + id
		}
		if i < historyRows {
			want = append(want, id)
		}
	}
	items.Questions = append(items.Questions, Question{ID: "open", Status: "pending", CreatedAt: now.Add(-48 * time.Hour)})
	items.Runs = append(items.Runs, Run{ID: "terminal", State: "succeeded", CreatedAt: now.Add(-time.Minute), FinishedAt: &now, CredentialRequest: "card"})
	handler := itemsBoard(t, items)
	slices.Sort(want)

	// Act
	read := readClosedItems(t, handler, "")
	sent, _ := handler.Service.Items()

	// Assert
	if keys := itemKeys(read); !slices.Equal(keys, want) {
		t.Errorf("History read %v, want the 20 newest closed items %v", keys, want)
	}
	if sent.HistoryCount != historyRows {
		t.Errorf("History counts %d, want %d", sent.HistoryCount, historyRows)
	}
}

func TestAChangeAnswerReadsTheQuestionItNamesHoweverLongAgoItClosed(t *testing.T) {
	// Arrange: the question the CFO answered while the Overlord was away, a
	// day ago, with 25 answered since.
	now := time.Now().UTC()
	away := now.Add(-24 * time.Hour)
	questions := []Question{{ID: "afk-left-1", Status: "succeeded", Text: "Ship it?", CreatedAt: away, AnsweredAt: &away}}
	for i := range 25 {
		answered := now.Add(-time.Duration(i+1) * time.Minute)
		questions = append(questions, Question{ID: fmt.Sprintf("later-%02d", i), Status: "succeeded", CreatedAt: answered, AnsweredAt: &answered})
	}
	handler := itemsBoard(t, Items{Questions: questions})

	for _, tc := range []struct {
		name, question string
		want           []string
	}{
		{"the question it names", "afk-left-1", []string{"question:afk-left-1"}},
		{"none for a question the store does not hold", "gone", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			read := readClosedItems(t, handler, "?question="+tc.question)

			// Assert
			if keys := itemKeys(read); !slices.Equal(keys, tc.want) {
				t.Errorf("read %v, want %v", keys, tc.want)
			}
		})
	}
}
