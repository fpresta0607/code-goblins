package digest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// bigHome is a home the size the fleet's was on 2026-10-01, when its digest
// ran to 95 KB: goblins with status logs, pending wakes, and context files
// of about 90 KB between them.
func bigHome(t *testing.T, goblins, wakes int) home.Home {
	t.Helper()
	h := newDigestHome(t)
	for i := 1; i <= goblins; i++ {
		id := fmt.Sprintf("cg-goblin-%02d", i)
		meta := fmt.Sprintf("goblin_id=%s\nproject=C:\\dev\\code-goblins\nharness=claude\nmodel=claude-opus-5-5\nkind=ship\ntitle=Task %d of the fleet\n", id, i)
		if err := os.WriteFile(filepath.Join(h.State, id+".meta"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
		status := fmt.Sprintf("2026-10-01T12:00:00Z working: step %d of the task, with a status line long enough to be worth cutting short in a table of the whole fleet\n", i)
		if err := os.WriteFile(filepath.Join(h.State, id+".status"), []byte(status), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The queue is written as the file wake.Append leaves, one JSON record a
	// line: Append rewrites the whole queue under its lock for every record,
	// which is minutes of fixture for a queue this long.
	var queue bytes.Buffer
	for i := 1; i <= wakes; i++ {
		line, err := json.Marshal(wake.Record{Seq: i, Time: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Kind: "stale", Key: fmt.Sprintf("cg-goblin-%02d", i%40+1), Detail: fmt.Sprintf("turn %d ended with nothing running", i)})
		if err != nil {
			t.Fatal(err)
		}
		queue.Write(append(line, '\n'))
	}
	if wakes > 0 {
		if err := os.WriteFile(filepath.Join(h.State, ".wake-queue"), queue.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, size := range map[string]int{"projects.md": 10_000, "overlord.md": 50_000, filepath.Join("memory", "MEMORY.md"): 30_000} {
		path := filepath.Join(h.Data, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		var content strings.Builder
		for line := 1; content.Len() < size; line++ {
			fmt.Fprintf(&content, "- %s line %d: a standing fact the CFO keeps\n", filepath.Base(name), line)
		}
		if err := os.WriteFile(path, []byte(content.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// delivered is what a Claude Code session is given of a hook's output: all
// of it up to Limit characters, and above that a preview of about 2 KB
// beside the path of a file holding the rest.
func delivered(output string) string {
	if len(output) <= Limit {
		return output
	}
	return output[:2000]
}

// printedInFull is every file a digest's read-once contract says it printed
// in full, as home-relative paths.
func printedInFull(t *testing.T, output string) []string {
	t.Helper()
	var files []string
	found := false
	for _, line := range strings.Split(output, "\n") {
		claim, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "PRINTED IN FULL: ")
		if !ok {
			continue
		}
		found = true
		if claim == "none" {
			continue
		}
		files = append(files, strings.Split(claim, ", ")...)
	}
	if !found {
		t.Fatalf("the digest has no PRINTED IN FULL line:\n%s", output)
	}
	return files
}

// holdsWhole reports whether every line of the file at path appears in text.
func holdsWhole(t *testing.T, text, path string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if !strings.Contains(text, line) {
			return false
		}
	}
	return true
}

func composeBrief(t *testing.T, h home.Home) string {
	t.Helper()
	var out bytes.Buffer
	if err := ComposeBrief(h, os.Getpid(), "s1", &out); err != nil {
		t.Fatalf("ComposeBrief: %v", err)
	}
	return out.String()
}

// Claude Code hands a session a hook's output whole only up to 10,000
// characters (on 2.1.286 the largest passed whole was 9,293 and the smallest
// replaced was 10,026); a longer one arrives as a 2 KB preview, which is how
// a compacted CFO came to start with a session lock and a few wake lines.
// The digest the hook prints stays under a budget below that limit however
// large the home is, in the order a CFO needs it.
func TestTheBriefDigestFitsWhatClaudeCodePassesThroughWhole(t *testing.T) {
	h := bigHome(t, 40, 300)

	out := composeBrief(t, h)

	if Budget >= Limit {
		t.Fatalf("Budget %d leaves no margin under Limit %d", Budget, Limit)
	}
	if len(out) > Budget {
		t.Fatalf("the brief digest is %d bytes, over its %d budget", len(out), Budget)
	}
	assertHeaderOrder(t, out, []string{
		"== SESSION LOCK ==",
		"== WAKE QUEUE ==",
		"== SUPERVISION OPERATING INSTRUCTIONS ==",
		"== FLEET ==",
		"== READ THIS NEXT ==",
		"== READ-ONCE CONTRACT ==",
		"== NEXT STEP ==",
	})
}

// A read-once contract tells the CFO not to read again what it was handed.
// It names only files whose every line reached the session: the brief prints
// no file in full and says so, and the long digest it points to holds every
// file its own contract names.
func TestAReadOnceContractNamesOnlyFilesItPrintedWhole(t *testing.T) {
	h := bigHome(t, 40, 300)

	out := composeBrief(t, h)

	reached := delivered(out)
	for _, file := range printedInFull(t, out) {
		if !holdsWhole(t, reached, filepath.Join(h.Root, file)) {
			t.Errorf("the brief says it printed %s in full, but the session was not handed all of it", file)
		}
	}
	long, err := os.ReadFile(filepath.Join(h.State, FullDigestFile))
	if err != nil {
		t.Fatal(err)
	}
	claimed := printedInFull(t, string(long))
	for _, want := range []string{`data\projects.md`, `data\overlord.md`, `data\memory\MEMORY.md`, `state\cg-goblin-01.meta`} {
		found := false
		for _, file := range claimed {
			found = found || file == want
		}
		if !found {
			t.Errorf("the long digest's contract does not name %s, which it prints in full", want)
		}
	}
	for _, file := range claimed {
		if !holdsWhole(t, string(long), filepath.Join(h.Root, file)) {
			t.Errorf("the long digest says it printed %s in full, but does not hold all of it", file)
		}
	}
}

// What the brief leaves out is one read away: it names a file under the
// state folder that holds the long form.
func TestTheBriefNamesTheFileThatHoldsTheLongDigest(t *testing.T) {
	h := bigHome(t, 3, 2)

	out := composeBrief(t, h)

	path := filepath.Join(h.State, FullDigestFile)
	if !strings.Contains(out, "READ THIS NEXT: "+path) {
		t.Fatalf("the brief does not point at %s:\n%s", path, out)
	}
	long, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"== FLEET STATE ==", "cg-goblin-01.meta:", "== ORPHANS ==", "== CONTEXT ==", "overlord.md line 1:"} {
		if !strings.Contains(string(long), want) {
			t.Errorf("the long digest is missing %q", want)
		}
	}
}

// The ack line retires every record at or below its sequence, so it is
// printed only beside a listing of all of them: a queue too long to list
// here keeps its first records and withholds the ack, pointing at cfo drain.
func TestABriefThatCannotListTheWholeWakeQueueWithholdsItsAck(t *testing.T) {
	for _, test := range []struct {
		name    string
		wakes   int
		isWhole bool
	}{
		{"a queue that fits", 3, true},
		{"a queue too long to list", 300, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := bigHome(t, 2, test.wakes)

			out := composeBrief(t, h)

			hasAck := strings.Contains(out, "WAKE_ACK_REQUIRED: cfo drain --ack-through")
			if hasAck != test.isWhole {
				t.Errorf("ack line printed = %v, want %v:\n%s", hasAck, test.isWhole, out)
			}
			if !test.isWhole && (!strings.Contains(out, "WAKE_ACK_WITHHELD") || !strings.Contains(out, "cfo drain")) {
				t.Errorf("a partial listing does not withhold its ack and point at cfo drain:\n%s", out)
			}
			if !strings.Contains(out, fmt.Sprintf("WAKE QUEUE: %d pending", test.wakes)) {
				t.Errorf("the brief does not say how many wakes are pending:\n%s", out)
			}
		})
	}
}

// Every goblin gets one line, and a fleet too large for the budget says how
// many more there are rather than dropping them silently.
func TestTheBriefListsEveryGoblinInOneLineOrCountsTheRest(t *testing.T) {
	for _, test := range []struct {
		name    string
		goblins int
	}{
		{"a fleet that fits", 5},
		{"a fleet too large to list", 120},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := bigHome(t, test.goblins, 0)

			out := composeBrief(t, h)

			fleet := out[strings.Index(out, "== FLEET =="):strings.Index(out, "== READ THIS NEXT ==")]
			listed := strings.Count(fleet, "cg-goblin-")
			more := 0
			for _, line := range strings.Split(fleet, "\n") {
				_, _ = fmt.Sscanf(line, "(+%d more goblins", &more)
			}
			if listed+more != test.goblins {
				t.Errorf("the fleet section accounts for %d listed and %d more, want %d goblins:\n%s", listed, more, test.goblins, fleet)
			}
			if len(out) > Budget {
				t.Errorf("the brief digest is %d bytes, over its %d budget", len(out), Budget)
			}
		})
	}
}

// The fleet gives up its room only to a wake queue that needs it: a fleet
// that fits is listed whole beside a queue cut short, and the contract says
// which of the two the session has read all of.
func TestASmallFleetStaysWholeBesideAWakeQueueCutShort(t *testing.T) {
	h := bigHome(t, 2, 300)

	out := composeBrief(t, h)

	if !strings.Contains(out, "WAKE_ACK_WITHHELD") {
		t.Fatalf("the wake queue was not cut short, so this proves nothing:\n%s", out)
	}
	fleet := out[strings.Index(out, "== FLEET =="):strings.Index(out, "== READ THIS NEXT ==")]
	if listed := strings.Count(fleet, "cg-goblin-"); listed != 2 || strings.Contains(fleet, "more goblins") {
		t.Errorf("the fleet section lists %d of 2 goblins:\n%s", listed, fleet)
	}
	if !strings.Contains(out, "only the first lines of the wake queue and one line for every goblin") {
		t.Errorf("the contract does not say the queue is cut short and the fleet whole:\n%s", out)
	}
	if len(out) > Budget {
		t.Errorf("the brief digest is %d bytes, over its %d budget", len(out), Budget)
	}
}

// A wake queue that cannot be read is reported by its error, and the error
// quotes the line it could not parse. However long that line is, the brief
// stays within its budget and offers no ack for a queue nobody was shown.
func TestABriefStaysWithinItsBudgetWhenTheWakeQueueIsUnreadable(t *testing.T) {
	h := bigHome(t, 2, 0)
	corrupt := `{"seq":1,"kind":"notify","detail":"` + strings.Repeat("a blocked goblin's question ", 500) + "\n"
	if err := os.WriteFile(filepath.Join(h.State, ".wake-queue"), []byte(corrupt), 0o644); err != nil {
		t.Fatal(err)
	}

	out := composeBrief(t, h)

	if len(out) > Budget {
		t.Errorf("the brief digest is %d bytes, over its %d budget", len(out), Budget)
	}
	if !strings.Contains(out, "WAKE QUEUE: UNREADABLE (wake: corrupt queue line") {
		t.Errorf("the brief does not say the wake queue is unreadable:\n%s", out)
	}
	if strings.Contains(out, "WAKE_ACK_REQUIRED: cfo drain --ack-through") {
		t.Errorf("the brief offers an ack for a queue it could not read:\n%s", out)
	}
	fleet := out[strings.Index(out, "== FLEET =="):strings.Index(out, "== READ THIS NEXT ==")]
	if listed := strings.Count(fleet, "cg-goblin-"); listed != 2 {
		t.Errorf("the fleet section lists %d of 2 goblins beside an unreadable queue:\n%s", listed, fleet)
	}
}

// The brief is the digest a hook prints, so it is the one a CFO session
// starts from: while AFK mode is on it says so before the wake queue with
// every line of the authority's terms, as the long digest does, and still
// fits its budget beside a fleet and a wake queue too long to list. While AFK
// mode is off it says nothing of it, and a switch that cannot be read is said
// rather than left out, which would read as off.
func TestTheBriefTellsTheCFOOfAFKModeWithinItsBudget(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(t *testing.T, stateDir string)
		says    string
	}{
		{"on", func(t *testing.T, stateDir string) {
			if _, _, err := afk.TurnOn(stateDir, "his own terminal (powershell.exe pid 4242)", nil, time.Date(2026, 10, 2, 2, 10, 0, 0, time.UTC)); err != nil {
				t.Fatal(err)
			}
		}, "AFK MODE IS ON: the Supreme Overlord turned it on 2026-10-02 02:10 UTC from his own terminal (powershell.exe pid 4242)"},
		{"off", func(*testing.T, string) {}, ""},
		{"a switch that cannot be read", func(t *testing.T, stateDir string) {
			if err := os.WriteFile(filepath.Join(stateDir, "afk.json"), []byte(`{"on": tr`), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "AFK MODE: UNREADABLE ("},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := bigHome(t, 40, 300)
			test.arrange(t, h.State)

			// Act
			out := composeBrief(t, h)

			// Assert
			if len(out) > Budget {
				t.Errorf("the brief digest is %d bytes, over its %d budget", len(out), Budget)
			}
			assertHeaderOrder(t, out, []string{
				"== SESSION LOCK ==",
				"== WAKE QUEUE ==",
				"== SUPERVISION OPERATING INSTRUCTIONS ==",
				"== FLEET ==",
				"== READ THIS NEXT ==",
				"== READ-ONCE CONTRACT ==",
				"== NEXT STEP ==",
			})
			if !strings.Contains(out, "WAKE QUEUE: 300 pending") {
				t.Errorf("the brief does not say how many wakes are pending:\n%s", out)
			}
			if test.says == "" {
				if strings.Contains(out, "AFK MODE") {
					t.Errorf("the brief mentions AFK mode while it is off:\n%s", out)
				}
				return
			}
			assertHeaderOrder(t, out, []string{"== SESSION LOCK ==", "== AFK MODE ==", test.says, "== WAKE QUEUE =="})
			for _, line := range afk.NoticeFor(h.State) {
				if !strings.Contains(out, line) {
					t.Errorf("the brief leaves out this line of AFK mode's notice: %q\n%s", line, out)
				}
			}
		})
	}
}

// A session that does not hold the home changes nothing in it: no long
// digest is written, and the brief says how to print one instead.
func TestABriefForASessionThatDoesNotHoldTheHomeWritesNothing(t *testing.T) {
	h := bigHome(t, 2, 1)
	holder := startLiveForeignProcess(t)
	if _, err := lock.AcquireOwner(h.State, holder.Process.Pid, "other"); err != nil {
		t.Fatal(err)
	}

	out := composeBrief(t, h)

	if _, err := os.Stat(filepath.Join(h.State, FullDigestFile)); !os.IsNotExist(err) {
		t.Errorf("a read-only brief wrote the long digest (%v)", err)
	}
	if _, ok := ReadCompleteMarker(h.State); ok {
		t.Error("a read-only brief wrote the completion marker")
	}
	if !strings.Contains(out, "cfo session-start") || !strings.Contains(out, readOnlyBannerTitle) {
		t.Errorf("a read-only brief does not say how to print the long digest:\n%s", out)
	}
}
