package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/cdp"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

// The Overlord, 2026-10-18: "scrawl not feeding back to command". End to end,
// on a Scrawl of the test's own and in a real headless Chrome: a goblin's
// page waits on him as a card, he writes his note in the page's message box
// and presses Send & End, and within a second of his press the card leaves
// what the Command Center reads, the board's event stream, his note reaches
// the goblin once and the CFO is told once. The card's link names the page
// on local host or on his tailnet alike: either is polled on the page Scrawl
// serves at it. On the build he ran that day Kenny's and Frida's pages were
// put to the CFO with no page file, so nothing polled them and his Send & End
// there reached nobody; the build since polls them, and this test holds it.
func TestASendAndEndOnAScrawlPageClearsItsCardAtOnceAndDeliversHisNoteOnce(t *testing.T) {
	scrawl := scratchScrawl(t)
	for name, host := range map[string]string{"local host": "", "tailnet": "https://pc.example.ts.net:4388"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, _, goblin, connection := goblinFixture(t, store)
			page, link := scrawl.open(t, filepath.Join(meta.Worktree, ".lavish", "plan.html"))
			published := link
			if host != "" {
				published = host + link[strings.Index(link, "/session/"):]
			}
			// As cfo present --kind review --watch publishes it.
			if err := PublishReview(h, meta.ID, "task-1-plan", "Approve the plan", published, "", nil); err != nil {
				t.Fatal(err)
			}
			if err := store.ingestReviews(); err != nil {
				t.Fatal(err)
			}
			if reviews := store.Snapshot().Reviews; len(reviews) != 1 || reviews[0].LavishPage != page || reviews[0].Lavish != published {
				t.Fatalf("reviews = %+v, want the card polled on %s", reviews, page)
			}
			s, err := Start(context.Background(), h, Options{CFO: connection, PollPage: (axi.Lavish{Commands: execx.OSRunner{}}).Poll})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			board := boardReviews(t, s)
			// He presses once the page says its poll listens, as it has for
			// minutes on a page of his.
			tab := scrawl.browse(t, link)

			// Act
			pressed := sendAndEnd(t, tab, "Ship the plan as drawn, with the second step first")
			var cleared time.Duration
			for expired := time.After(time.Minute); cleared == 0; {
				select {
				case reviews, isOpen := <-board:
					if !isOpen {
						t.Fatal("the board's event stream ended")
					}
					if len(reviews) == 1 && reviews[0].State != "open" {
						cleared = time.Since(pressed)
					}
				case <-expired:
					t.Fatal("the card never left the Command Center")
				}
			}
			told := goblin.waitForLines(t, 1)
			waitForReviewWake(t, h.State, meta.ID)

			// Assert
			t.Logf("the card left the Command Center %s after his Send & End", cleared.Round(time.Millisecond))
			// stall-accepted: it times the card against his press, as his own test of a page's item does, and nothing it waits on is longer than a poll's return.
			if cleared > time.Second {
				t.Errorf("the card left the Command Center %s after his Send & End, want under a second", cleared.Round(time.Millisecond))
			}
			if got := s.Store.Snapshot().Reviews[0]; got.State != "answered" || got.AnsweredIn != "page" || got.PageSettled == nil {
				t.Errorf("the card = %+v, want it answered on its page, its page settled", got)
			}
			if len(told) != 1 || !strings.Contains(told[0], "Ship the plan as drawn, with the second step first") || len(goblin.lines(t)) != 1 {
				t.Errorf("the goblin got %q, want his note once", goblin.lines(t))
			}
			if wakes := reviewWakes(t, h.State, meta.ID); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "Ship the plan as drawn") {
				t.Errorf("review wakes = %+v, want the CFO told once that the goblin has his note", wakes)
			}
		})
	}
}

// waitForReviewWake waits until the CFO has been told of task's page.
func waitForReviewWake(t *testing.T, stateDir, task string) {
	t.Helper()
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if len(reviewWakes(t, stateDir, task)) > 0 {
			return
		}
	}
	t.Fatalf("the CFO was never told of %s's page", task)
}

// testScrawl is a Scrawl of the test's own: lavish-axi's server on a free
// port of local host, keeping its state in a folder of the test's, so the
// Overlord's own Scrawl and its sessions are never touched.
type testScrawl struct {
	port int
}

