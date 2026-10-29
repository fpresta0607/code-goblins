package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// applied is held with c applied, as the board applies a change.
func applied(held any, c boardChange) (any, error) {
	switch {
	case c.Value != nil:
		var value any
		err := json.Unmarshal(c.Value, &value)
		return value, err
	case c.Keys != nil || c.Gone != nil:
		before, isObject := held.(map[string]any)
		if !isObject {
			return nil, fmt.Errorf("a change of keys reached %v, which is no object", held)
		}
		after := maps.Clone(before)
		for _, key := range c.Gone {
			delete(after, key)
		}
		for key, each := range c.Keys {
			value, err := applied(before[key], each)
			if err != nil {
				return nil, err
			}
			after[key] = value
		}
		return after, nil
	default:
		before, isList := held.([]any)
		if !isList {
			return nil, fmt.Errorf("a change of items reached %v, which is no list", held)
		}
		byID := map[string]any{}
		order := []string{}
		for _, item := range before {
			fields, isObject := item.(map[string]any)
			id, isNamed := fields["id"].(string)
			if !isObject || !isNamed {
				return nil, fmt.Errorf("a change of items reached the item %v, which has no id", item)
			}
			byID[id] = item
			order = append(order, id)
		}
		if c.Order != nil {
			order = c.Order
		}
		after := []any{}
		for _, id := range order {
			item, isHeld := byID[id]
			if each, isChanged := c.Items[id]; isChanged {
				value, err := applied(item, each)
				if err != nil {
					return nil, err
				}
				item = value
			} else if !isHeld {
				return nil, fmt.Errorf("the change names the item %s, which the board does not hold", id)
			}
			after = append(after, item)
		}
		return after, nil
	}
}

