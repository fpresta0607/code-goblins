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
	pair, err := Assign(stateDir, "Refund totals in the export")

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
	pair, err := Assign(stateDir, "")

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
			pair, err := pick("", heldBut(tc.spelled, free), nil)

			// Assert
			if err != nil || pair.Name != free {
				t.Fatalf("pair = %+v, %v, want %s", pair, err, free)
			}
		})
	}
}

func TestPickRefusesWhenEveryNameIsHeldByALiveGoblin(t *testing.T) {
	// Act
	pair, err := pick("", heldBut(asSpelled), nil)

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

	// Act
	pair, err := pick("", heldBut(asSpelled, free...), recent)

	// Assert
	if err != nil || pair.Name != free[2] {
		t.Fatalf("pair = %+v, %v, want %s, the one free name no recent spawn used", pair, err, free[2])
	}
}

func TestPickReusesARecentNameOnlyWhenNoOtherIsFree(t *testing.T) {
	// Arrange: live goblins hold every name but one, which a recent spawn
	// used. Live goblins are the hard rule, recent spawns the preference.
	free := firstNames[0]

	// Act
	pair, err := pick("", heldBut(asSpelled, free), []Pair{{Name: free, Title: "Bug Hunter"}})

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
		pair, err := Assign(stateDir, "")
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

func TestAssignFitsTheTitleToTheWorkWhereTheHintMakesItObvious(t *testing.T) {
	cases := []struct {
		name   string
		hint   string
		titles []string
	}{
		{name: "a board task", hint: "Show goblin names on the board cards and the canvas", titles: themeTitles("board")},
		{name: "a flaky test hunt", hint: "Hunt the flaky gate test that hangs", titles: themeTitles("bugs")},
		{name: "a docs task", hint: "Rewrite the README guide", titles: themeTitles("docs")},
		{name: "a task id alone", hint: "cg-merge-train", titles: themeTitles("delivery")},
		{name: "nothing obvious", hint: "Refund totals in the export", titles: genericTitles},
		{name: "no hint", hint: "", titles: genericTitles},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()

			// Act
			pair, err := Assign(stateDir, tc.hint)

			// Assert
			if err != nil || !slices.Contains(tc.titles, pair.Title) {
				t.Fatalf("pair = %+v, %v, want a title from %v", pair, err, tc.titles)
			}
		})
	}
}

func TestAssignGivesAFreshTitleEveryTimeAndFallsBackToAGenericOne(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	board := themeTitles("board")

	// Act
	var titles []string
	for range len(board) + 1 {
		pair, err := Assign(stateDir, "board cards")
		if err != nil {
			t.Fatal(err)
		}
		titles = append(titles, pair.Title)
	}

	// Assert
	themed := titles[:len(board)]
	if !slices.Equal(slices.Sorted(slices.Values(themed)), slices.Sorted(slices.Values(board))) {
		t.Fatalf("titles = %v, want each board title once before any repeats", titles)
	}
	if last := titles[len(board)]; !slices.Contains(genericTitles, last) {
		t.Fatalf("title %q once every board title is recent, want a generic one", last)
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

func TestHintReadsTheTaskSectionOfTheBriefOnly(t *testing.T) {
	// Arrange
	brief := "# Brief cg-x\n\n## Task\n\nShow names on the board.\nAnd the canvas.\n\n## Constraints\n\nNever touch the merge train.\n"

	// Act
	hint := Hint("Goblin names", "cg-x", brief)

	// Assert
	if hint != "Goblin names cg-x Show names on the board.\nAnd the canvas." {
		t.Fatalf("hint = %q", hint)
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
	for _, theme := range themes {
		titles = append(titles, theme.titles...)
	}
	seenTitle := map[string]bool{}
	for _, title := range titles {
		if seenTitle[title] || len(title) > 20 || strings.ContainsAny(title, ";\u2014") {
			t.Errorf("title %q repeats, runs past 20 characters or holds a semicolon or em dash", title)
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

func themeTitles(key string) []string {
	for _, theme := range themes {
		if theme.key == key {
			return theme.titles
		}
	}
	panic("no theme " + key)
}
