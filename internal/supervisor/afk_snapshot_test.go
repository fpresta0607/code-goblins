package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// While AFK mode is on the snapshot says what each held item's goblin did
// meanwhile, from the status log the snapshot already reads for that goblin's
// card. So items held for several goblins, two of them for one, cost a
// snapshot of an unchanged fleet no file it had not opened before, and a
// goblin's new report costs its own status file once.
func TestASnapshotWithItemsHeldForGoblinsOpensNoStatusLogAgain(t *testing.T) {
	// Arrange
	s, h := liveSizedFleet(t)
	now := time.Now().UTC()
	if _, _, err := afk.TurnOn(h.State, "his board", nil, now); err != nil {
		t.Fatal(err)
	}
	for i, task := range []string{"goblin-00", "goblin-00", "goblin-01", "goblin-02", "goblin-03", "goblin-04"} {
		review := Review{ID: fmt.Sprintf("waiting-%s-%d", task, i), Identity: strings.Repeat("d", 64), Task: task, Title: "Waiting on you: sign in to Vercel", State: "open", CreatedAt: now, UpdatedAt: now}
		if err := s.Store.acceptReview(review); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.holdForOverlord(now); err != nil {
		t.Fatal(err)
	}
	first, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	before := fsx.Opens()
	second, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	unchanged := fsx.Opens() - before
	if err := state.AppendStatus(h.State, "goblin-00", "working: the new report"); err != nil {
		t.Fatal(err)
	}
	before = fsx.Opens()
	third, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	reported := fsx.Opens() - before

	// Assert
	if len(first.AFK.Held) != 6 || len(second.AFK.Held) != 6 {
		t.Fatalf("held = %+v, then %+v, want the six waits each time", first.AFK.Held, second.AFK.Held)
	}
	t.Logf("a snapshot of an unchanged fleet with six held items opened %d files, and %d after one goblin's report", unchanged, reported)
	const unchangedBudget, reportedBudget = 6, 7
	if unchanged > unchangedBudget {
		t.Errorf("a snapshot of an unchanged fleet with six held items opened %d files, want at most %d", unchanged, unchangedBudget)
	}
	if reported > reportedBudget {
		t.Errorf("a snapshot after one goblin's report opened %d files, want at most %d", reported, reportedBudget)
	}
	for _, held := range third.AFK.Held {
		want := ""
		if held.Task == "goblin-00" {
			want = "working: the new report"
		}
		if held.Meanwhile != want {
			t.Errorf("%s of %s says its goblin did %q meanwhile, want %q", held.Item, held.Task, held.Meanwhile, want)
		}
	}
}

// The supervisor asks for the stretch's lines for every snapshot it sends, so
// the log's file is read again only when the file has changed, and always
// when it has.
func TestAReaderReadsTheLogAgainOnlyWhenItHasChanged(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store}
	night := time.Now().UTC().Add(-time.Hour)
	switched, _, err := afk.TurnOn(h.State, "his board", nil, night)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := afk.Log(h.State, afk.Entry{Kind: afk.KindDeploy, What: "acme production", Evidence: "/health reads 200"}, night.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(h.State, "afk.audit")

	// Act
	first, err := s.afkView(store.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	// The same size and time of writing with other words in it: a snapshot
	// that read the file again would count these.
	written, err := os.Stat(log)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, make([]byte, len(kept)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(log, written.ModTime(), written.ModTime()); err != nil {
		t.Fatal(err)
	}
	opened := fsx.Opens()
	unchanged, err := s.afkView(store.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	reopened := fsx.Opens() - opened
	if err := os.WriteFile(log, kept, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := afk.Log(h.State, afk.Entry{Kind: afk.KindInstall, What: "cfo 1.4.2", Evidence: "cfo version reads 1.4.2"}, night.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	grown, err := s.afkView(store.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	// Another stretch is on with the log's file as it was.
	switched.Session = "afk-another-stretch"
	another, err := json.Marshal(switched)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "afk.json"), another, 0o600); err != nil {
		t.Fatal(err)
	}
	other, err := s.afkView(store.Snapshot())
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	if first.State != "on" || first.Decided != 1 {
		t.Fatalf("the first read = %+v, want on with the deploy decided", first)
	}
	if unchanged.Decided != 1 || reopened != 0 {
		t.Errorf("a read of a file that had not changed = %+v after opening %d files, want what was read before and no file opened", unchanged, reopened)
	}
	if grown.Decided != 2 {
		t.Errorf("a read after the log grew = %+v, want the new decision too", grown)
	}
	if other.State != "on" || other.Decided != 0 {
		t.Errorf("another stretch reads %+v, want nothing decided: what was read for one stretch is never handed to another", other)
	}
}

// A stretch whose log is not there reads as no lines.
func TestAReaderReadsNoLogAsNoLines(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store}
	if _, _, err := afk.TurnOn(h.State, "his board", nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.State, "afk.audit")); err != nil {
		t.Fatal(err)
	}

	// Act
	view, err := s.afkView(store.Snapshot())
	again, againErr := s.afkView(store.Snapshot())

	// Assert
	if err != nil || view.State != "on" || view.Decided != 0 || len(view.Held) != 0 {
		t.Fatalf("afkView = %+v, %v, want on with nothing decided or held", view, err)
	}
	if againErr != nil || again.Decided != 0 || len(again.Held) != 0 {
		t.Errorf("afkView again = %+v, %v, want nothing decided or held", again, againErr)
	}
}