func decoded(t *testing.T, text string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAChangeCarriesOnlyWhatChangedAndGivesTheNextValue(t *testing.T) {
	tests := []struct {
		name, held, next, want string
	}{
		{"a key that changed deep down is sent alone", `{"a":1,"b":{"c":2,"d":4}}`, `{"a":1,"b":{"c":3,"d":4}}`, `{"keys":{"b":{"keys":{"c":{"value":3}}}}}`},
		{"a key that came is sent whole", `{"a":1}`, `{"a":1,"b":[1]}`, `{"keys":{"b":{"value":[1]}}}`},
		{"a key that went is named", `{"a":1,"b":2,"c":3}`, `{"a":1}`, `{"gone":["b","c"]}`},
		{"an item that changed is sent alone by its id", `[{"id":"x","n":1},{"id":"y","n":1}]`, `[{"id":"x","n":1},{"id":"y","n":2}]`, `{"items":{"y":{"keys":{"n":{"value":2}}}}}`},
		{"an item that came is sent whole with every id in order", `[{"id":"x"}]`, `[{"id":"y","n":1},{"id":"x"}]`, `{"order":["y","x"],"items":{"y":{"value":{"id":"y","n":1}}}}`},
		{"an item that went leaves every id in order", `[{"id":"x"},{"id":"y"}]`, `[{"id":"y"}]`, `{"order":["y"]}`},
		{"items that moved are sent as their order", `[{"id":"x"},{"id":"y"}]`, `[{"id":"y"},{"id":"x"}]`, `{"order":["y","x"]}`},
		{"a list whose items have no ids is sent whole", `[1,2]`, `[1,3]`, `{"value":[1,3]}`},
		{"a list whose ids repeat is sent whole", `[{"id":"x","n":1},{"id":"x","n":2}]`, `[{"id":"x","n":1},{"id":"x","n":3}]`, `{"value":[{"id":"x","n":1},{"id":"x","n":3}]}`},
		{"a list that emptied is sent whole", `[{"id":"x"}]`, `[]`, `{"value":[]}`},
		{"a list that was empty is sent whole", `null`, `[{"id":"x"}]`, `{"value":[{"id":"x"}]}`},
		{"a value of another kind is sent whole", `{"a":{"b":1}}`, `{"a":null}`, `{"keys":{"a":{"value":null}}}`},
		{"values holding JSON's own marks are split where they end", `{"a":"x,}]\"{:","b":[1,{"c":"]"}],"d":-1.5e3,"e":true}`, `{"a":"x,}]\"{:","b":[1,{"c":"]"}],"d":-1.5e3,"e":false}`, `{"keys":{"e":{"value":false}}}`},
		{"a key and an id written with escapes are the text they name", `{"a\u0026b":[{"n":1,"id":"x\"y"}]}`, `{"a\u0026b":[{"n":2,"id":"x\"y"}]}`, `{"keys":{"a\u0026b":{"items":{"x\"y":{"keys":{"n":{"value":2}}}}}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got, isChanged := changeOf(json.RawMessage(test.held), json.RawMessage(test.next))

			// Assert
			data, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if !isChanged || string(data) != test.want {
				t.Fatalf("changeOf(%s, %s) = %s, %t, want %s", test.held, test.next, data, isChanged, test.want)
			}
			if after, err := applied(decoded(t, test.held), got); err != nil || !reflect.DeepEqual(after, decoded(t, test.next)) {
				t.Fatalf("the change turned %s into %v (%v), want %s", test.held, after, err, test.next)
			}
		})
	}
}

func TestTheSameValueIsNoChange(t *testing.T) {
	if got, isChanged := changeOf(json.RawMessage(`{"a":[{"id":"x"}]}`), json.RawMessage(`{"a":[{"id":"x"}]}`)); isChanged {
		t.Fatalf("changeOf of a value and itself = %+v, want no change", got)
	}
}

// streamEvent is one event of a board's stream: its name, its size, and the
// snapshot the board holds once it took it in.
type streamEvent struct {
	name string
	size int
	data Snapshot
}

// openBoard opens one board's event stream and reports each event on the
// channel it returns with what the board holds after it: the snapshot it
// opened with, and each change applied to what it held. An event of another
// name changes nothing it holds.
func openBoard(t *testing.T, ctx context.Context, url string) <-chan streamEvent {
	t.Helper()
	request, _ := http.NewRequestWithContext(ctx, "GET", url+"/api/events", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	events := make(chan streamEvent, 64)
	go func() {
		defer close(events)
		reader := bufio.NewReaderSize(response.Body, 4<<20)
		var held any
		name := ""
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			if after, isName := strings.CutPrefix(line, "event: "); isName {
				name = after
				continue
			}
			data, isData := strings.CutPrefix(line, "data: ")
			if !isData {
				continue
			}
			var value any
			if json.Unmarshal([]byte(data), &value) != nil {
				return
			}
			switch name {
			case "snapshot":
				held = value
			case "change":
				var c boardChange
				if json.Unmarshal([]byte(data), &c) != nil {
					return
				}
				if held, err = applied(held, c); err != nil {
					t.Errorf("the board could not take the change %s: %v", data, err)
					return
				}
			}
			var snapshot Snapshot
			whole, _ := json.Marshal(held)
			if json.Unmarshal(whole, &snapshot) != nil {
				return
			}
			events <- streamEvent{name, len(data), snapshot}
		}
	}()
	return events
}

// nextEvent reads the stream's next event, or fails when none comes in time.
func nextEvent(t *testing.T, events <-chan streamEvent, what string) streamEvent {
	t.Helper()
	select {
	case event, isOpen := <-events:
		if !isOpen {
			t.Fatalf("the stream ended while the test waited for %s", what)
		}
		return event
	case <-time.After(time.Minute):
		t.Fatalf("the stream sent nothing while the test waited for %s", what)
		return streamEvent{}
	}
}

// boardFixture is a supervisor serving one board, whose store holds count
// questions waiting.
func boardFixture(t *testing.T, count int) (*Service, *Store, string) {
	t.Helper()
	store, _ := testStore(t)
	for i := range count {
		question := Question{ID: fmt.Sprintf("question-%03d", i), Identity: strings.Repeat("c", 64), Text: "Merge the train for the pull requests it holds?", Options: []string{"Yes", "No"}, CreatedAt: time.Now().UTC()}
		if err := store.acceptQuestion(question); err != nil {
			t.Fatal(err)
		}
	}
	s := &Service{Store: store, Instance: "test-instance", subscribers: map[chan struct{}]struct{}{}, done: make(chan struct{})}
	handler := NewHTTP(s, "", nil)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	handler.Host = strings.TrimPrefix(server.URL, "http://")
	return s, store, server.URL
}

// #1611: every open board was sent the whole fleet on every change, a
// megabyte, though most changes moved a clock or a meter.
func TestABuildThatChangedNothingSendsTheBoardOnlyItsTime(t *testing.T) {
	// Arrange: a board open with its first snapshot.
	s, _, url := boardFixture(t, 100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := openBoard(t, ctx, url)
	opened := nextEvent(t, events, "its first snapshot")

	// Act: the fleet is read again and nothing in it changed.
	s.publish(nil)

	// Assert
	built := nextEvent(t, events, "the next build")
	if built.name != "change" || built.size > 200 {
		t.Fatalf("the stream sent a %s of %d bytes for a fleet that did not change, want a change of 200 bytes at most (the snapshot was %d)", built.name, built.size, opened.size)
	}
	if !built.data.At.After(opened.data.At) || built.data.Revision <= opened.data.Revision || len(built.data.Questions) != 100 {
		t.Errorf("the board holds the time %s, the revision %d and %d questions, want a later time, a later revision and the 100 questions", built.data.At, built.data.Revision, len(built.data.Questions))
	}
}

func TestAnAnsweredItemReachesTheBoardAsThatItemAlone(t *testing.T) {
	// Arrange: a board open with its first snapshot, a hundred questions
	// waiting.
	s, store, url := boardFixture(t, 100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := openBoard(t, ctx, url)
	opened := nextEvent(t, events, "its first snapshot")

	// Act: one question is dismissed.
	if _, err := store.Queue(Action{ID: "dismiss-1", Kind: "question_clear", QuestionID: "question-042", Generation: strings.Repeat("c", 64)}); err != nil {
		t.Fatal(err)
	}
	s.publish(nil)

	// Assert
	answered := nextEvent(t, events, "the dismissed question")
	if answered.name != "change" || answered.size*20 > opened.size {
		t.Fatalf("the stream sent a %s of %d bytes for one dismissed question, want a change of a twentieth of the %d byte snapshot at most", answered.name, answered.size, opened.size)
	}
	statuses := map[string]string{}
	for _, question := range answered.data.Questions {
		statuses[question.ID] = question.Status
	}
	if len(statuses) != 100 || statuses["question-042"] != "cleared" || statuses["question-041"] != "pending" {
		t.Errorf("the board holds %d questions with question-042 %q and question-041 %q, want 100 with only question-042 cleared", len(statuses), statuses["question-042"], statuses["question-041"])
	}
	if !slices.ContainsFunc(answered.data.Actions, func(action Action) bool { return action.ID == "dismiss-1" }) {
		t.Errorf("the board holds the actions %+v, want the dismiss he sent", answered.data.Actions)
	}
}