// scratchScrawl starts nothing yet: the first page it opens starts its
// server, which it stops once the test ends. It needs the Code Goblins build
// of lavish-axi: CI installs it for this test, and a desk without it skips.
func scratchScrawl(t *testing.T) *testScrawl {
	t.Helper()
	version, err := exec.Command("lavish-axi", "--version").Output()
	if err != nil || !strings.Contains(string(version), "codegoblins") {
		if os.Getenv("CI") != "" {
			t.Fatalf("lavish-axi --version = %q (%v), want the Code Goblins build, which CI installs for this test", version, err)
		}
		t.Skipf("lavish-axi --version = %q (%v): this test needs the Code Goblins build of Scrawl", version, err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAVISH_AXI_PORT", strconv.Itoa(port))
	t.Setenv("LAVISH_AXI_HOST", "127.0.0.1")
	t.Setenv("LAVISH_AXI_NO_OPEN", "1")
	t.Setenv("LAVISH_AXI_STATE_DIR", t.TempDir())
	t.Cleanup(func() {
		if output, err := exec.Command("lavish-axi", "stop", "--port", strconv.Itoa(port)).CombinedOutput(); err != nil {
			t.Logf("lavish-axi stop --port %d: %v: %s", port, err, output)
		}
		if connection, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second); err == nil {
			connection.Close()
			t.Errorf("the test's Scrawl still listens on port %d", port)
		}
	})
	return &testScrawl{port: port}
}

// open writes a page at file and opens its review session, returning the
// page as lavish-axi keeps it and its link.
func (s *testScrawl) open(t *testing.T, file string) (page, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`<html><body><main><h1>The plan</h1><p>Step one, then step two.</p></main></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	link, err := (axi.Lavish{Commands: execx.OSRunner{}}).Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	session, found, err := (axi.Lavish{}).SessionAt(link)
	if err != nil || !found {
		t.Fatalf("lavish-axi keeps no session at %s (%v)", link, err)
	}
	return session.File, link
}

// browse opens link in a headless Chrome of the test's own, ended with the
// test, and returns its tab once the page's message box shows, the page says
// a poll listens for what he sends, and the page it frames has spoken to it
// since it last loaded, as on a page of his open for minutes. Send & End
// first asks the framed page for a snapshot of what he sees, and Scrawl
// waits five seconds for an answer before it sends without one; a press
// within the tenth of a second before the framed page spoke met that wait on
// GitHub's runners and here (#1190). artifactLoadToken and artifactSpokeToken
// are the Scrawl page's own: an answer counts only when it carries the load's
// token, and the page keeps the token of the last load that spoke.
func (s *testScrawl) browse(t *testing.T, link string) *cdp.Conn {
	t.Helper()
	executable, err := browserlogin.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	chrome, err := browserlogin.Start(ctx, browserlogin.Launch{Executable: executable, Profile: t.TempDir(), Headless: true, Page: link})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := browserlogin.End(context.Background(), chrome); err != nil {
			t.Error(err)
		}
	})
	tab := firstTab(ctx, t, chrome.URL)
	t.Cleanup(func() { tab.Close() })
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(100 * time.Millisecond) {
		if pageValue(t, tab, `!!document.getElementById("chatInput") && !document.getElementById("sendAndEnd").disabled && ["Agent listening", "External listener active"].includes(document.getElementById("panelSummary").textContent) && typeof artifactLoadToken === "string" && artifactLoadToken !== "" && artifactSpokeToken === artifactLoadToken`) == "true" {
			return tab
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Scrawl page at %s never showed its message box with a poll listening and its framed page spoken: %s", link, pageValue(t, tab, `(document.getElementById("panelSummary") ? document.getElementById("panelSummary").textContent : "no summary") + "; framed page " + (typeof artifactLoadToken !== "string" ? "load token unknown to this Scrawl" : artifactSpokeToken === artifactLoadToken && artifactLoadToken !== "" ? "spoke" : "silent")`))
		}
	}
}

// sendAndEnd writes note in the page's message box and presses Send & End, as
// he does, and returns when he pressed.
func sendAndEnd(t *testing.T, tab *cdp.Conn, note string) time.Time {
	t.Helper()
	quoted, err := json.Marshal(note)
	if err != nil {
		t.Fatal(err)
	}
	pageValue(t, tab, fmt.Sprintf(`(() => { const box = document.getElementById("chatInput"); box.value = %s; box.dispatchEvent(new Event("input", { bubbles: true })); return "written"; })()`, quoted))
	pressed := time.Now()
	if got := pageValue(t, tab, `(() => { document.getElementById("sendAndEnd").click(); return "pressed"; })()`); got != "pressed" {
		t.Fatalf("Send & End = %q", got)
	}
	return pressed
}

// pageValue evaluates expression in the tab and returns its value as text.
func pageValue(t *testing.T, tab *cdp.Conn, expression string) string {
	t.Helper()
	var result struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
	}
	if err := tab.Call(context.Background(), "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true}, &result); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(result.Result.Value)
}
