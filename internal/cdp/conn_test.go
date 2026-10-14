package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestCallReadsOnlyItsReplyAndReportsProtocolFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		replies []string
		want    string
		error   string
	}{
		{"result after events", []string{`{"method":"Page.loadEventFired"}`, `{"id":99,"result":{"value":"other"}}`, `{"id":1,"result":{"value":"ready"}}`}, "ready", ""},
		{"command refusal", []string{`{"id":1,"error":{"code":-32601,"message":"unknown command"}}`}, "", "unknown command"},
		{"invalid reply", []string{`not JSON`}, "", "not JSON"},
		{"invalid result", []string{`{"id":1,"result":{"value":42}}`}, "", "cannot unmarshal"},
		{"missing result", []string{`{"id":1}`}, "", "no result object"},
		{"null result", []string{`{"id":1,"result":null}`}, "", "no result object"},
		{"array result", []string{`{"id":1,"result":[]}`}, "", "no result object"},
		{"empty result object", []string{`{"id":1,"result":{}}`}, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			received := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				peer, err := websocket.Accept(writer, request, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer peer.CloseNow()
				_, data, err := peer.Read(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				received <- string(data)
				for _, reply := range test.replies {
					if err := peer.Write(ctx, websocket.MessageText, []byte(reply)); err != nil {
						t.Error(err)
						return
					}
				}
			}))
			defer server.Close()
			connection, err := Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			var result struct {
				Value string `json:"value"`
			}

			err = connection.Call(ctx, "Page.navigate", map[string]string{"url": "about:blank"}, &result)

			if test.error == "" && err != nil || test.error != "" && (err == nil || !strings.Contains(err.Error(), test.error)) {
				t.Fatalf("Call error = %v, want %q", err, test.error)
			}
			if result.Value != test.want {
				t.Fatalf("result = %q, want %q", result.Value, test.want)
			}
			var request struct {
				ID     int64             `json:"id"`
				Method string            `json:"method"`
				Params map[string]string `json:"params"`
			}
			select {
			case data := <-received:
				if err := json.Unmarshal([]byte(data), &request); err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if request.ID != 1 || request.Method != "Page.navigate" || request.Params["url"] != "about:blank" {
				t.Fatalf("request = %+v, want the requested command and parameters", request)
			}
			if test.name == "command refusal" {
				var refusal *Error
				if !errors.As(err, &refusal) || refusal.Code != -32601 || refusal.Method != "Page.navigate" {
					t.Fatalf("refusal = %v, want the typed protocol error", err)
				}
			}
		})
	}
}
