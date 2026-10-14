package browserlogin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPrivateDevToolsPipeMatchesRepliesAndRejectsMalformedResults(t *testing.T) {
	for _, test := range []struct {
		name, reply string
		shouldPass  bool
	}{
		{"result", `"result":{"product":"stand-in"}`, true},
		{"empty result", `"result":{}`, true},
		{"missing result", `"unused":true`, false},
		{"null result", `"result":null`, false},
		{"error", `"error":{"code":-32000,"message":"refused"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			commands, input := io.Pipe()
			output, replies := io.Pipe()
			pipe := newDevToolsPipe(output, input)
			defer pipe.close()
			defer commands.Close()
			defer replies.Close()
			go func() {
				data, err := bufio.NewReader(commands).ReadBytes(0)
				if err != nil {
					return
				}
				var message devToolsMessage
				if json.Unmarshal(data[:len(data)-1], &message) != nil {
					return
				}
				fmt.Fprintf(replies, "{\"id\":99999,\"result\":{}}\x00{\"id\":%d,%s}\x00", message.ID, test.reply)
			}()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()

			err := pipe.call(ctx, "Browser.getVersion", nil, nil)

			if (err == nil) != test.shouldPass {
				t.Fatalf("pipe result = %v, want pass %t", err, test.shouldPass)
			}
		})
	}
}

func TestPrivateDevToolsPipeKeepsClientIDsAndNestedSessionsSeparate(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := &devToolsClient{ctx: ctx, cancel: cancel, root: "first-root", messages: make(chan devToolsMessage, 4)}
	second := &devToolsClient{ctx: ctx, cancel: cancel, root: "second-root", messages: make(chan devToolsMessage, 4)}
	pipe := &devToolsPipe{pending: map[int64]devToolsPending{
		1: {id: 7, method: "Target.attachToTarget", client: first},
		2: {id: 7, session: "second-child", client: second},
	}, sessions: map[string]*devToolsClient{"first-root": first, "second-root": second}}

	pipe.deliver(devToolsMessage{ID: 1, Result: json.RawMessage(`{"sessionId":"first-child"}`)})
	pipe.deliver(devToolsMessage{ID: 2, Result: json.RawMessage(`{}`)})
	pipe.deliver(devToolsMessage{Session: "first-root", Method: "Target.attachedToTarget", Params: json.RawMessage(`{"sessionId":"nested"}`)})
	pipe.deliver(devToolsMessage{Session: "nested", Method: "Runtime.consoleAPICalled"})
	pipe.deliver(devToolsMessage{Session: "unknown", Method: "must-not-arrive"})

	if reply := <-first.messages; reply.ID != 7 || reply.Session != "" {
		t.Fatalf("first reply = %+v", reply)
	}
	if reply := <-second.messages; reply.ID != 7 || reply.Session != "second-child" {
		t.Fatalf("second reply = %+v", reply)
	}
	if event := <-first.messages; event.Session != "" || !strings.HasPrefix(event.Method, "Target.") {
		t.Fatalf("root event = %+v", event)
	}
	if event := <-first.messages; event.Session != "nested" {
		t.Fatalf("nested event = %+v", event)
	}
	if len(first.messages) != 0 || len(second.messages) != 0 || pipe.sessions["first-child"] != first {
		t.Fatal("a message crossed client sessions")
	}
}

func TestPrivateDevToolsPipeCancellationInterruptsABlockedWrite(t *testing.T) {
	commands, input, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer commands.Close()
	defer input.Close()
	output, replies, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	pipe := newDevToolsPipe(output, input)
	defer pipe.close()
	defer replies.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	answer := make(chan error, 1)
	go func() {
		answer <- pipe.call(ctx, "Runtime.evaluate", map[string]string{"expression": strings.Repeat("x", 128<<10)}, nil)
	}()

	select {
	case err := <-answer:
		if err == nil {
			t.Fatal("a cancelled write passed")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt the blocked pipe write")
	}
}
