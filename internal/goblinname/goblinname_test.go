package goblinname

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// liveGoblin writes the record of a running goblin that holds pair, with a
// key the typed record does not carry so a rewrite that drops it shows.
func liveGoblin(t *testing.T, stateDir, id string, pair Pair) {
	t.Helper()
	record := map[string]string{"harness": "claude", "kind": "ship", "mode": "direct-PR", "project": `C:\northwind-api`, "spawn_gen": "s1", "pr": "https://github.com/fpresta0607/northwind-api/pull/412"}
	if pair.Name != "" {
		record["goblin_name"], record["goblin_title"] = pair.Name, pair.Title
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A plain write, since a full fleet's worth of atomic ones is slow here.
	var lines strings.Builder
	for _, key := range slices.Sorted(maps.Keys(record)) {
		lines.WriteString(key + "=" + record[key] + "\n")
	}
	if err := os.WriteFile(state.TaskMetaPath(stateDir, id), []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readRecord(t *testing.T, stateDir, id string) map[string]string {
	t.Helper()
	record, err := state.ReadMeta(state.TaskMetaPath(stateDir, id))
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestAssignGivesANameAndTitleFromTheCatalogAndRemembersIt(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()

	// Act
	pair, err := Assign(stateDir, Work{ID: "nw-sum", Title: "Make the numbers add up"})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(firstNames, pair.Name) || !slices.Contains(genericTitles, pair.Title) {
		t.Fatalf("pair = %+v, want a catalog name and a generic title", pair)
	}
	recent, err := readRecent(stateDir)
	if err != nil || !slices.Equal(recent, []Pair{pair}) {
		t.Fatalf("recent = %v, %v, want just %+v", recent, err, pair)
	}
}

// The live goblins' names are read from their records: one full fleet end
// to end, since each record costs time to write and clean up here. The rules
// themselves are tested on pick.
func TestAssignNeverGivesANameALiveGoblinHolds(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	free := firstNames[len(firstNames)/2]
	for i, name := range firstNames {
		if name != free {
			liveGoblin(t, stateDir, fmt.Sprintf("task-%d", i), Pair{Name: name, Title: "Code Designer"})
		}
	}

	// Act
	pair, err := Assign(stateDir, Work{})

	// Assert
	if err != nil || pair.Name != free {
		t.Fatalf("pair = %+v, %v, want the one name no live goblin holds, %s", pair, err, free)
	}
}

// heldBut is a live fleet holding every catalog name but free, each written
// as spelled writes it.
func heldBut(spelled func(string) string, free ...string) []Pair {
	var live []Pair
	for _, name := range firstNames {
		if !slices.Contains(free, name) {
			live = append(live, Pair{Name: spelled(name), Title: "Code Designer"})
		}
	}
	return live
}

func asSpelled(name string) string { return name }

func TestPickNeverGivesANameALiveGoblinHoldsWhateverItsCase(t *testing.T) {
	cases := []struct {
		name    string
		spelled func(string) string
	}{
		{name: "as the catalog spells it", spelled: asSpelled},
		{name: "in another case", spelled: strings.ToUpper},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			free := firstNames[7]

			// Act
			pair, err := pick(Work{}, heldBut(tc.spelled, free), nil)

			// Assert
			if err != nil || pair.Name != free {
				t.Fatalf("pair = %+v, %v, want %s", pair, err, free)
			}
		})
	}
}

func TestPickRefusesWhenEveryNameIsHeldByALiveGoblin(t *testing.T) {
	// Act
	pair, err := pick(Work{}, heldBut(asSpelled), nil)

	// Assert
	if err == nil {
		t.Fatalf("pair = %+v, want a refusal while every name is held", pair)
	}
}

func TestPickKeepsClearOfTheNamesTheLastSpawnsUsed(t *testing.T) {
	// Arrange: live goblins hold every name but three, and the last spawns
	// used two of those three.
	free := firstNames[:3]
	recent := []Pair{{Name: free[0], Title: "Bug Hunter"}, {Name: free[1], Title: "Word Smith"}}

	// Act and assert: pick chooses at random, so a pick blind to recent
	// spawns would land on the right name a third of the time; many picks
	// leave it no such luck.
	for range 50 {
		pair, err := pick(Work{}, heldBut(asSpelled, free...), recent)
		if err != nil || pair.Name != free[2] {
			t.Fatalf("pair = %+v, %v, want %s, the one free name no recent spawn used", pair, err, free[2])
		}
	}
}

func TestPickReusesARecentNameOnlyWhenNoOtherIsFree(t *testing.T) {
	// Arrange: live goblins hold every name but one, which a recent spawn
	// used. Live goblins are the hard rule, recent spawns the preference.
	free := firstNames[0]

	// Act
	pair, err := pick(Work{}, heldBut(asSpelled, free), []Pair{{Name: free, Title: "Bug Hunter"}})

	// Assert
	if err != nil || pair.Name != free {
		t.Fatalf("pair = %+v, %v, want %s", pair, err, free)
	}
}

func TestAssignForgetsPairsOlderThanTheRecentWindow(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()

	// Act
	var assigned []Pair
	for range RecentWindow + 5 {
		pair, err := Assign(stateDir, Work{})
		if err != nil {
			t.Fatal(err)
		}
		assigned = append(assigned, pair)
	}

	// Assert
	recent, err := readRecent(stateDir)
	if err != nil || !slices.Equal(recent, assigned[5:]) {
		t.Fatalf("recent = %v, %v, want the last %d of %v", recent, err, RecentWindow, assigned)
	}
	names := map[string]bool{}
	for _, pair := range recent {
		if names[pair.Name] {
			t.Fatalf("%s was given twice within the last %d spawns: %v", pair.Name, RecentWindow, recent)
		}
		names[pair.Name] = true
	}
}

// The Overlord, 2026-10-08: "make their role title a little more specific
// to the task they are actually doing but still keep it short and fun". A
// title names the subject of the work, here for real rows of the fleet's
// backlog.
func TestAssignNamesTheSubjectOfRealBacklogTasks(t *testing.T) {
	cases := []struct {
		work Work
		want string
	}{
		{work: Work{ID: "pd-whats-new", Title: "Announce the PrecisionDocs connector changes inside the app with a What's new that looks great"}, want: "Whats-New Wizard"},
		{work: Work{ID: "cg-voice-long", Title: `A long dictation of any length is heard and typed in full, never "Nothing was heard" (Moonshine tiny since PR 488); Claude Code`}, want: "Voice Whisperer"},
		{work: Work{ID: "cg-resume-smooth", Title: "One click starts or resumes a goblin quickly, a message to a busy goblin is queued, and no yellow error line shows; Claude Code"}, want: "Resume Wrangler"},
		{work: Work{ID: "cg-tree-polish", Title: "On the Orchestration family tree, branches meet the top of each baby goblin and a click on a baby goblin opens its agent terminal; Claude Code"}, want: "Branch Bender"},
		{work: Work{ID: "cg-fly-token-durable", Title: "PrecisionDocs' fly service stays green until its token's own expiry, so PrecisionDocs goblins are never refused for Fly; Claude Code"}, want: "Token Tamer"},
		{work: Work{ID: "cg-openconsole", Title: "Goblin terminals run on OpenConsole (ConPTY 1.25) with a host screen model that answers its cursor query and repaints viewers, so keys never strand and conhost's crash cannot kill a terminal; Claude Code"}, want: "Console Captain"},
		{work: Work{ID: "cg-stall-evidence", Title: "A goblin is called stalled only when its screen, transcript and process tree all stop, so the CFO is never woken for a goblin that is plainly busy; Claude Code"}, want: "Stall Sleuth"},
		{work: Work{ID: "cg-test-hygiene", Title: "Tests stop reading the live checkout or timing out under load, and proc.Processes fills or drops Start (cg-harness-capacity's findings); Claude Code"}, want: "Test Tamer"},
		{work: Work{ID: "cfo-no-mistakes-update", Title: "Install no-mistakes built from his fork (fpresta0607/no-mistakes main 8cf1c98e3 or later) in a quiet window"}, want: "Gate Guardian"},
		{work: Work{ID: "pd-agent-api-v2-later", Title: "The agent API rows left after area A: row 26 and row 27"}, want: "API Artisan"},
		{work: Work{ID: "cg-readme-pictures", Title: "The README's pictures show today's Code Goblins (board, family tree, desktop window, Scrawl review), light and accurate"}, want: "Picture Painter"},
		{work: Work{ID: "cg-repo-tickets", Title: "Tickets and contributors in collaborative repos"}, want: "Ticket Tamer"},
		{work: Work{ID: "cg-helper-goblins", Title: "Goblins start helper goblins through the supervisor: memory admission, one helper per goblin, no helpers of helpers, the parent merges"}, want: "Helper Herder"},
		{work: Work{ID: "cg-tidy-home", Title: "One home for everything Code Goblins writes, kept small (overlord.md, 2026-10-05); Claude Code"}, want: "Home Keeper"},
		{work: Work{ID: "cg-merge-train", Title: "A merge train lands many green PRs with one CI run, bisecting on failure"}, want: "Train Conductor"},
		{work: Work{ID: "cg-quick-tour", Title: "A very quick tour for new users: how Code Goblins works and how to use the board, on first open; Claude Code"}, want: "Tour Guide"},
	}
	for _, tc := range cases {
		t.Run(tc.work.ID, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()

			// Act
			pair, err := Assign(stateDir, tc.work)

			// Assert
			if err != nil || pair.Title != tc.want {
				t.Fatalf("pair = %+v, %v, want the title %q", pair, err, tc.want)
			}
		})
	}
}

func TestAssignReadsTheTaskSectionOnlyWhenTheTitleAndIDNameNoSubject(t *testing.T) {
	cases := []struct {
		name string
		work Work
		want string
	}{
		{name: "the title names one", work: Work{ID: "cg-x1", Title: "Smooth resumes", Task: "Hear long dictation. The voice must be heard in full."}, want: "Resume Wrangler"},
		{name: "the id names one", work: Work{ID: "cg-resume-smooth", Title: "Make it right", Task: "Hear long dictation. The voice must be heard in full."}, want: "Resume Wrangler"},
		{name: "only the Task section names one", work: Work{ID: "cg-x1", Title: "Make it right", Task: "Hear long dictation. The voice must be heard in full."}, want: "Voice Whisperer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			pair, err := Assign(t.TempDir(), tc.work)

			// Assert
			if err != nil || pair.Title != tc.want {
				t.Fatalf("pair = %+v, %v, want the title %q", pair, err, tc.want)
			}
		})
	}
}

