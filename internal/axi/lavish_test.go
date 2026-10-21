package axi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	// The Code Goblins build 0.1.79-codegoblins.16 printed the prompts as a
	// list once one carried a table cell's target, as on 2026-10-18, when the
	// Overlord wrote on a cell of a review page's table and then sent a note
	// on the whole page with Send & End.
	lavishCellNote = "session:\n" +
		"  file: \"C:\\\\work\\\\.lavish\\\\plan.html\"\n" +
		"  status: feedback\n" +
		"  session_ended: true\n" +
		"  ended_by: user\n" +
		"prompts[2]:\n" +
		"  - uid: \"1\"\n" +
		"    prompt: then shouldnt this come first?\n" +
		"    selector: \"div:nth-of-type(2) > table > tbody > tr:nth-of-type(1) > td:nth-of-type(2)\"\n" +
		"    tag: td\n" +
		"    text: \"Ships after the security review; the launch waits on it\"\n" +
		"    target:\n" +
		"      type: table-cell\n" +
		"      selector: \"div:nth-of-type(2) > table > tbody > tr:nth-of-type(1) > td:nth-of-type(2)\"\n" +
		"      rowLabel: Step two\n" +
		"      columnLabel: WHEN\n" +
		"      text: \"Ships after the security review; the launch waits on it\"\n" +
		"  - uid: \"\"\n" +
		"    prompt: \"Ship the plan, with \\\"step two\\\" first\"\n" +
		"    selector: \"\"\n" +
		"    tag: message\n" +
		"    text: Freeform message\n" +
		"next_step: \"Apply the requested changes.\"\n"
	// The toon 2.3.1 encoder the Code Goblins build 0.1.79-codegoblins.17
	// prints with also makes his pick of a declared choice a list item when he
	// sends it with a note on a table cell.
	lavishPickWithACellNote = "session:\n" +
		"  file: \"C:\\\\work\\\\.lavish\\\\plan.html\"\n" +
		"  status: feedback\n" +
		"prompts[2]:\n" +
		"  - uid: \"1\"\n" +
		"    prompt: Build it as shown\n" +
		"    selector: \"script[data-lavish-choices]\"\n" +
		"    tag: choice\n" +
		"    text: Build it as shown?\n" +
		"  - uid: \"\"\n" +
		"    prompt: then shouldnt this come first?\n" +
		"    selector: \"table > tbody > tr:nth-of-type(1) > td:nth-of-type(2)\"\n" +
		"    tag: td\n" +
		"    text: Ships after the security review; the launch waits on it\n" +
		"    target:\n" +
		"      type: table-cell\n" +
		"      rowLabel: Step two\n" +
		"      columnLabel: WHEN\n" +
		"next_step: Apply the requested changes.\n"
	// Each other kind of note Scrawl sends carries more than a table holds,
	// nested its own way, as toon 2.3.1 prints them sent together: a text
	// selection's range, a Mermaid node, a batch of layout issues, a
	// whiteboard's scene, and the images he attached to a note on an element.
	lavishEveryKindOfNote = "session:\n" +
		"  file: \"C:\\\\work\\\\.lavish\\\\plan.html\"\n" +
		"  status: feedback\n" +
		"prompts[6]:\n" +
		"  - uid: \"\"\n" +
		"    prompt: say when it ships\n" +
		"    selector: \"main > p:nth-of-type(2)\"\n" +
		"    tag: text\n" +
		"    text: the launch waits\n" +
		"    target:\n" +
		"      type: text-range\n" +
		"      text: the launch waits\n" +
		"      selector: \"main > p:nth-of-type(2)\"\n" +
		"      commonAncestorSelector: \"main > p:nth-of-type(2)\"\n" +
		"      start:\n" +
		"        path[2]: 0,1\n" +
		"        offset: 4\n" +
		"      end:\n" +
		"        path[2]: 0,1\n" +
		"        offset: 20\n" +
		"  - uid: m1\n" +
		"    prompt: rename it Release\n" +
		"    selector: svg g.node\n" +
		"    tag: g\n" +
		"    text: Build\n" +
		"    target:\n" +
		"      type: mermaid-node\n" +
		"      diagramId: d1\n" +
		"      nodeId: B\n" +
		"      label: Build\n" +
		"      selector: svg g.node\n" +
		"  - uid: \"\"\n" +
		"    prompt: fix these\n" +
		"    selector: \"\"\n" +
		"    tag: layout-warnings\n" +
		"    text: 2 layout issues\n" +
		"    target:\n" +
		"      type: layout-warnings\n" +
		"      warnings[2]{id,rule,selector,component,axis,overflow_px,viewport_class,viewport_width,status,last_seen_at}:\n" +
		"        w1,overflow,div.card,Card,horizontal,12,phone,390,open,\"2026-10-19T00:00:00Z\"\n" +
		"        w2,clip,div.price,\"\",vertical,3.5,desktop,1440,open,\"2026-10-19T00:00:01Z\"\n" +
		"      artifact_revision: 3\n" +
		"  - uid: \"\"\n" +
		"    prompt: Moved Review below Build\n" +
		"    selector: \"\"\n" +
		"    tag: whiteboard\n" +
		"    text: Diagram 1\n" +
		"    target:\n" +
		"      type: excalidraw-scene\n" +
		"      diagramIndex: 0\n" +
		"      diagramId: d1\n" +
		"      sourceHash: 5d41402a\n" +
		"      scenePath: \"C:\\\\work\\\\.lavish\\\\d1.excalidraw\"\n" +
		"      previewPath: \"C:\\\\work\\\\.lavish\\\\d1.png\"\n" +
		"      imageFallback: false\n" +
		"      stats:\n" +
		"        added: 0\n" +
		"        removed: 0\n" +
		"        moved: 1\n" +
		"  - uid: u7\n" +
		"    prompt: make it bigger\n" +
		"    selector: h2\n" +
		"    tag: h2\n" +
		"    text: Pricing\n" +
		"    attachments[2]:\n" +
		"      - id: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png\n" +
		"        path: \"C:\\\\work\\\\a.png\"\n" +
		"        mime: image/png\n" +
		"        bytes: 1234\n" +
		"        width: 100\n" +
		"        height: 50\n" +
		"      - id: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.png\n" +
		"        name: after.png\n" +
		"        path: \"C:\\\\work\\\\b.png\"\n" +
		"        mime: image/png\n" +
		"        bytes: 2345\n" +
		"        width: 200\n" +
		"        height: 100\n" +
		"  - uid: \"\"\n" +
		"    prompt: Ship it after these\n" +
		"    selector: \"\"\n" +
		"    tag: message\n" +
		"    text: Freeform message\n" +
		"next_step: Apply the requested changes.\n"
	// Images that share their fields, each with the name he gave it, toon
	// 2.3.1 prints as a table: two on a note on an element, and one on a note
	// on the whole page that has no words.
	lavishImagesInATable = "session:\n" +
		"  file: \"C:\\\\work\\\\.lavish\\\\plan.html\"\n" +
		"  status: feedback\n" +
		"prompts[2]:\n" +
		"  - uid: u7\n" +
		"    prompt: make it look like these\n" +
		"    selector: h2\n" +
		"    tag: h2\n" +
		"    text: Pricing\n" +
		"    attachments[2]{id,type,path,mime,bytes,width,height,name}:\n" +
		"      aaaa.png,image,\"C:\\\\work\\\\.lavish-axi\\\\attachments\\\\c42fd9a0de5ec55d\\\\aaaa.png\",image/png,1234,100,50,\"before, cropped.png\"\n" +
		"      bbbb.png,image,\"C:\\\\work\\\\.lavish-axi\\\\attachments\\\\c42fd9a0de5ec55d\\\\bbbb.png\",image/png,1234,100,50,after.png\n" +
		"  - uid: \"\"\n" +
		"    prompt: \"\"\n" +
		"    selector: \"\"\n" +
		"    tag: message\n" +
		"    text: Freeform message\n" +
		"    attachments[1]{id,type,path,mime,bytes,width,height,name}:\n" +
		"      aaaa.png,image,\"C:\\\\work\\\\.lavish-axi\\\\attachments\\\\c42fd9a0de5ec55d\\\\aaaa.png\",image/png,1234,100,50,\"before, cropped.png\"\n" +
		"next_step: Apply the requested changes.\n"
	// The Code Goblins build 0.1.79-codegoblins.16 prints this, exiting 0,
	// when it is asked without --reopen to open a page whose review the
	// Overlord ended (its createUserEndedOpenOutput).
	lavishUserEnded = "session:\n" +
		"  file: \"C:\\\\work\\\\.lavish\\\\plan.html\"\n" +
		"  url: \"http://127.0.0.1:4387/session/c42fd9a0de5ec55d\"\n" +
		"  status: user-ended\n" +
		"next_step: \"The user explicitly ended this Lavish Editor session from the browser, so `lavish-axi C:\\\\work\\\\.lavish\\\\plan.html` did not reopen it.\"\n"
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

