package main

import (
	"bytes"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A card in his Command Center asks him something or lets him act. A wait on
// him that names nothing he acts on there goes to the CEO alone, its line
// saying it has no card, and the goblin is told how to put the step itself in
// front of him; a wait that names the page he takes it on is a card. On
// 2026-10-18 he met waits such as a goblin's need for two grants as cards with
// nothing to press but Skip.
func TestNotifyWaitThatNamesNothingHeActsOnIsNoCard(t *testing.T) {
	for name, c := range map[string]struct {
		args   []string
		isCard bool
	}{
		"nothing he acts on":    {[]string{"g1", "--waiting-on", "overlord", "Production steps 1, 2 and 4 need the database and bank grants"}, false},
		"the page he goes to":   {[]string{"g1", "--waiting-on", "overlord", "Add the two DNS records", "--link", "https://dash.cloudflare.com/acme/dns"}, true},
		"a decision it follows": {[]string{"g1", "--waiting-on", "overlord", "Waiting for your answer on the store", "--follows", "notify-g1-3"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			stateDir := questionHome(t)
			nativeGoblin(t, stateDir, "g1")
			if slices.Contains(c.args, "--follows") {
				decisionsForG1["the goblin's own question"](t, stateDir)
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runNotify(c.args, &stdout, &stderr, defaultCommandRuntime())

			// Assert
			if exit != 0 || stderr.Len() != 0 {
				t.Fatalf("exit=%d stderr=%q, want the wait taken", exit, stderr.String())
			}
			records, err := wake.Pending(stateDir)
			if err != nil || len(records) != 1 || !strings.HasPrefix(records[0].Detail, "waiting on overlord: ") {
				t.Fatalf("wake records = %+v %v, want the CEO woken with the wait", records, err)
			}
			waits := waitsInTheInbox(t, stateDir)
			if c.isCard {
				if len(waits) != 1 || strings.Contains(records[0].Detail, "no card") || strings.Contains(stdout.String(), "No card went") {
					t.Errorf("waits = %+v, wake = %q, stdout = %q, want the wait as one card", waits, records[0].Detail, stdout.String())
				}
				return
			}
			if len(waits) != 0 {
				t.Errorf("waits = %+v, want no card in his Command Center", waits)
			}
			if !strings.HasSuffix(records[0].Detail, "(no card: it names nothing he acts on)") {
				t.Errorf("wake = %q, want it to tell the CEO the wait has no card", records[0].Detail)
			}
			for _, said := range []string{"No card went to the Overlord's Command Center", "--run", "--lavish", "--link", "goblins auth request", "goblins notify g1 --blocked"} {
				if !strings.Contains(stdout.String(), said) {
					t.Errorf("stdout = %q, want it to say %q", stdout.String(), said)
				}
			}
		})
	}
}

// nativeGoblin records goblin id as a native task whose terminal is hosted in
// stateDir with this test process as its program, so what the test runs is
// proven to come from inside it and reaches the Command Center's inboxes.
func nativeGoblin(t *testing.T, stateDir, id string) {
	t.Helper()
	worktree := t.TempDir()
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: id, SpawnGen: "gen-1", Project: worktree, Worktree: worktree, Backend: "native", Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	hostThisProcess(t, stateDir, id)
}

// A wait declared to be the open decision is published following it by its
// ID, so the board shows only the decision while it is open and its answer
// closes the wait; one declared --separate follows none and is a card of its
// own.
func TestNotifyWaitPublishesWhatItDeclared(t *testing.T) {
	for decisionName, open := range decisionsForG1 {
		for name, isFollowing := range map[string]bool{"--follows the decision": true, "--separate": false} {
			t.Run(decisionName+", "+name, func(t *testing.T) {
				// Arrange
				stateDir := questionHome(t)
				nativeGoblin(t, stateDir, "g1")
				id, _ := open(t, stateDir)
				flags, want, line := []string{"--separate", "--link", "https://dashboard.stripe.com/login"}, "", "waiting on overlord: Waiting for your answer on the store (link https://dashboard.stripe.com/login)"
				if isFollowing {
					flags, want, line = []string{"--follows", id}, id, "waiting on overlord: Waiting for your answer on the store"
				}
				var stdout, stderr bytes.Buffer

				// Act
				exit := runNotify(append([]string{"g1", "--waiting-on", "overlord", "Waiting for your answer on the store"}, flags...), &stdout, &stderr, defaultCommandRuntime())

				// Assert
				if exit != 0 || stderr.Len() != 0 {
					t.Fatalf("exit=%d stderr=%q, want the wait taken", exit, stderr.String())
				}
				if waits := waitsInTheInbox(t, stateDir); len(waits) != 1 || waits[0].Follows != want {
					t.Fatalf("published waits = %+v, want one following %q", waits, want)
				}
				if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != line {
					t.Fatalf("wake records = %+v %v, want the wait on the Overlord", records, err)
				}
			})
		}
	}
}

// The CEO, 2026-10-18: opening something means a page, a link or a file, so a
// wait beside a file its goblin delivered opens that file, as its card does,
// and is a card of its own. --follows is refused, recording nothing, and
// names the file's item; --separate is taken.
func TestNotifyWaitBesideADeliveredFileIsACardOfItsOwn(t *testing.T) {
	for decisionName, open := range decisionsForG1 {
		for name, c := range map[string]struct {
			flags     func(id string) []string
			isRefused bool
		}{
			"--follows the decision": {flags: func(id string) []string { return []string{"--follows", id} }, isRefused: true},
			"--separate":             {flags: func(string) []string { return []string{"--separate"} }},
		} {
			t.Run(decisionName+", "+name, func(t *testing.T) {
				// Arrange
				stateDir := questionHome(t)
				nativeGoblin(t, stateDir, "g1")
				id, _ := open(t, stateDir)
				now := time.Now().UTC()
				file := supervisor.Review{ID: "g1-site-plan", Identity: strings.Repeat("g", 64), Task: "g1", Title: "The site plan", State: "open", Document: &supervisor.ReviewDocument{Name: "site-plan.pdf", Size: 2048, Sum: strings.Repeat("b", 64), Kind: "application/pdf"}, CreatedAt: now, UpdatedAt: now}
				plantJSON(t, filepath.Join(stateDir, "reviews-inbox", file.ID+".open.json"), file)
				var stdout, stderr bytes.Buffer

				// Act
				exit := runNotify(append([]string{"g1", "--waiting-on", "overlord", "Waiting for your answer on the store"}, c.flags(id)...), &stdout, &stderr, defaultCommandRuntime())

				// Assert
				waits := waitsInTheInbox(t, stateDir)
				records, err := wake.Pending(stateDir)
				if err != nil {
					t.Fatal(err)
				}
				switch {
				case c.isRefused && (exit != 2 || !strings.Contains(stderr.String(), "your delivered file g1-site-plan is open") || !strings.Contains(stderr.String(), "give --separate")):
					t.Fatalf("exit=%d stderr=%q, want --follows refused naming the delivered file", exit, stderr.String())
				case c.isRefused && (len(waits) != 0 || len(records) != 0):
					t.Fatalf("a refused wait published %+v and woke the CEO with %+v", waits, records)
				case !c.isRefused && (exit != 0 || stderr.Len() != 0 || len(waits) != 1 || waits[0].Follows != ""):
					t.Fatalf("exit=%d stderr=%q waits=%+v, want one wait of its own", exit, stderr.String(), waits)
				}
			})
		}
	}
}

// With no decision open a wait on the Overlord needs no declaration and is a
// card of its own, as it is with --separate; --follows is refused, saying
// none is open, and either flag on anything but a wait on him is refused.
func TestNotifyWaitWithNoDecisionOpenIsACardOfItsOwn(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"--follows with none open": {[]string{"g1", "--waiting-on", "overlord", "Sign in to Stripe", "--follows", "notify-g1-3"}, "and none is"},
		"--follows on a question":  {[]string{"g1", "--blocked", "Which store? options: SQLite | Postgres", "--follows", "notify-g1-3"}, "go with --waiting-on overlord"},
		"--separate on a CI wait":  {[]string{"g1", "--waiting-on", "ci", "PR 45 checks", "--separate"}, "go with --waiting-on overlord"},
		"--separate on working":    {[]string{"g1", "--working", "fixing the lint step", "--separate"}, "go with --waiting-on overlord"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			stateDir := questionHome(t)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runNotify(c.args, &stdout, &stderr, defaultCommandRuntime())

			// Assert
			if exit != 2 || !strings.Contains(stderr.String(), c.says) {
				t.Fatalf("exit=%d stderr=%q, want 2 saying %q", exit, stderr.String(), c.says)
			}
			if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
				t.Errorf("a refused notify recorded status %q", lines)
			}
			if records, err := wake.Pending(stateDir); err != nil || len(records) != 0 {
				t.Errorf("a refused notify woke the CEO: %+v %v", records, err)
			}
		})
	}
	for name, flags := range map[string][]string{"no declaration": {"--link", "https://dashboard.stripe.com/login"}, "--separate": {"--separate", "--link", "https://dashboard.stripe.com/login"}} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			stateDir := questionHome(t)
			nativeGoblin(t, stateDir, "g1")
			var stdout, stderr bytes.Buffer

			// Act
			exit := runNotify(append([]string{"g1", "--waiting-on", "overlord", "Sign in to Stripe in the browser I opened"}, flags...), &stdout, &stderr, defaultCommandRuntime())

			// Assert
			if exit != 0 || stderr.Len() != 0 {
				t.Fatalf("exit=%d stderr=%q, want the wait taken", exit, stderr.String())
			}
			if waits := waitsInTheInbox(t, stateDir); len(waits) != 1 || waits[0].Follows != "" {
				t.Fatalf("published waits = %+v, want one card of its own", waits)
			}
		})
	}
}

