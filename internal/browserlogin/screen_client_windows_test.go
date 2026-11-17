package browserlogin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// A client of this process's own reaches a page of the browser it started,
// and the page's screencast frames arrive on its events, each after the one
// before was acknowledged.
func TestAnOwnClientShowsAPageAndItsFrames(t *testing.T) {
	// Arrange
	process, _ := startChrome(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	client, err := process.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var created struct {
		Target string `json:"targetId"`
	}
	if err := client.Call(ctx, "", "Target.createTarget", map[string]string{"url": "data:text/html,<h1>Screen</h1>"}, &created); err != nil {
		t.Fatal(err)
	}
	var attached struct {
		Session string `json:"sessionId"`
	}
	if err := client.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": created.Target, "flatten": true}, &attached); err != nil {
		t.Fatal(err)
	}

	// Act
	startErr := client.Call(ctx, attached.Session, "Page.startScreencast", map[string]any{"format": "jpeg", "quality": 50}, nil)
	frames := []int{}
	for len(frames) < 2 && ctx.Err() == nil {
		select {
		case event := <-client.Events():
			if event.Method != "Page.screencastFrame" || event.Session != attached.Session {
				continue
			}
			var frame struct {
				Data    string `json:"data"`
				Session int    `json:"sessionId"`
			}
			if err := json.Unmarshal(event.Params, &frame); err != nil || frame.Data == "" {
				t.Fatalf("frame = %s, %v", event.Params, err)
			}
			frames = append(frames, frame.Session)
			if err := client.Call(ctx, attached.Session, "Page.screencastFrameAck", map[string]int{"sessionId": frame.Session}, nil); err != nil {
				t.Fatal(err)
			}
			// A frame comes when the page changes; this one gives the next.
			_ = client.Call(ctx, attached.Session, "Runtime.evaluate", map[string]string{"expression": "document.body.style.background = document.body.style.background === 'red' ? 'blue' : 'red'"}, nil)
		case <-ctx.Done():
		}
	}
	stranger := client.Call(ctx, "not-this-clients-session", "Page.navigate", map[string]string{"url": "about:blank"}, nil)

	// Assert
	if startErr != nil {
		t.Fatal(startErr)
	}
	if len(frames) != 2 {
		t.Fatalf("frames = %v, want two, each after the last was acknowledged", frames)
	}
	if stranger == nil {
		t.Fatal("a session of no client's ran a command")
	}
}

// A command its relay refuses is answered with the refusal, as the
// browser's error on the client's own command, and the client goes on.
func TestARelayedClientsRefusedCommandIsAnsweredWithItsReason(t *testing.T) {
	// Arrange
	process, _ := startChrome(t, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		process.ServeClient(w, r, func(command Command) string {
			if command.Method == "Page.navigate" {
				return "A person is driving this browser."
			}
			return ""
		})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	reply := func(id int64) devToolsMessage {
		for {
			var message devToolsMessage
			if err := wsjson.Read(ctx, connection, &message); err != nil {
				t.Fatal(err)
			}
			if message.ID == id {
				return message
			}
		}
	}

	// Act
	if err := wsjson.Write(ctx, connection, devToolsMessage{ID: 1, Method: "Page.navigate", Session: "goblins-page", Params: json.RawMessage(`{"url":"about:blank"}`)}); err != nil {
		t.Fatal(err)
	}
	refused := reply(1)
	if err := wsjson.Write(ctx, connection, devToolsMessage{ID: 2, Method: "Browser.getVersion"}); err != nil {
		t.Fatal(err)
	}
	answered := reply(2)

	// Assert
	var refusal struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(refused.Error, &refusal); err != nil || refusal.Code != refusalCode || refusal.Message != "A person is driving this browser." || refused.Session != "goblins-page" {
		t.Fatalf("refused = %+v", refused)
	}
	if len(answered.Error) != 0 || len(answered.Result) == 0 {
		t.Fatalf("the next command = %+v, want the browser's answer", answered)
	}
}