// lavish-axi answers a page whose review the Overlord ended with that
// session's address, as a success: Open refuses it, naming the address, so
// no card opens a page that says the review ended.
func TestLavishOpenRefusesAPageWhoseReviewHeEnded(t *testing.T) {
	// Arrange
	runner := &fakeRunner{result: execx.Result{Stdout: []byte(lavishUserEnded)}}

	// Act
	url, err := (Lavish{Commands: runner}).Open(context.Background(), `C:\work\.lavish\plan.html`)

	// Assert
	if !errors.Is(err, ErrSessionEnded) || url != "" || !strings.Contains(err.Error(), "http://127.0.0.1:4387/session/c42fd9a0de5ec55d") {
		t.Fatalf("Open = %q, %v; want ErrSessionEnded naming the session's address", url, err)
	}
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
	assertLavishRequest(t, runner, execx.Request{Dir: profileDir(t), Name: "lavish-axi", Args: []string{`C:\work\.lavish\plan.html`, "--no-open", "--reopen"}})
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

// A poll's prompts are what the Overlord wrote on the page, read from
// lavish-axi's prompts in either form it prints them, a table or a list,
// quoted or not, so a board can say what he answered; a table without the
// prompt column reads as none, and the whole output is still kept.
func TestLavishPollReadsWhatTheOverlordWrote(t *testing.T) {
	for name, test := range map[string]struct {
		output string
		want   []string
	}{
		"two prompts, one quoted":   {lavishTwoPrompts, []string{"LOVE EVERY BIT OF IT", `Make it bigger, then "ship" it`}},
		"a declared choice":         {lavishChoice, []string{"Use tabs for each section"}},
		"prompts printed as a list": {lavishCellNote, []string{"then shouldnt this come first?", `Ship the plan, with "step two" first`}},
		"no prompt column":          {lavishFeedbackEnded, nil},
		"no prompts":                {lavishWaiting, nil},
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

// Each note he sent keeps what it is attached to, in the order he wrote them:
// an element by its tag, selector and text, a table cell by its row and
// column too, and a note from the page's message box as one on the whole
// page, so whoever reads his set reads each note where he put it. A note
// keeps the local path of each image he attached to it, in either form
// lavish-axi prints them, with words or without. What a note carries nested
// deeper takes none of those fields and ends no note.
func TestLavishPollKeepsWhatEachNoteIsAttachedTo(t *testing.T) {
	cell := "div:nth-of-type(2) > table > tbody > tr:nth-of-type(1) > td:nth-of-type(2)"
	attachments := `C:\work\.lavish-axi\attachments\c42fd9a0de5ec55d\`
	for name, test := range map[string]struct {
		output string
		want   []PageNote
	}{
		"a cell and the whole page, as a list": {lavishCellNote, []PageNote{
			{Prompt: "then shouldnt this come first?", Tag: "td", Selector: cell, Text: "Ships after the security review; the launch waits on it", Row: "Step two", Column: "WHEN"},
			{Prompt: `Ship the plan, with "step two" first`, Tag: "message", Text: "Freeform message"},
		}},
		"the page and an element, as a table": {lavishTwoPrompts, []PageNote{
			{Prompt: "LOVE EVERY BIT OF IT", Tag: "message", Text: "Freeform message"},
			{Prompt: `Make it bigger, then "ship" it`, Tag: "element", Selector: "h1", Text: "Heading"},
		}},
		"every other kind of note, as a list": {lavishEveryKindOfNote, []PageNote{
			{Prompt: "say when it ships", Tag: "text", Selector: "main > p:nth-of-type(2)", Text: "the launch waits"},
			{Prompt: "rename it Release", Tag: "g", Selector: "svg g.node", Text: "Build"},
			{Prompt: "fix these", Tag: "layout-warnings", Text: "2 layout issues"},
			{Prompt: "Moved Review below Build", Tag: "whiteboard", Text: "Diagram 1"},
			{Prompt: "make it bigger", Tag: "h2", Selector: "h2", Text: "Pricing", Images: []string{`C:\work\a.png`, `C:\work\b.png`}},
			{Prompt: "Ship it after these", Tag: "message", Text: "Freeform message"},
		}},
		"images in a table, with words and without": {lavishImagesInATable, []PageNote{
			{Prompt: "make it look like these", Tag: "h2", Selector: "h2", Text: "Pricing", Images: []string{attachments + "aaaa.png", attachments + "bbbb.png"}},
			{Prompt: "", Tag: "message", Text: "Freeform message", Images: []string{attachments + "aaaa.png"}},
		}},
		"no prompts": {lavishWaiting, nil},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runner := &fakeRunner{result: execx.Result{Stdout: []byte(test.output)}}

			// Act
			poll, err := (Lavish{Commands: runner}).Poll(context.Background(), `C:\work\.lavish\plan.html`, "", time.Second)

			// Assert
			if err != nil || !reflect.DeepEqual(poll.Notes, test.want) {
				t.Fatalf("Poll notes = %+v, %v; want %+v", poll.Notes, err, test.want)
			}
		})
	}
}

// A poll says when he picked an option the page declared in its
// data-lavish-choices block, which Scrawl sends tagged choice: that pick
// answers the page, in either form lavish-axi prints the prompts. What he
// wrote himself, on the page or on an element, is no pick.
func TestLavishPollSaysWhenHePickedAnOptionThePageDeclared(t *testing.T) {
	for name, test := range map[string]struct {
		output   string
		isPicked bool
	}{
		"a declared choice":                       {lavishChoice, true},
		"a declared choice and a cell, as a list": {lavishPickWithACellNote, true},
		"a cell and the whole page, as a list":    {lavishCellNote, false},
		"a message and a note":                    {lavishTwoPrompts, false},
		"a table without its tags":                {lavishFeedbackEnded, false},
		"no prompts":                              {lavishWaiting, false},
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
		"https://pc.example.ts.net:4388/session/1a4d8f434fcca24b": true,
		"http://127.0.0.1:4387/session/1a4d8f434fcca24b":          true,
		"http://127.0.0.1:4387/session/0000000000000000":          false,
		"http://127.0.0.1:4387/session/1a4d8f434fcca24b/export":   false,
		"https://example.com/plan":                                false,
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
