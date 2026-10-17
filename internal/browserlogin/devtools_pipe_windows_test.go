package browserlogin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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

// A caller that gives up before its command is written, because its context
// was done when it called or ended while it waited its turn behind another
// write, writes nothing and leaves the pipe to every other caller: the pipe
// is the browser's one connection, shared by the bridge's every request and
// client. Closing it then answered every later request with a 503, as
// TestAClosedTabIsNoLongerListedOnceCloseTabReturns met once (#1020).
func TestACallerThatGivesUpBeforeItsWriteLeavesThePipeToTheOthers(t *testing.T) {
	for _, test := range []struct {
		name          string
		isBehindWrite bool
	}{
		{"its context was done when it called", false},
		{"its context ended while it waited its turn", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: a browser that answers every command, once it reads.
			commands, input := io.Pipe()
			output, replies := io.Pipe()
			writes := make(chan struct{}, 16)
			pipe := newDevToolsPipe(output, startedWriter{input, writes})
			defer pipe.close()
			defer commands.Close()
			defer replies.Close()
			reading := make(chan struct{})
			go func() {
				<-reading
				reader := bufio.NewReader(commands)
				for {
					data, err := reader.ReadBytes(0)
					if err != nil {
						return
					}
					var message devToolsMessage
					if json.Unmarshal(data[:len(data)-1], &message) != nil {
						return
					}
					fmt.Fprintf(replies, "{\"id\":%d,\"result\":{}}\x00", message.ID)
				}
			}()
			live, cancelLive := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancelLive()
			written := make(chan error, 1)
			if test.isBehindWrite {
				go func() { written <- pipe.call(live, "Browser.getVersion", nil, nil) }()
				select {
				case <-writes:
				case <-time.After(5 * time.Second):
					t.Fatal("the first command never started its write")
				}
			} else {
				close(reading)
			}
			gaveUp, giveUp := context.WithCancel(t.Context())
			defer giveUp()
			if !test.isBehindWrite {
				giveUp()
			}

			// Act
			waited := make(chan error, 1)
			go func() { waited <- pipe.call(gaveUp, "Target.getTargets", nil, nil) }()
			if test.isBehindWrite {
				time.Sleep(50 * time.Millisecond)
				giveUp()
				if err := <-waited; err == nil {
					t.Fatal("a caller that gave up behind another write passed")
				}
				close(reading)
				if err := <-written; err != nil {
					t.Fatalf("the command written first: %v", err)
				}
			} else if err := <-waited; err == nil {
				t.Fatal("a caller whose context was done passed")
			}
			err := pipe.call(live, "Browser.getVersion", nil, nil)

			// Assert
			if err != nil {
				t.Fatalf("a later caller met %v, want the pipe left to it", err)
			}
		})
	}
}

// startedWriter is a pipe's writer that says on started when each write
// begins.
type startedWriter struct {
	io.WriteCloser
	started chan<- struct{}
}

func (w startedWriter) Write(data []byte) (int, error) {
	w.started <- struct{}{}
	return w.WriteCloser.Write(data)
}

// A caller that finds the pipe closed is told what closed it, which the
// bridge's 503 then says, so a request refused for it names its cause: the
// one #1020 met said only that the browser did not answer.
func TestAClosedPipeSaysWhatClosedIt(t *testing.T) {
	for _, test := range []struct {
		name  string
		close func(pipe *devToolsPipe, replies io.WriteCloser)
		want  string
	}{
		{"the browser closed its end", func(_ *devToolsPipe, replies io.WriteCloser) { replies.Close() }, "the browser closed its end"},
		{"the browser sent what is not JSON", func(_ *devToolsPipe, replies io.WriteCloser) { io.WriteString(replies, "not json\x00") }, "not JSON"},
		{"the bridge closed it", func(pipe *devToolsPipe, _ io.WriteCloser) { pipe.close() }, "the bridge closed it"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			commands, input := io.Pipe()
			output, replies := io.Pipe()
			pipe := newDevToolsPipe(output, input)
			defer pipe.close()
			defer commands.Close()
			defer replies.Close()
			go io.Copy(io.Discard, commands)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			// Act
			test.close(pipe, replies)
			<-pipe.done
			err := pipe.call(ctx, "Browser.getVersion", nil, nil)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("call = %v, want it to say %q", err, test.want)
			}
		})
	}
}

// End gives a browser that closed the pipe once it read the ask to close
// closeWait to exit, and ends one it could not ask at once, so a call says
// errPipeClosed only when its command was written before the pipe closed. A
// call on a pipe a frame that was not JSON had closed, while the browser
// kept running, said it too, and End waited closeWait for a browser it
// never asked.
func TestOnlyACallWrittenBeforeThePipeClosedSaysTheBrowserTookIt(t *testing.T) {
	for _, test := range []struct {
		name      string
		isWritten bool
		want      string
	}{
		{"the pipe closed before the call", false, "not JSON"},
		{"the browser closed its end once it read the call", true, "the browser closed its end"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			commands, input := io.Pipe()
			output, replies := io.Pipe()
			pipe := newDevToolsPipe(output, input)
			defer pipe.close()
			defer commands.Close()
			defer replies.Close()
			if test.isWritten {
				go func() {
					if _, err := bufio.NewReader(commands).ReadBytes(0); err == nil {
						replies.Close()
					}
				}()
			} else {
				io.WriteString(replies, "not json\x00")
				<-pipe.done
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			// Act
			err := pipe.call(ctx, "Browser.close", nil, nil)

			// Assert
			if err == nil || errors.Is(err, errPipeClosed) != test.isWritten || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("call = %v, want errPipeClosed %t and it to say %q", err, test.isWritten, test.want)
			}
		})
	}
}