func TestAssignGivesAGenericTitleOnlyWhenTheTaskNamesNoSubject(t *testing.T) {
	cases := []struct {
		name string
		work Work
	}{
		{name: "nothing named", work: Work{ID: "nw-sum", Title: "Make the numbers add up", Task: "The totals should come out right."}},
		{name: "nothing given", work: Work{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			pair, err := Assign(t.TempDir(), tc.work)

			// Assert
			if err != nil || !slices.Contains(genericTitles, pair.Title) {
				t.Fatalf("pair = %+v, %v, want a generic title", pair, err)
			}
		})
	}
}

func TestAssignGivesEachGoblinOnOneSubjectAnotherTitleNamingIt(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	work := Work{ID: "cg-token", Title: "Keep the Fly token green"}

	// Act
	var titles []string
	for range RecentWindow {
		pair, err := Assign(stateDir, work)
		if err != nil {
			t.Fatal(err)
		}
		titles = append(titles, pair.Title)
	}

	// Assert
	if titles[0] != "Token Tamer" {
		t.Fatalf("first title = %q, want Token Tamer", titles[0])
	}
	for i, title := range titles {
		if !strings.HasPrefix(title, "Token ") || slices.Contains(titles[:i], title) {
			t.Fatalf("titles = %v, want each a new title naming the token", titles)
		}
	}
}

