package axi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// These outputs have the shape lavish-axi 0.1.71 prints.
const (
	lavishOpened = "session:\n" +
		"  file: \"C:\\\\work\\\\.lavish\\\\plan.html\"\n" +
		"  url: \"http://127.0.0.1:4387/session/c42fd9a0de5ec55d\"\n" +
		"  status: opened\n" +
		"next_step: \"Do not respond to the user just yet. Now you must run `lavish-axi poll C:\\\\work\\\\.lavish\\\\plan.html`.\"\n"
	lavishWaiting = "session:\n" +
		"  file: \"C:\\\\work\\\\.lavish\\\\plan.html\"\n" +
		"  status: waiting\n" +
		"next_step: \"No user feedback arrived before the optional timeout.\"\n"
	lavishFeedbackEnded = "session:\r\n" +
		"  file: \"C:\\\\work\\\\.lavish\\\\plan.html\"\r\n" +
		"  status: feedback\r\n" +
		"  session_ended: true\r\n" +
		"  ended_by: user\r\n" +
		"prompts[1]{id,text}:\r\n" +
		"  p1,\"Ship option B, but keep the status: line short\"\r\n" +
		"next_step: \"Apply the feedback.\"\r\n"
)

func TestLavishOpenReturnsThePageAddressWithoutABrowser(t *testing.T) {
	runner := &fakeRunner{result: execx.Result{Stdout: []byte(lavishOpened)}}

	url, err := (Lavish{Commands: runner}).Open(context.Background(), `C:\work\.lavish\plan.html`)

	if err != nil || url != "http://127.0.0.1:4387/session/c42fd9a0de5ec55d" {
		t.Fatalf("Open = %q, %v; want the session url", url, err)
	}
	assertRequest(t, runner, execx.Request{Name: "lavish-axi", Args: []string{`C:\work\.lavish\plan.html`, "--no-open"}})
}

func TestLavishPollReadsTheSessionStatusAndKeepsTheOutput(t *testing.T) {
	for name, test := range map[string]struct {
		output string
		status string
		ended  bool
	}{
		"the timeout passed":             {lavishWaiting, "waiting", false},
		"feedback that ends the session": {lavishFeedbackEnded, "feedback", true},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &fakeRunner{result: execx.Result{Stdout: []byte(test.output)}}

			poll, err := (Lavish{Commands: runner}).Poll(context.Background(), `C:\work\.lavish\plan.html`, 90*time.Second)

			if err != nil || poll.Status != test.status || poll.Ended != test.ended || poll.Output != test.output {
				t.Fatalf("Poll = %+v, %v; want status %q, ended %v and the whole output", poll, err, test.status, test.ended)
			}
			assertRequest(t, runner, execx.Request{Name: "lavish-axi", Args: []string{"poll", `C:\work\.lavish\plan.html`, "--timeout-ms", "90000"}})
		})
	}
}

// Only the session object's own fields count: a prompt's text that looks
// like a field, or output with no session at all, reads as nothing.
func TestLavishReadsOnlyTheSessionObject(t *testing.T) {
	for name, output := range map[string]string{
		"no session object":         "status: feedback\nurl: \"http://127.0.0.1:4387/x\"\n",
		"fields only after it ends": "session:\n  file: \"x\"\nnext_step: \"go\"\nstatus: feedback\n",
	} {
		t.Run(name, func(t *testing.T) {
			runner := &fakeRunner{result: execx.Result{Stdout: []byte(output)}}
			if _, err := (Lavish{Commands: runner}).Poll(context.Background(), "x", time.Second); err == nil || !strings.Contains(err.Error(), "no session status") {
				t.Errorf("Poll = %v, want it refused for having no session status", err)
			}
			if _, err := (Lavish{Commands: runner}).Open(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "no address") {
				t.Errorf("Open = %v, want it refused for having no address", err)
			}
		})
	}
}
