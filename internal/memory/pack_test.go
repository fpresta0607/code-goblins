package memory

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func harborPackInput() PackInput {
	return PackInput{
		Task:    "harbor-saves",
		Project: "harbor",
		Title:   "Harbor configurator saves",
		Brief:   "# Brief harbor-saves\n\n## Task\n\nStore configurator saves in the hosted database and deploy the harbor site.\n\n## Constraints\n\nUnrelated text about lighthouse keepers.\n",
		Budget:  DefaultPackBudget,
		Now:     indexDate,
		Live: []LiveTask{
			{ID: "harbor-gallery-2", Title: "Gallery captions", Goblin: "Ivy", Branch: "feat/gallery-captions", PullRequest: "https://github.com/acme/harbor/pull/11", Status: "working: captions under review"},
		},
		Commits: []string{"abc1234 feat(gallery): lazy images"},
	}
}

func TestBuildPackCarriesScopedMemoryAndLiveStateWithinBudget(t *testing.T) {
	index := buildFixtureIndex(t)

	pack, err := index.BuildPack(harborPackInput())

	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Context pack: harbor, harbor-saves",
		"## " + sectionRulings, "Merging to main deploys the harbor site to production.", "2026-11-05 Deploys go by his word",
		"## " + sectionFacts, "harbor-database-is-live (", "Supersedes harbor-has-no-hosted-database.",
		"## " + sectionLive, "harbor-gallery-2 (Ivy): Gallery captions; branch feat/gallery-captions; pull request https://github.com/acme/harbor/pull/11; last report: working: captions under review", "on main since your base: abc1234",
		"## " + sectionDone, "2026-11-08 harbor-live: Harbor configurator saves - PR https://github.com/acme/harbor/pull/9",
		"cfo memory recall \"<question>\" --project harbor",
	} {
		if !strings.Contains(pack.Text, want) {
			t.Fatalf("the pack lacks %q:\n%s", want, pack.Text)
		}
	}
	for _, absent := range []string{"saves stay in the browser", "northwind", "lighthouse"} {
		if strings.Contains(pack.Text, absent) {
			t.Fatalf("the pack carries %q:\n%s", absent, pack.Text)
		}
	}
	if gallery := "- 2026-10-22 harbor-gallery: Pearl: harbor gallery with lazy images (repo: harbor) https://github.com/acme/harbor/pull/7 (done 2026-10-22) - lesson: lazy images keep the gallery fast\n"; !strings.Contains(pack.Text, gallery) || strings.Count(pack.Text, "harbor gallery with lazy images") != 1 {
		t.Fatalf("an archived report must appear once, with its lesson:\n%s", pack.Text)
	}
	if pack.Tokens > pack.Budget || pack.Tokens != Tokens(len(pack.Text)) || pack.Shown != len(pack.Selected) || pack.Unchanged != 0 {
		t.Fatalf("pack accounting %+v", pack)
	}
}

func TestBuildPackNeverExceedsItsBudget(t *testing.T) {
	index := buildFixtureIndex(t)
	input := harborPackInput()
	for i := range 40 {
		input.Live = append(input.Live, LiveTask{ID: fmt.Sprintf("harbor-task-%d", i), Title: strings.Repeat("long title ", 20), Status: strings.Repeat("working: busy ", 30)})
	}
	for _, budget := range []int{150, 400, 1000, DefaultPackBudget} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			input.Budget = budget

			pack, err := index.BuildPack(input)

			if err != nil || pack.Tokens > budget || !strings.Contains(pack.Text, "Need more?") {
				t.Fatalf("budget %d gave %d tokens: %v\n%s", budget, pack.Tokens, err, pack.Text)
			}
		})
	}
}