func TestBackfillNamesEveryLiveGoblinWithoutOneAndKeepsTheRestOfItsRecord(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	held := Pair{Name: firstNames[0], Title: "Code Designer"}
	liveGoblin(t, stateDir, "nw-sync", Pair{})
	liveGoblin(t, stateDir, "nw-named", held)
	liveGoblin(t, stateDir, "nw-export", Pair{})
	before := readRecord(t, stateDir, "nw-sync")

	// Act
	err := Backfill(stateDir)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := readRecord(t, stateDir, "nw-named"); got["goblin_name"] != held.Name || got["goblin_title"] != held.Title {
		t.Fatalf("named goblin's record = %v, want its pair kept", got)
	}
	sync, export := readRecord(t, stateDir, "nw-sync"), readRecord(t, stateDir, "nw-export")
	for id, record := range map[string]map[string]string{"nw-sync": sync, "nw-export": export} {
		if !slices.Contains(firstNames, record["goblin_name"]) || record["goblin_title"] == "" || record["goblin_name"] == held.Name {
			t.Fatalf("%s's record = %v, want a fresh pair", id, record)
		}
	}
	if sync["goblin_name"] == export["goblin_name"] {
		t.Fatalf("both goblins were named %s", sync["goblin_name"])
	}
	want := maps.Clone(before)
	want["goblin_name"], want["goblin_title"] = sync["goblin_name"], sync["goblin_title"]
	if !maps.Equal(sync, want) {
		t.Fatalf("record = %v, want %v", sync, want)
	}
}

