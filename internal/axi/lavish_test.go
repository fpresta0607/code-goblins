package axi

import (
	"context"
	"slices"
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
	lavishTwoPrompts = "session:\r\n" +
		"  status: feedback\r\n" +
		"prompts[2]{uid,prompt,selector,tag,text}:\r\n" +
		"  \"\",LOVE EVERY BIT OF IT,\"\",message,Freeform message\r\n" +
		"  u7,\"Make it bigger, then \\\"ship\\\" it\",h1,element,Heading\r\n" +
		"next_step: \"Apply the feedback.\"\r\n"
	// The Code Goblins build 0.1.79-codegoblins.3 printed this, live, when the
	// Overlord picked an option a Scrawl page declared in its
	// data-lavish-choices block (2026-10-05): the prompt is the option's exact
	// text, and the text column is the question.
	lavishChoice = "session:\n" +
		"  file: \"C:\\\\work\\\\.lavish\\\\plan.html\"\n" +
		"  status: feedback\n" +
		"prompts[1]{uid,prompt,selector,tag,text}:\n" +
		"  \"1\",Use tabs for each section,\"script[data-lavish-choices]\",choice,Which layout should the settings page use?\n" +
		"next_step: \"Apply the requested changes.\"\n"
	lavishEndedByAgent = "session:\n" +
		"  file: \"C:\\\\work\\\\.lavish\\\\plan.html\"\n" +
		"  status: ended\n" +
		"  ended_by: agent\n" +
		"next_step: \"The agent ended the session.\"\n"
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
		output  string
		status  string
		ended   bool
		endedBy string
	}{
		"the timeout passed":             {lavishWaiting, "waiting", false, ""},
		"feedback that ends the session": {lavishFeedbackEnded, "feedback", true, "user"},
		"a session its agent ended":      {lavishEndedByAgent, "ended", false, "agent"},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &fakeRunner{result: execx.Result{Stdout: []byte(test.output)}}

			poll, err := (Lavish{Commands: runner}).Poll(context.Background(), `C:\work\.lavish\plan.html`, "", 90*time.Second)

			if err != nil || poll.Status != test.status || poll.Ended != test.ended || poll.EndedBy != test.endedBy || poll.Output != test.output {
				t.Fatalf("Poll = %+v, %v; want status %q, ended %v by %q and the whole output", poll, err, test.status, test.ended, test.endedBy)
			}
			assertRequest(t, runner, execx.Request{Name: "lavish-axi", Args: []string{"poll", `C:\work\.lavish\plan.html`, "--timeout-ms", "90000"}, KillTree: true})
		})
	}
}

// A poll can carry a reply the Overlord reads in the page's conversation
// panel, such as that his revision was received and what happens next.
func TestLavishPollCarriesAReplyToThePage(t *testing.T) {
	// Arrange
	runner := &fakeRunner{result: execx.Result{Stdout: []byte(lavishWaiting)}}
	reply := "Revision received. task-1 makes the next version, which replaces this page."

	// Act
	_, err := (Lavish{Commands: runner}).Poll(context.Background(), `C:\work\.lavish\plan.html`, reply, 90*time.Second)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	assertRequest(t, runner, execx.Request{Name: "lavish-axi", Args: []string{"poll", `C:\work\.lavish\plan.html`, "--agent-reply", reply, "--timeout-ms", "90000"}, KillTree: true})
}

// A poll's prompts are what the Overlord wrote on the page, read from the
// prompt column of lavish-axi's prompt table, quoted or not, so a board can
// say what he answered; a table without that column, or prompts given as a
// list, read as none, and the whole output is still kept.
func TestLavishPollReadsWhatTheOverlordWrote(t *testing.T) {
	for name, test := range map[string]struct {
		output string
		want   []string
	}{
		"two prompts, one quoted": {lavishTwoPrompts, []string{"LOVE EVERY BIT OF IT", `Make it bigger, then "ship" it`}},
		"a declared choice":       {lavishChoice, []string{"Use tabs for each section"}},
		"no prompt column":        {lavishFeedbackEnded, nil},
		"no prompts":              {lavishWaiting, nil},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &fakeRunner{result: execx.Result{Stdout: []byte(test.output)}}

			poll, err := (Lavish{Commands: runner}).Poll(context.Background(), `C:\work\.lavish\plan.html`, "", time.Second)

			if err != nil || !slices.Equal(poll.Prompts, test.want) {
				t.Fatalf("Poll = %+v, %v; want prompts %q", poll, err, test.want)
			}
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
			if _, err := (Lavish{Commands: runner}).Poll(context.Background(), "x", "", time.Second); err == nil || !strings.Contains(err.Error(), "no session status") {
				t.Errorf("Poll = %v, want it refused for having no session status", err)
			}
			if _, err := (Lavish{Commands: runner}).Open(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "no address") {
				t.Errorf("Open = %v, want it refused for having no address", err)
			}
		})
	}
}