func TestBuildPackLeadsWithWhatMatchesTheTasksSubjectOverItsBriefsGeneralWords(t *testing.T) {
	dataDir, stateDir := writeIndexFixture(t)
	// A long brief's many general words, each shared by a quarter of the
	// facts, so every gallery note matches more of the brief than the fact
	// about the task's own subject does.
	var words []string
	for number := range 40 {
		words = append(words, fmt.Sprintf("topic%02d", number))
	}
	for number := range 80 {
		var shared []string
		for offset := range 15 {
			shared = append(shared, words[(number+offset)%len(words)])
		}
		writeFile(t, filepath.Join(dataDir, "memory", fmt.Sprintf("gallery-note-%d.md", number)), fmt.Sprintf("---\nname: gallery-note-%d\ndescription: %s\n---\n\nA general note.\n", number, strings.Join(shared, " ")))
	}
	index, err := BuildIndex(dataDir, stateDir, indexDate)
	if err != nil {
		t.Fatal(err)
	}
	input := PackInput{Task: "harbor-saves", Project: "harbor", Title: "Harbor hosted database saves", Brief: "## Task\n\n" + strings.Join(words, " ") + "\n", Budget: 600, Now: indexDate}

	pack, err := index.BuildPack(input)

	if err != nil || !strings.Contains(pack.Text, "harbor-database-is-live (") || !strings.Contains(pack.Text, "gallery-note-") {
		t.Fatalf("%v\n%s", err, pack.Text)
	}
}

func TestBuildPackLeavesOutFactsScopedToAnotherRoleOrTask(t *testing.T) {
	dataDir, stateDir := writeIndexFixture(t)
	for name, metadata := range map[string]string{"harbor-ceo-only": "roles: [ceo]", "harbor-other-task": "task: harbor-gallery", "harbor-this-task": "task: harbor-saves", "harbor-goblins": "roles: [goblin]"} {
		writeFile(t, filepath.Join(dataDir, "memory", name+".md"), fmt.Sprintf("---\nname: %s\ndescription: harbor configurator saves note\nmetadata:\n  %s\n---\n\nharbor configurator saves.\n", name, metadata))
	}
	index, err := BuildIndex(dataDir, stateDir, indexDate)
	if err != nil {
		t.Fatal(err)
	}

	pack, err := index.BuildPack(harborPackInput())

	if err != nil {
		t.Fatal(err)
	}
	for id, isWanted := range map[string]bool{"harbor-this-task": true, "harbor-goblins": true, "harbor-ceo-only": false, "harbor-other-task": false} {
		if strings.Contains(pack.Text, id+" (") != isWanted {
			t.Errorf("%s in the pack is %v, want %v", id, !isWanted, isWanted)
		}
	}
}

func TestBuildPackForAResumedConversationShowsOnlyWhatChanged(t *testing.T) {
	index := buildFixtureIndex(t)
	input := harborPackInput()
	first, err := index.BuildPack(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Delivered = first.Selected

	unchanged, err := index.BuildPack(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Live[0].Status = "done: PR https://github.com/acme/harbor/pull/12"
	changed, err := index.BuildPack(input)
	if err != nil {
		t.Fatal(err)
	}

	if unchanged.Shown != 0 || unchanged.Unchanged != len(first.Selected) || strings.Contains(unchanged.Text, "Merging to main") || !strings.Contains(unchanged.Text, fmt.Sprintf("%d entries you already hold", len(first.Selected))) {
		t.Fatalf("an unchanged resume repeated entries:\n%s", unchanged.Text)
	}
	if changed.Shown != 1 || !strings.Contains(changed.Text, "pull/12") || strings.Contains(changed.Text, "Merging to main") {
		t.Fatalf("a changed entry was not the only one shown:\n%s", changed.Text)
	}
}

func TestBuildPackSaysWhenNothingMatches(t *testing.T) {
	index := buildFixtureIndex(t)

	pack, err := index.BuildPack(PackInput{Task: "lone", Project: "elsewhere", Title: "zzz", Brief: "qqq", Budget: DefaultPackBudget, Now: indexDate, CommitsUnread: "origin/HEAD is unknown"})

	if err != nil || !strings.Contains(pack.Text, "Commits on main since your base could not be read: origin/HEAD is unknown") || strings.Contains(pack.Text, "## "+sectionRulings) {
		t.Fatalf("%v\n%s", err, pack.Text)
	}
	empty, err := index.BuildPack(PackInput{Task: "lone", Project: "elsewhere", Title: "zzz", Brief: "qqq", Budget: DefaultPackBudget, Now: indexDate})
	if err != nil || !strings.Contains(empty.Text, "No memory or live state matched this task.") {
		t.Fatalf("%v\n%s", err, empty.Text)
	}
}