func TestBackfillOfANamedFleetChangesNothing(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	liveGoblin(t, stateDir, "nw-named", Pair{Name: firstNames[0], Title: "Code Designer"})

	// Act
	err := Backfill(stateDir)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, recentFile)); !os.IsNotExist(err) {
		t.Fatalf("recent pairs file stat = %v, want none written when nobody needed a name", err)
	}
}

func TestWorkOfReadsTheTaskSectionOfTheBriefOnly(t *testing.T) {
	// Arrange
	brief := "# Brief cg-x\n\n## Task\n\nShow names on the board.\nAnd the canvas.\n\n## Constraints\n\nNever touch the merge train.\n"

	// Act
	work := WorkOf("cg-x", "Goblin names", brief)

	// Assert
	if want := (Work{ID: "cg-x", Title: "Goblin names", Task: "Show names on the board.\nAnd the canvas."}); work != want {
		t.Fatalf("work = %+v, want %+v", work, want)
	}
}

// A task gets its goblin's pair when it is queued and keeps it while it
// waits, and every queued task gets a name of its own.
func TestReserveNamesEachQueuedTaskOnceAndKeepsItsPair(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	voice, resume := Work{ID: "cg-voice-long", Title: "Long dictation is heard in full"}, Work{ID: "cg-resume-smooth", Title: "Smooth resumes"}
	token := Work{ID: "cg-fly-token", Title: "Keep the Fly token green"}

	// Act
	first, err := Reserve(stateDir, []Work{voice, resume})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Reserve(stateDir, []Work{voice, resume, token})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if first[voice.ID].Title != "Voice Whisperer" || first[resume.ID].Title != "Resume Wrangler" {
		t.Fatalf("first = %+v, want Voice Whisperer and Resume Wrangler", first)
	}
	if second[voice.ID] != first[voice.ID] || second[resume.ID] != first[resume.ID] || second[token.ID].Title != "Token Tamer" {
		t.Fatalf("second = %+v, want the first pairs kept and a Token Tamer", second)
	}
	names := map[string]bool{}
	for _, pair := range second {
		if !slices.Contains(firstNames, pair.Name) || names[pair.Name] {
			t.Fatalf("pairs = %+v, want a catalog name of its own for each", second)
		}
		names[pair.Name] = true
	}
	if kept, err := ReadQueued(stateDir); err != nil || !maps.Equal(kept, second) {
		t.Fatalf("kept = %+v, %v, want %+v", kept, err, second)
	}
}

func TestReserveForgetsATaskNoLongerQueued(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	gone, staying := Work{ID: "cg-gone"}, Work{ID: "cg-staying"}
	first, err := Reserve(stateDir, []Work{gone, staying})
	if err != nil {
		t.Fatal(err)
	}

	// Act
	second, err := Reserve(stateDir, []Work{staying})

	// Assert
	if err != nil || !maps.Equal(second, map[string]Pair{staying.ID: first[staying.ID]}) {
		t.Fatalf("second = %+v, %v, want only %s's pair %+v", second, err, staying.ID, first[staying.ID])
	}
}

// The rules of names hold across queued and live tasks: one full fleet end
// to end each way.
func TestReserveNeverGivesANameALiveGoblinHolds(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	free := firstNames[len(firstNames)/3]
	for i, name := range firstNames {
		if name != free {
			liveGoblin(t, stateDir, fmt.Sprintf("task-%d", i), Pair{Name: name, Title: "Code Designer"})
		}
	}

	// Act
	pairs, err := Reserve(stateDir, []Work{{ID: "cg-queued"}})

	// Assert
	if err != nil || pairs["cg-queued"].Name != free {
		t.Fatalf("pairs = %+v, %v, want the one name no live goblin holds, %s", pairs, err, free)
	}
}

