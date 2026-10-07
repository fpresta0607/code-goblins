package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// fakeScrawl stands in for lavish-axi as a sweep meets it: the sessions its
// state file lists, the prompts a poll takes from a page, the replies a poll
// posts there, and the pages an end ends.
type fakeScrawl struct {
	mu       sync.Mutex
	sessions map[string]*axi.PageSession
	prompts  map[string][]string
	replies  map[string][]string
	polled   []string
	ended    []string
}

func newFakeScrawl() *fakeScrawl {
	return &fakeScrawl{sessions: map[string]*axi.PageSession{}, prompts: map[string][]string{}, replies: map[string][]string{}}
}

// add lists a session of page with status, holding prompts undelivered.
func (f *fakeScrawl) add(page, status string, prompts ...string) {
	f.sessions[page] = &axi.PageSession{File: page, Status: status, Pending: len(prompts)}
	f.prompts[page] = prompts
}

func (f *fakeScrawl) list() ([]axi.PageSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sessions []axi.PageSession
	for _, session := range f.sessions {
		sessions = append(sessions, *session)
	}
	slices.SortFunc(sessions, func(a, b axi.PageSession) int { return strings.Compare(a.File, b.File) })
	return sessions, nil
}

func (f *fakeScrawl) poll(_ context.Context, page, reply string, _ time.Duration) (axi.PagePoll, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polled = append(f.polled, page)
	if reply != "" {
		f.replies[page] = append(f.replies[page], reply)
	}
	session := f.sessions[page]
	if prompts := f.prompts[page]; len(prompts) > 0 {
		f.prompts[page], session.Pending = nil, 0
		return axi.PagePoll{Status: "feedback", Ended: session.Status == "ended", EndedBy: session.EndedBy, Prompts: prompts, Output: "session:\n  status: feedback\n"}, nil
	}
	if session.Status == "ended" {
		return axi.PagePoll{Status: "ended", EndedBy: session.EndedBy}, nil
	}
	return axi.PagePoll{Status: "waiting"}, nil
}

func (f *fakeScrawl) end(_ context.Context, page string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, page)
	if session := f.sessions[page]; session.Status != "ended" {
		session.Status, session.EndedBy = "ended", "agent"
	}
	return nil
}

// sweeper is a service whose sweep meets scrawl.
func sweeper(store *Store, scrawl *fakeScrawl, cfo *CFOConnection) *Service {
	return &Service{Store: store, Options: Options{CFO: cfo, PollPage: scrawl.poll, PageSessions: scrawl.list, EndPage: scrawl.end}}
}

// writePage writes an HTML page at path and returns it.
func writePage(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// retire leaves task id as cfo cleanup does: its status log, no record.
func retire(t *testing.T, stateDir, id string) {
	t.Helper()
	if err := state.AppendStatus(stateDir, id, "done: PR https://github.com/example/repo/pull/1"); err != nil {
		t.Fatal(err)
	}
	if err := state.RemoveTaskMeta(stateDir, id); err != nil {
		t.Fatal(err)
	}
}

// Item 2 of the brief: what the Overlord sent on a goblin's page that nothing
// read reaches the CFO once the goblin has been retired, as a review wake
// naming the task, exactly once however often the sweep runs. The page is
// ended first, so it takes nothing more, and the replies on it say why the
// review closed and who has what he sent.
func TestASweepDeliversARetiredGoblinsUnreadFeedbackToTheCFOOnce(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	page := writePage(t, filepath.Join(h.Data, "task-1", "mockups", "plan.html"))
	retire(t, h.State, "task-1")
	scrawl := newFakeScrawl()
	scrawl.add(page, "feedback", "Make the goblin heads bigger")
	s := sweeper(store, scrawl, nil)

	// Act
	first := s.sweepSessions(context.Background())
	second := s.sweepSessions(context.Background())

	// Assert
	if first != nil || second != nil {
		t.Fatal(first, second)
	}
	wakes := reviewWakes(t, h.State, "task-1")
	if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "the Overlord wrote on the page "+page+" of task-1, which has been retired") || !strings.HasSuffix(wakes[0].Detail, ", act on it") {
		t.Fatalf("review wakes = %+v, want one naming the retired task and its page, for the CFO to act on", wakes)
	}
	_, rest, _ := strings.Cut(wakes[0].Detail, "his feedback is in ")
	saved, _, _ := strings.Cut(rest, ", act on it")
	if data, err := os.ReadFile(saved); err != nil || string(data) != "session:\n  status: feedback\n" {
		t.Errorf("saved feedback %s = %q, %v; want the poll's whole output", saved, data, err)
	}
	if !slices.Equal(scrawl.ended, []string{page}) {
		t.Errorf("ended %q, want the retired goblin's page ended once", scrawl.ended)
	}
	want := []string{"task-1 has been retired, so this review is closed. Anything you sent here went to the CFO.", "Received. task-1 has been retired, so the CFO has it."}
	if got := scrawl.replies[page]; !slices.Equal(got, want) {
		t.Errorf("replies on the page = %q, want %q", got, want)
	}
}

