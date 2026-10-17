package axi

import (
	"context"
	"os"
	"path/filepath"
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

// profileDir is the folder every lavish-axi call runs in.
func profileDir(t *testing.T) string {
	t.Helper()
	profile, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func TestLavishOpenReturnsThePageAddressWithoutABrowser(t *testing.T) {
	runner := &fakeRunner{result: execx.Result{Stdout: []byte(lavishOpened)}}

	url, err := (Lavish{Commands: runner}).Open(context.Background(), `C:\work\.lavish\plan.html`)

	if err != nil || url != "http://127.0.0.1:4387/session/c42fd9a0de5ec55d" {
		t.Fatalf("Open = %q, %v; want the session url", url, err)
	}
	assertLavishRequest(t, runner, execx.Request{Dir: profileDir(t), Name: "lavish-axi", Args: []string{`C:\work\.lavish\plan.html`, "--no-open"}})
}

// lavish-axi refuses to open again a page whose review the Overlord ended,
// unless it is told to reopen it, which only his asking for another look
// warrants.
func TestLavishReopenOpensAPageHisEndedReviewClosed(t *testing.T) {
	runner := &fakeRunner{result: execx.Result{Stdout: []byte(lavishOpened)}}

	_, err := (Lavish{Commands: runner}).Reopen(context.Background(), `C:\work\.lavish\plan.html`)

	if err != nil {
		t.Fatal(err)
	}
	assertRequest(t, runner, execx.Request{Dir: profileDir(t), Name: "lavish-axi", Args: []string{`C:\work\.lavish\plan.html`, "--no-open", "--reopen"}})
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
			assertLavishRequest(t, runner, execx.Request{Dir: profileDir(t), Name: "lavish-axi", Args: []string{"poll", `C:\work\.lavish\plan.html`, "--timeout-ms", "90000"}, KillTree: true})
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
	assertLavishRequest(t, runner, execx.Request{Dir: profileDir(t), Name: "lavish-axi", Args: []string{"poll", `C:\work\.lavish\plan.html`, "--agent-reply", reply, "--timeout-ms", "90000"}, KillTree: true})
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

// A poll says when he picked an option the page declared in its
// data-lavish-choices block, which Scrawl sends tagged choice: that pick
// answers the page. What he wrote himself, on the page or on an element, is
// no pick.
func TestLavishPollSaysWhenHePickedAnOptionThePageDeclared(t *testing.T) {
	for name, test := range map[string]struct {
		output   string
		isPicked bool
	}{
		"a declared choice":        {lavishChoice, true},
		"a message and a note":     {lavishTwoPrompts, false},
		"a table without its tags": {lavishFeedbackEnded, false},
		"no prompts":               {lavishWaiting, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runner := &fakeRunner{result: execx.Result{Stdout: []byte(test.output)}}

			// Act
			poll, err := (Lavish{Commands: runner}).Poll(context.Background(), `C:\work\.lavish\plan.html`, "", time.Second)

			// Assert
			if err != nil || poll.Picked != test.isPicked {
				t.Fatalf("Poll = %+v, %v; want picked %v", poll, err, test.isPicked)
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

// Ending a page ends its review session as an agent.
func TestLavishEndEndsThePagesSession(t *testing.T) {
	// Arrange
	runner := &fakeRunner{result: execx.Result{Stdout: []byte("session:\n  file: \"C:\\\\work\\\\.lavish\\\\plan.html\"\n  status: ended\n")}}

	// Act
	err := (Lavish{Commands: runner}).End(context.Background(), `C:\work\.lavish\plan.html`)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	assertLavishRequest(t, runner, execx.Request{Dir: profileDir(t), Name: "lavish-axi", Args: []string{"end", `C:\work\.lavish\plan.html`}})
}

// The sessions are read from lavish-axi's state file without running it, so
// reading takes nothing from a page: each session's key, page, status, who
// ended it and how many prompts wait on it undelivered, counted from the
// prompts themselves when the count lags them. No state file is no sessions.
func TestLavishSessionsReadsEverySessionWithoutTakingItsFeedback(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	t.Setenv("LAVISH_AXI_STATE_DIR", dir)
	none, noneErr := (Lavish{}).Sessions()
	stored := `{"sessions":{
		"b2":{"key":"b2","file":"C:\\work\\b.html","status":"ended","ended_by":"user","pending_prompts":0,"prompts":[],"chat":[{"role":"user","text":"done"}]},
		"a1":{"key":"a1","file":"C:\\work\\a.html","status":"feedback","pending_prompts":1,"prompts":[{"prompt":"Ship it"},{"prompt":"Bigger cards"}]}
	}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	sessions, err := (Lavish{}).Sessions()

	// Assert
	if noneErr != nil || len(none) != 0 {
		t.Fatalf("Sessions without a state file = %+v, %v; want none", none, noneErr)
	}
	want := []PageSession{{Key: "a1", File: `C:\work\a.html`, Status: "feedback", Pending: 2}, {Key: "b2", File: `C:\work\b.html`, Status: "ended", EndedBy: "user"}}
	if err != nil || !slices.Equal(sessions, want) {
		t.Fatalf("Sessions = %+v, %v; want %+v", sessions, err, want)
	}
}

// A Scrawl link names its session by the key its address ends with, on
// either of the addresses lavish-axi answers on; a link to no session it
// keeps names none.
func TestLavishSessionAtFindsTheSessionALinkNames(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	t.Setenv("LAVISH_AXI_STATE_DIR", dir)
	stored := `{"sessions":{"1a4d8f434fcca24b":{"key":"1a4d8f434fcca24b","file":"C:\\scratch\\review\\index.html","status":"ended","ended_by":"user"}}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	want := PageSession{Key: "1a4d8f434fcca24b", File: `C:\scratch\review\index.html`, Status: "ended", EndedBy: "user"}
	for link, isKept := range map[string]bool{
		"https://sermon.tailcc4238.ts.net:4388/session/1a4d8f434fcca24b": true,
		"http://127.0.0.1:4387/session/1a4d8f434fcca24b":                 true,
		"http://127.0.0.1:4387/session/0000000000000000":                 false,
		"http://127.0.0.1:4387/session/1a4d8f434fcca24b/export":          false,
		"https://example.com/plan":                                        false,
	} {
		// Act
		session, found, err := (Lavish{}).SessionAt(link)

		// Assert
		if err != nil || found != isKept || isKept && session != want {
			t.Errorf("SessionAt(%s) = %+v, %v, %v; want found %v", link, session, found, err, isKept)
		}
	}
}

// A state file lavish-axi left half written is an error, never no sessions.
func TestLavishSessionsRefusesAnUnreadableStateFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LAVISH_AXI_STATE_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"sessions":{"a1":{"file":`), 0o600); err != nil {
		t.Fatal(err)
	}

	if sessions, err := (Lavish{}).Sessions(); err == nil {
		t.Fatalf("Sessions = %+v, want an error for a half-written state file", sessions)
	}
}

// A return page's Change sends its question and the new answer tagged
// answer_change: the prompt is the answer, the text the question's ID. The
// poll reads each change once, in order, and what else he wrote is no change.
func TestLavishPollReadsEachAnswerChangeThePageSent(t *testing.T) {
	for name, test := range map[string]struct {
		output string
		want   []PageChange
	}{
		"two changes and a note": {"session:\n" +
			"  status: feedback\n" +
			"prompts[3]{uid,prompt,selector,tag,text}:\n" +
			"  \"1\",Delete now,form,answer_change,afk-left-20261015T051200.000000000Z\n" +
			"  \"2\",\"Wait, then delete it\\nafter a backup\",form,answer_change,notify-a-goblin-7\n" +
			"  \"3\",Looks good,\"\",message,Freeform message\n" +
			"next_step: \"Apply the feedback.\"\n", []PageChange{{Question: "afk-left-20261015T051200.000000000Z", Answer: "Delete now"}, {Question: "notify-a-goblin-7", Answer: "Wait, then delete it\nafter a backup"}}},
		"a declared choice":  {lavishChoice, nil},
		"a message and note": {lavishTwoPrompts, nil},
		"no prompts":         {lavishWaiting, nil},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runner := &fakeRunner{result: execx.Result{Stdout: []byte(test.output)}}

			// Act
			poll, err := (Lavish{Commands: runner}).Poll(context.Background(), `C:\work\.lavish\plan.html`, "", time.Second)

			// Assert
			if err != nil || !slices.Equal(poll.Changes, test.want) {
				t.Fatalf("Poll = %+v, %v; want changes %q", poll.Changes, err, test.want)
			}
		})
	}
}

// Assert the network override without ever printing the inherited environment.
func assertLavishRequest(t *testing.T, runner *fakeRunner, want execx.Request) {
	t.Helper()
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(runner.calls))
	}
	got := runner.calls[0]
	if !slices.Contains(got.Env, "LAVISH_AXI_HOST=127.0.0.1") {
		t.Error("Scrawl did not get its loopback-only setting")
	}
	for _, entry := range got.Env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "LAVISH_AXI_LINK_URL") || strings.EqualFold(key, "LAVISH_AXI_LINK_HOST") || strings.EqualFold(key, "LAVISH_AXI_ALLOWED_HOSTS") {
			t.Error("Scrawl inherited a network override", key)
		}
	}
	got.Env = nil
	safe := &fakeRunner{calls: []execx.Request{got}}
	assertRequest(t, safe, want)
}