// An answer the Overlord gives on the board names no sequence, as goblins answer's
// does, so the goblin cites what its --blocked notify printed.
func TestAGoblinCitesTheDecisionItsBlockedNotifyPrintedThoughTheBoardAnsweredIt(t *testing.T) {
	// Arrange
	stateDir := questionHome(t)
	nativeGoblin(t, stateDir, "g1")
	var asked, stderr bytes.Buffer
	// The terminal nativeGoblin hosts runs under this test process, which it
	// records as the goblin's program, so a done report would read that
	// terminal's own console host and shell as left running once the test
	// binary has run past fleettree.HarnessLaunch.
	runtime := defaultCommandRuntime()
	runtime.leftRunning = nil
	if exit := runNotify([]string{"g1", "--blocked", "Close it? options: Close it, no PR (Recommended) | Keep it open"}, &asked, &stderr, runtime); exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	printed := regexp.MustCompile(`--decision (\d+)`).FindStringSubmatch(asked.String())
	if printed == nil {
		t.Fatalf("stdout = %q, want the sequence to cite", asked.String())
	}
	questions, err := supervisor.OpenDecisions(stateDir, "g1")
	if err != nil || len(questions) != 1 {
		t.Fatalf("open decisions = %+v, %v; want the question the notify asked", questions, err)
	}
	answered := questions[0]
	answered.Status, answered.Answer, answered.AnsweredBy = "succeeded", "Close it, no PR", "overlord"
	plantJSON(t, filepath.Join(stateDir, ".supervisor.json"), map[string]any{"questions": []supervisor.Question{answered}})
	var stdout bytes.Buffer

	// Act
	exit := runNotify([]string{"g1", "--done", "--closed", closedIssue, "--decision", printed[1]}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	want := "done: closed " + closedIssue + " (decision " + printed[1] + ": Close it, no PR)"
	lines, err := state.TailStatus(stateDir, "g1", 1)
	if err != nil || len(lines) != 1 {
		t.Fatalf("status = %v, %v; want one done line", lines, err)
	}
	if _, event := state.SplitStatus(lines[0]); event != want {
		t.Errorf("status = %q, want %q", event, want)
	}
}