// Item 4 of the brief: a sweep ends the open pages of retired goblins, found
// by the item that presented them or by the worktree, data folder or task
// scratch folder that holds them, so Scrawl's open list means something. It
// never touches a page of a goblin that stands, the CFO's own, one a poller
// watches, one already ended, or one no fleet task holds.
func TestASweepEndsOnlyTheOpenPagesOfRetiredGoblins(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	scrawl := newFakeScrawl()
	retire(t, h.State, "task-2")
	presented := writePage(t, filepath.Join(h.Root, "elsewhere", "presented.html"))
	store.mu.Lock()
	store.db.Reviews = append(store.db.Reviews,
		Review{ID: "waiting-task-2-4", Identity: strings.Repeat("a", 64), Task: "task-2", Title: "Waiting on you: the plan", Lavish: pageLink, LavishPage: presented, State: "withdrawn", CreatedAt: time.Now().UTC()},
		Review{ID: "cfo-plan-1", Identity: strings.Repeat("c", 64), Title: "The plan", Lavish: pageLink, LavishPage: filepath.Join(h.Root, ".lavish", "cfo.html"), State: "cleared", CreatedAt: time.Now().UTC()})
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	store.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(h.State, state.ArchiveDirName, "task-3.20261006T120000Z"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"task-4", "task-5"} {
		if err := state.AppendStatus(h.State, id, "done: PR https://github.com/example/repo/pull/4"); err != nil {
			t.Fatal(err)
		}
	}
	inData := writePage(t, filepath.Join(h.Data, "task-2", "review.html"))
	inOldWorktree := writePage(t, filepath.Join(h.Root, "project", ".worktrees", "gb-task-3", ".lavish", "plan.html"))
	inTaskScratch := writePage(t, filepath.Join(h.State, "tasktmp", "task-4", "scrawl", "index.html"))
	inHomeWorktree := writePage(t, filepath.Join(h.Root, "worktrees", "project", "task-5", ".lavish", "plan.html"))
	watched := writePage(t, filepath.Join(h.Data, "task-1", "watched.html"))
	for _, page := range []string{presented, inData, inOldWorktree, inTaskScratch, inHomeWorktree, writePage(t, filepath.Join(h.Data, "task-1", "review.html")), filepath.Join(h.Root, ".lavish", "cfo.html"), writePage(t, filepath.Join(h.Data, "research", "plan.html")), writePage(t, filepath.Join(t.TempDir(), "notes.html"))} {
		scrawl.add(page, "open")
	}
	ended := writePage(t, filepath.Join(h.Data, "task-2", "old.html"))
	scrawl.add(ended, "ended")
	scrawl.add(watched, "feedback", "Bigger")
	s := sweeper(store, scrawl, nil)
	s.pages = map[string]bool{pageKey(watched): true}

	// Act
	err := s.sweepSessions(context.Background())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []string{presented, inData, inOldWorktree, inTaskScratch, inHomeWorktree}
	slices.Sort(want)
	got := slices.Clone(scrawl.ended)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("ended %q, want only the retired goblins' open pages %q", got, want)
	}
	polled := slices.Clone(scrawl.polled)
	slices.Sort(polled)
	if !slices.Equal(polled, want) {
		t.Errorf("polled %q, want only the pages it ended, to post why %q", polled, want)
	}
	if got := scrawl.replies[inOldWorktree]; len(got) != 1 || got[0] != "task-3 has been retired, so this review is closed. Anything you sent here went to the CFO." {
		t.Errorf("replies on task-3's page = %q, want it told why the review closed", got)
	}
	if r := store.Snapshot().Reviews[0]; r.ID != "waiting-task-2-4" || r.PageSettled == nil {
		t.Errorf("the retired goblin's item = %+v, want its page settled", r)
	}
}

// What the Overlord sent on a page of a goblin that stands but that nothing
// watches, such as one it presented with cfo present, reaches that goblin
// while it runs and the CFO otherwise, once, and the page stays open.
func TestASweepHandsUnreadFeedbackToTheGoblinThatStillStands(t *testing.T) {
	for name, test := range map[string]struct {
		runs  bool
		wake  string
		reply string
	}{
		"a goblin that runs":           {true, "the Overlord wrote on the page %s, and task-1 has it: Make the heads bigger", "Received. task-1 has it."},
		"a goblin that cannot take it": {false, "relay it to task-1", "Received. The CFO has it and passes it to task-1."},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			var cfo *CFOConnection
			var told func() []string
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			if test.runs {
				var goblin hostedTerminal
				meta, _, goblin, cfo = goblinFixture(t, store)
				told = func() []string { return goblin.lines(t) }
			}
			page := writePage(t, filepath.Join(meta.Worktree, ".lavish", "walkthrough.html"))
			scrawl := newFakeScrawl()
			scrawl.add(page, "feedback", "Make the heads bigger")
			s := sweeper(store, scrawl, cfo)

			// Act
			first := s.sweepSessions(context.Background())
			second := s.sweepSessions(context.Background())

			// Assert
			if first != nil || second != nil {
				t.Fatal(first, second)
			}
			if wakes := reviewWakes(t, h.State, "task-1"); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, strings.ReplaceAll(test.wake, "%s", page)) {
				t.Fatalf("review wakes = %+v, want one saying %q", wakes, test.wake)
			}
			if test.runs {
				if lines := told(); len(lines) != 1 || !strings.Contains(lines[0], "The Overlord wrote on your review page "+page+": Make the heads bigger") {
					t.Fatalf("the goblin got %q, want what he wrote on its page", lines)
				}
			}
			if got := scrawl.replies[page]; len(got) != 1 || got[0] != test.reply {
				t.Errorf("replies on the page = %q, want %q", got, test.reply)
			}
			if len(scrawl.ended) != 0 {
				t.Errorf("ended %q, want a standing goblin's page left open", scrawl.ended)
			}
		})
	}
}
