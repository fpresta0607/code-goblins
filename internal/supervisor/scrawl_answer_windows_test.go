package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/cdp"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
)

// The Overlord, 2026-10-18: "scrawl not feeding back to command". End to end,
// on a Scrawl of the test's own and in a real headless Chrome: a goblin's
// page waits on him as a card, he writes his note in the page's message box
// and presses Send & End, and the card leaves what the Command Center reads,
// the board's event stream, at once: Scrawl sends his note and his end
// together with the framed page's snapshot, never after its five seconds
// without one, the poll that waited at his press reads them, and the card
// leaves once the receipt that keeps his answer is saved. His note reaches
// the goblin once and the CFO is told once. The card's link names the page on
// local host or on his tailnet alike: either is polled on the page Scrawl
// serves at it. On the build he ran that day Kenny's and Frida's pages were
// put to the CFO with no page file, so nothing polled them and his Send & End
// there reached nobody; the build since polls them, and this test holds it.
//
// The card's leaving is not timed against his press: before it leaves, the
// receipt of his answer and the card's close are each saved as a new file,
// and on a busy desk making a new file in the state folder took up to half a
// second and saving the receipt once took 1.8 s, which held the card 1.9 s
// after his press (#1344). A receipt slow to save holds the card until it is
// saved, as it must.
func TestASendAndEndOnAScrawlPageClearsItsCardAtOnceAndDeliversHisNoteOnce(t *testing.T) {
	scrawl := scratchScrawl(t)
	for name, test := range map[string]struct {
		host          string
		isReceiptHeld bool
	}{
		"local host":                  {},
		"tailnet":                     {host: "https://pc.example.ts.net:4388"},
		"a receipt that saves slowly": {isReceiptHeld: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, _, goblin, connection := goblinFixture(t, store)
			page, link := scrawl.open(t, filepath.Join(meta.Worktree, ".lavish", "plan.html"))
			published := link
			if test.host != "" {
				published = test.host + link[strings.Index(link, "/session/"):]
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
			// A receipt that saves slowly: a reader holds the receipt file, so
			// replacing it waits until the reader lets go, as on a busy desk
			// replacing it waits on making its new file.
			var receiptHolder *os.File
			if test.isReceiptHeld {
				if err := os.WriteFile(store.pageReceiptsPath(), []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				holder, err := os.Open(store.pageReceiptsPath())
				if err != nil {
					t.Fatal(err)
				}
				receiptHolder = holder
				t.Cleanup(func() { holder.Close() })
			}
			// polls are the page's polls, as each began and what it read. The
			// poll that reads his Send & End lets a held receipt go a moment
			// after, saying when.
			var polls struct {
				sync.Mutex
				began []time.Time
				read  []axi.PagePoll
			}
			released := make(chan time.Time, 1)
			poll := func(ctx context.Context, file, reply string, timeout time.Duration) (axi.PagePoll, error) {
				began := time.Now()
				read, err := (axi.Lavish{Commands: execx.OSRunner{}}).Poll(ctx, file, reply, timeout)
				polls.Lock()
				polls.began, polls.read = append(polls.began, began), append(polls.read, read)
				polls.Unlock()
				if read.Ended && receiptHolder != nil {
					// stall-accepted: a pause can only hold the receipt longer, and the card waits for it all the same.
					time.AfterFunc(1500*time.Millisecond, func() {
						at := time.Now()
						receiptHolder.Close()
						released <- at
					})
				}
				return read, err
			}
			s, err := Start(context.Background(), h, Options{CFO: connection, PollPage: poll})
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
			var cleared time.Time
			for expired := time.After(time.Minute); cleared.IsZero(); {
				select {
				case reviews, isOpen := <-board:
					if !isOpen {
						t.Fatal("the board's event stream ended")
					}
					if len(reviews) == 1 && reviews[0].State != "open" {
						cleared = time.Now()
					}
				case <-expired:
					t.Fatal("the card never left the Command Center")
				}
			}
			told := goblin.waitForLines(t, 1)
			waitForReviewWake(t, h.State, meta.ID)

			// Assert
			t.Logf("the card left the Command Center %s after his Send & End", cleared.Sub(pressed).Round(time.Millisecond))
			polls.Lock()
			began, read := slices.Clone(polls.began), slices.Clone(polls.read)
			polls.Unlock()
			// The supervisor polls an ended page no more, so the poll that read
			// his Send & End is the last.
			last := len(read) - 1
			if last < 0 || !began[last].Before(pressed) || read[last].Status != "feedback" || !read[last].Ended || read[last].EndedBy != "user" || slices.ContainsFunc(read[:last], func(p axi.PagePoll) bool { return p.Status == "feedback" }) {
				summary := make([]string, len(read))
				for i, p := range read {
					summary[i] = fmt.Sprintf("%s, ended %t, began %s before his press", p.Status, p.Ended, pressed.Sub(began[i]).Round(time.Millisecond))
				}
				t.Fatalf("the page's polls = %q, want his note and his end read together by the poll that waited at his press", summary)
			}
			snapshot := ""
			for _, line := range strings.Split(read[last].Output, "\n") {
				if value, isSnapshot := strings.CutPrefix(strings.TrimSuffix(line, "\r"), "dom_snapshot: "); isSnapshot {
					snapshot = value
				}
			}
			if !strings.Contains(snapshot, "Step one, then step two.") {
				t.Errorf("Scrawl sent his Send & End with the snapshot %q, want the framed page's, which it waits up to five seconds for before it sends without one", snapshot)
			}
			if test.isReceiptHeld {
				select {
				case at := <-released:
					if !cleared.After(at) {
						t.Errorf("the card left the Command Center before his receipt was saved, want his answer kept first")
					}
				case <-time.After(time.Minute):
					t.Error("his receipt was never let go")
				}
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
	// servers are the processes that served its pages, by pid, each held
	// from when it listened, so the test waits for those very processes to
	// end, never for a pid Windows has since given another.
	servers map[int]windows.Handle
}

// scratchScrawl starts nothing yet: the first page it opens starts its
// server, which it stops, and waits for, once the test ends. It needs the Code Goblins build
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
	s := &testScrawl{port: port, servers: map[int]windows.Handle{}}
	t.Cleanup(func() {
		if output, err := exec.Command("lavish-axi", "stop", "--port", strconv.Itoa(port)).CombinedOutput(); err != nil {
			t.Logf("lavish-axi stop --port %d: %v: %s", port, err, output)
		}
		// lavish-axi stop returns once its server stops answering, and the
		// server's process can run on after it, still listening, or holding
		// the state folder the test removes next for a second more (#1345),
		// so the test waits for each server's process to end.
		for pid, server := range s.servers {
			if event, err := windows.WaitForSingleObject(server, uint32(time.Minute.Milliseconds())); event != windows.WAIT_OBJECT_0 {
				t.Errorf("the test's Scrawl, process %d on port %d, still runs a minute after its stop (%v)", pid, port, err)
			}
			windows.CloseHandle(server)
		}
	})
	return s
}

// open writes a page at file and opens its review session, returning the
// page as lavish-axi keeps it and its link, and holds the process of the
// server that serves it.
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
	listens, err := fleettree.Listeners()
	if err != nil {
		t.Fatal(err)
	}
	isListening := false
	for pid, ports := range listens {
		if !slices.Contains(ports, s.port) {
			continue
		}
		isListening = true
		if _, isHeld := s.servers[pid]; isHeld {
			continue
		}
		server, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
		if err != nil {
			t.Fatalf("open the test's Scrawl, process %d: %v", pid, err)
		}
		s.servers[pid] = server
	}
	if !isListening {
		t.Fatalf("nothing listens on the test's Scrawl port %d with its page open", s.port)
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