func TestAssignNeverGivesANameAQueuedTaskHolds(t *testing.T) {
	// Arrange: queued tasks hold every name but one.
	stateDir := t.TempDir()
	var queued []Work
	for i := range len(firstNames) - 1 {
		queued = append(queued, Work{ID: fmt.Sprintf("queued-%d", i)})
	}
	held, err := Reserve(stateDir, queued)
	if err != nil {
		t.Fatal(err)
	}
	free := slices.DeleteFunc(slices.Clone(firstNames), func(name string) bool {
		return slices.ContainsFunc(slices.Collect(maps.Values(held)), func(pair Pair) bool { return pair.Name == name })
	})

	// Act
	pair, err := Assign(stateDir, Work{ID: "cg-not-queued"})

	// Assert
	if err != nil || len(free) != 1 || pair.Name != free[0] {
		t.Fatalf("pair = %+v, %v, want the one name no queued task holds, %v", pair, err, free)
	}
}

func TestAssignGivesAQueuedTaskThePairItWasQueuedWith(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	queued, err := Reserve(stateDir, []Work{{ID: "cg-x", Title: "Smooth resumes"}})
	if err != nil {
		t.Fatal(err)
	}

	// Act: the spawn's own title names another subject.
	pair, err := Assign(stateDir, Work{ID: "cg-x", Title: "Keep the Fly token green"})

	// Assert
	if err != nil || pair != queued["cg-x"] {
		t.Fatalf("pair = %+v, %v, want the queued pair %+v", pair, err, queued["cg-x"])
	}
	if recent, err := readRecent(stateDir); err != nil || !slices.Equal(recent, []Pair{pair}) {
		t.Fatalf("recent = %v, %v, want the spawn's pair", recent, err)
	}
}

func TestBackfillGivesALiveGoblinThePairItWasQueuedWith(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	queued, err := Reserve(stateDir, []Work{{ID: "nw-sync", Title: "Smooth resumes"}})
	if err != nil {
		t.Fatal(err)
	}
	liveGoblin(t, stateDir, "nw-sync", Pair{})

	// Act
	err = Backfill(stateDir)

	// Assert
	if record := readRecord(t, stateDir, "nw-sync"); err != nil || record["goblin_name"] != queued["nw-sync"].Name || record["goblin_title"] != queued["nw-sync"].Title {
		t.Fatalf("record = %v, %v, want the queued pair %+v", record, err, queued["nw-sync"])
	}
}

func TestCatalogNamesAreShortSingleWordsWithNoRepeats(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range firstNames {
		key := strings.ToLower(name)
		if seen[key] || len(name) > 8 || strings.ContainsFunc(name, func(r rune) bool { return r < 'A' || r > 'z' || (r > 'Z' && r < 'a') }) {
			t.Errorf("name %q repeats, runs past 8 letters or holds more than letters", name)
		}
		seen[key] = true
	}
	// The recent window and a full fleet together must leave names free.
	if len(firstNames) < 4*RecentWindow+60 {
		t.Fatalf("%d names, want room for %d recent spawns and a large fleet", len(firstNames), RecentWindow)
	}
	titles := slices.Clone(genericTitles)
	words := map[string]bool{}
	for _, subject := range subjects {
		if words[subject.word] || len(subject.names) == 0 {
			t.Errorf("subject %q repeats or has no names", subject.word)
		}
		words[subject.word] = true
		for _, name := range subject.names {
			if name != strings.ToLower(name) || strings.Join(strings.Fields(name), " ") != name {
				t.Errorf("subject %q's name %q is not lower case words", subject.word, name)
			}
		}
		titles = append(titles, subject.titles()...)
	}
	seenTitle := map[string]bool{}
	for _, title := range titles {
		if seenTitle[title] || len(title) > 20 || strings.ContainsAny(title, ";\u2014") || len(strings.Fields(title)) < 2 || len(strings.Fields(title)) > 3 {
			t.Errorf("title %q repeats, runs past 20 characters or 3 words, or holds a semicolon or em dash", title)
		}
		seenTitle[title] = true
	}
}

func TestCalledNamesAGoblinByNameAndID(t *testing.T) {
	cases := []struct{ name, id, want string }{
		{name: "Jerry", id: "cg-x", want: "Jerry (cg-x)"},
		{name: "", id: "cg-x", want: "cg-x"},
	}
	for _, tc := range cases {
		if got := Called(tc.name, tc.id); got != tc.want {
			t.Errorf("Called(%q, %q) = %q, want %q", tc.name, tc.id, got, tc.want)
		}
	}
}
