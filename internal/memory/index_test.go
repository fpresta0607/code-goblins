package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var indexDate = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// writeIndexFixture is a small fleet home: two dated ruling sections, facts
// where one supersedes another, a project-scoped fact, an archive, a done
// archive and one live task's status log.
func writeIndexFixture(t *testing.T) (dataDir, stateDir string) {
	t.Helper()
	root := t.TempDir()
	dataDir, stateDir = filepath.Join(root, "data"), filepath.Join(root, "state")
	files := map[string]string{
		"data/overlord.md":                             "## Deploys go by his word, 2026-11-05 about 10:00Z\n\nMerging to main deploys the harbor site to production.\n<!-- memory tiers -->\nEvery deploy waits for his word.\n\n# Standing Overlord directives\n\n## Undated heading\n\nThe board renders only what changed.\n",
		"data/memory/MEMORY.md":                        "# Memory index\n- [Harbor database](harbor-has-no-hosted-database.md)\n",
		"data/memory/harbor-has-no-hosted-database.md": "---\nname: harbor-has-no-hosted-database\ndescription: \"harbor saves stay in the browser, no hosted database\"\nmetadata:\n  type: project\n  modified: 2026-10-01T08:00:00.000Z\n---\n\nThe harbor site has no hosted database, so saves stay in the browser.\n",
		"data/memory/harbor-database-is-live.md":       "---\nname: harbor-database-is-live\ndescription: \"harbor's hosted database is live since 11-05\"\nmetadata:\n  type: project\n  modified: 2026-11-05T09:00:00.000Z\n  supersedes: [harbor-has-no-hosted-database]\n  projects: [harbor]\n---\n\nThe harbor hosted database went live on 2026-11-05; saves are server side.\n",
		"data/memory/northwind-cache-port.md":          "---\nname: northwind-cache-port\ndescription: northwind's cache listens on port 4300\nmetadata:\n  type: reference\n  project: northwind\n  roles: [goblin]\n  modified: 2026-11-02T00:00:00Z\n---\n\nThe northwind cache port is 4300.\n",
		"data/memory/broken-frontmatter.md":            "---\nname: broken\ndescription: never closed\n\nA lighthouse note whose frontmatter never closes.\n",
		"data/memory-archive.md":                       "# Memory archive\n\n## Retired 2026-09-23\n\nThe old harbor cache lived on port 9999.\n",
		"data/done-archive.md":                         "\n## Archived 2026-10-23\n- [x] harbor-gallery - Pearl: harbor gallery with lazy images (repo: harbor) https://github.com/acme/harbor/pull/7 (done 2026-10-22)\n  lesson: lazy images keep the gallery fast\n- [x] harbor-live - an older report (repo: harbor) (done 2026-10-01)\n",
		"state/harbor-live.status":                     "2026-11-08T10:00:00Z working: building\n2026-11-08T12:00:00Z done: PR https://github.com/acme/harbor/pull/9\n",
		"state/harbor-live.meta":                       "project=C:\\dev\\harbor\ntitle=Harbor configurator saves\nworktree=C:\\wt\\harbor-live\n",
		"state/still-working.status":                   "2026-11-08T10:00:00Z working: building\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dataDir, stateDir
}

func buildFixtureIndex(t *testing.T) *Index {
	t.Helper()
	dataDir, stateDir := writeIndexFixture(t)
	index, err := BuildIndex(dataDir, stateDir, indexDate)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func itemByID(t *testing.T, index *Index, id string) Item {
	t.Helper()
	position := slices.IndexFunc(index.Items, func(item Item) bool { return item.ID == id })
	if position < 0 {
		t.Fatalf("no item %s among %d", id, len(index.Items))
	}
	return index.Items[position]
}

func TestBuildIndexReadsEveryMemorySourceWithDatesAndScopes(t *testing.T) {
	index := buildFixtureIndex(t)

	kinds := map[string]int{}
	for _, item := range index.Items {
		kinds[item.Kind]++
	}
	if kinds[KindRuling] != 3 || kinds[KindFact] != 4 || kinds[KindArchive] != 1 || kinds[KindDone] != 2 {
		t.Fatalf("kinds %v", kinds)
	}
	ruling := itemByID(t, index, KindRuling+"-"+Hash([]byte("Deploys go by his word, 2026-11-05 about 10:00Z\nEvery deploy waits for his word."))[:12])
	if ruling.Line != 5 || FormatDate(ruling.Date) != "2026-11-05" || ruling.Title != "Deploys go by his word, 2026-11-05 about 10:00Z" {
		t.Fatalf("ruling %+v", ruling)
	}
	if undated := index.Items[slices.IndexFunc(index.Items, func(item Item) bool { return item.Text == "The board renders only what changed." })]; !undated.Date.IsZero() {
		t.Fatalf("a heading without a date gave %s", undated.Date)
	}
	live := itemByID(t, index, "harbor-database-is-live")
	if FormatDate(live.Date) != "2026-11-05" || !slices.Equal(live.Projects, []string{"harbor"}) || !slices.Equal(live.Supersedes, []string{"harbor-has-no-hosted-database"}) {
		t.Fatalf("fact %+v", live)
	}
	if port := itemByID(t, index, "northwind-cache-port"); !slices.Equal(port.Projects, []string{"northwind"}) || !slices.Equal(port.Roles, []string{"goblin"}) {
		t.Fatalf("scoped fact %+v", port)
	}
	if done := itemByID(t, index, "done-harbor-live"); done.Text != "PR https://github.com/acme/harbor/pull/9" || done.Title != "Harbor configurator saves" || !slices.Equal(done.Projects, []string{"harbor"}) || done.Line != 2 {
		t.Fatalf("the live report must win over the older archived one: %+v", done)
	}
	if gallery := itemByID(t, index, "done-harbor-gallery"); gallery.Text != "lesson: lazy images keep the gallery fast" || !strings.HasPrefix(gallery.Title, "Pearl: harbor gallery with lazy images") || FormatDate(gallery.Date) != "2026-10-22" {
		t.Fatalf("an archived done row must hold its row once, as its title, and its indented lines as its text: %+v", gallery)
	}
	if broken := itemByID(t, index, "broken-frontmatter"); !strings.Contains(broken.Text, "lighthouse") || len(index.Problems) != 1 || !strings.Contains(index.Problems[0], "frontmatter is not closed") {
		t.Fatalf("broken fact %+v problems %v", broken, index.Problems)
	}
	if !index.IsSuperseded("harbor-has-no-hosted-database") || index.IsSuperseded("harbor-database-is-live") {
		t.Fatal("supersession was not recorded")
	}
}

func TestTermsMatchAWordsInflectionsAndDropLoneLetters(t *testing.T) {
	for _, test := range []struct {
		words string
		want  []string
	}{
		{"deploy deploys deployed deploying", []string{"deploy", "deploy", "deploy", "deploy"}},
		{"limits goblins runs running stopped", []string{"limit", "goblin", "run", "run", "stop"}},
		{"entries copied pushes classes boxes", []string{"entry", "copy", "push", "class", "box"}},
		{"status process analysis called missed", []string{"status", "process", "analysis", "call", "miss"}},
		{"seed need red using", []string{"seed", "need", "red", "using"}},
		{"merge merged merges merging", []string{"merg", "merg", "merg", "merg"}},
		{"create created use uses", []string{"creat", "creat", "use", "use"}},
		{"the repository's PR is a fix", []string{"repository", "pr", "fix"}},
		{"Ångström ünïcode", []string{"ångström", "ünïcod"}},
	} {
		if got := Terms(test.words); !slices.Equal(got, test.want) {
			t.Errorf("Terms(%q) = %q, want %q", test.words, got, test.want)
		}
	}
}

func TestBuildIndexReadsTheEdgeShapesOfItsSources(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	writeFile(t, filepath.Join(dataDir, "overlord.md"), "## Board fixes, 2026-11-01\n#2203 fixed the board's focus race\n### Later\nA line under the later heading.\n")
	writeFile(t, filepath.Join(dataDir, "memory", "closed-at-the-end.md"), "---\nname: closed-at-the-end\ndescription: a fence that ends the file\n---")
	writeFile(t, filepath.Join(dataDir, "memory", "empty-header.md"), "---\n---\nA body under an empty header.\n")

	index, err := BuildIndex(dataDir, filepath.Join(root, "state"), indexDate)

	if err != nil {
		t.Fatal(err)
	}
	issue := index.Items[slices.IndexFunc(index.Items, func(item Item) bool { return item.Text == "#2203 fixed the board's focus race" })]
	if issue.Title != "Board fixes, 2026-11-01" || index.Items[slices.IndexFunc(index.Items, func(item Item) bool { return item.Kind == KindRuling && item.Title == "Later" })].Text != "A line under the later heading." {
		t.Fatalf("a line that starts with # but is no heading was misread: %+v", index.Items)
	}
	if len(index.Problems) != 0 || itemByID(t, index, "closed-at-the-end").Title != "a fence that ends the file" || !strings.Contains(itemByID(t, index, "empty-header").Text, "A body under an empty header.") {
		t.Fatalf("closed frontmatter was read as unclosed: %v %+v", index.Problems, index.Items)
	}
}

func TestShortenedRecallKeepsWhatAReaderIsShownWithinOneReply(t *testing.T) {
	result := RecallResult{Items: []Recalled{{Item: Item{ID: "long", Title: strings.Repeat("title ", 200)}, Excerpt: "short"}}}
	for number := range replyProblems + 3 {
		result.Problems = append(result.Problems, fmt.Sprintf("problem %d %s", number, strings.Repeat("x", 500)))
	}

	shortened := result.Shortened()

	if len(shortened.Items[0].Title) > excerptLimit+3 || len(shortened.Problems) != replyProblems+1 || !strings.HasPrefix(shortened.Problems[replyProblems], "and 3 more;") || len(shortened.Problems[0]) > excerptLimit+3 {
		t.Fatalf("%+v", shortened)
	}
	if len(result.Items[0].Title) != 1200 || len(result.Problems) != replyProblems+3 {
		t.Fatal("shortening changed the full result it was made from")
	}
}

func recalledIDs(result RecallResult) []string {
	var ids []string
	for _, item := range result.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestRecallRanksRelevantItemsAndHidesSupersededAndArchivedOnes(t *testing.T) {
	index := buildFixtureIndex(t)
	for _, test := range []struct {
		name            string
		query           RecallQuery
		first           string
		absent, present []string
	}{
		{"the newer fact answers", RecallQuery{Question: "does harbor have a hosted database", Limit: 5}, "harbor-database-is-live", []string{"harbor-has-no-hosted-database"}, nil},
		{"history shows the superseded fact", RecallQuery{Question: "does harbor have a hosted database", Limit: 5, IsHistory: true}, "", nil, []string{"harbor-has-no-hosted-database"}},
		{"archive stays out", RecallQuery{Question: "harbor cache port", Limit: 5}, "northwind-cache-port", []string{"archive-"}, nil},
		{"history includes the archive", RecallQuery{Question: "old harbor cache port 9999", Limit: 5, IsHistory: true}, "archive-", nil, nil},
		{"rulings answer deploy questions", RecallQuery{Question: "what does merging to main deploy", Limit: 5}, "ruling-", nil, nil},
		{"done reports carry their lessons", RecallQuery{Question: "gallery lazy images lesson", Limit: 5}, "done-harbor-gallery", nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := index.Recall(test.query)
			if err != nil {
				t.Fatal(err)
			}
			ids := recalledIDs(result)
			if len(ids) == 0 || test.first != "" && !strings.HasPrefix(ids[0], test.first) {
				t.Fatalf("first want %s, got %v", test.first, ids)
			}
			for _, absent := range test.absent {
				if slices.ContainsFunc(ids, func(id string) bool { return strings.HasPrefix(id, absent) }) {
					t.Fatalf("%s should be hidden: %v", absent, ids)
				}
			}
			for _, present := range test.present {
				if !slices.Contains(ids, present) {
					t.Fatalf("%s missing: %v", present, ids)
				}
			}
		})
	}
	result, err := index.Recall(RecallQuery{Question: "hosted database", Limit: 5})
	if err != nil || result.Hidden != 1 {
		t.Fatalf("hidden count %d: %v", result.Hidden, err)
	}
	rendered := RenderRecall(RecallQuery{Question: "hosted database"}, result, "built in this process")
	for _, want := range []string{"1. harbor-database-is-live  fact  2026-11-05  ", "harbor-database-is-live.md:1", "harbor's hosted database is live since 11-05", "1 superseded or archived matches hidden", "INDEX PROBLEM:", "never the authority"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered recall lacks %q:\n%s", want, rendered)
		}
	}
}

func TestRecallFiltersExcludeOtherScopesAndKeepFleetWideItems(t *testing.T) {
	index := buildFixtureIndex(t)
	for _, test := range []struct {
		name            string
		query           RecallQuery
		present, absent string
	}{
		{"project keeps its own and fleet-wide", RecallQuery{Question: "harbor database port deploy", Project: "harbor"}, "harbor-database-is-live", "northwind-cache-port"},
		{"project matches without case", RecallQuery{Question: "cache port", Project: "NORTHWIND"}, "northwind-cache-port", "harbor-database-is-live"},
		{"repo names owner and repository", RecallQuery{Question: "cache port database", Repo: "acme/northwind"}, "northwind-cache-port", "harbor-database-is-live"},
		{"role excludes other roles", RecallQuery{Question: "cache port", Role: "ceo"}, "", "northwind-cache-port"},
		{"role keeps its own", RecallQuery{Question: "cache port", Role: "goblin"}, "northwind-cache-port", ""},
		{"task excludes other tasks' reports", RecallQuery{Question: "harbor gallery PR", Task: "harbor-live"}, "done-harbor-live", "done-harbor-gallery"},
		{"since drops older items", RecallQuery{Question: "harbor gallery PR configurator", Since: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}, "done-harbor-live", "done-harbor-gallery"},
		{"until drops newer items", RecallQuery{Question: "harbor gallery PR configurator", Until: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}, "done-harbor-gallery", "done-harbor-live"},
		{"a time filter drops undated items", RecallQuery{Question: "board renders changed", Since: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}, "", "ruling-"},
		{"kinds restrict the search", RecallQuery{Question: "harbor deploys database", Kinds: []string{KindRuling}}, "ruling-", "harbor-database-is-live"},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.query.Limit = 10
			result, err := index.Recall(test.query)
			if err != nil {
				t.Fatal(err)
			}
			ids := recalledIDs(result)
			hasPrefix := func(prefix string) bool {
				return slices.ContainsFunc(ids, func(id string) bool { return strings.HasPrefix(id, prefix) })
			}
			if test.present != "" && !hasPrefix(test.present) || test.absent != "" && hasPrefix(test.absent) {
				t.Fatalf("want %q and not %q, got %v", test.present, test.absent, ids)
			}
		})
	}
}

func TestRecallRejectsQueriesItCannotAnswer(t *testing.T) {
	index := buildFixtureIndex(t)
	for name, query := range map[string]RecallQuery{
		"empty":              {Question: " ", Limit: 5},
		"only stop words":    {Question: "what is the", Limit: 5},
		"zero limit":         {Question: "harbor", Limit: 0},
		"over limit":         {Question: "harbor", Limit: 51},
		"until before since": {Question: "harbor", Limit: 5, Since: indexDate, Until: indexDate.Add(-time.Hour)},
	} {
		if _, err := index.Recall(query); err == nil {
			t.Errorf("%s: recall accepted %+v", name, query)
		}
	}
	result, err := index.Recall(RecallQuery{Question: "unicorn", Limit: 5})
	if err != nil || len(result.Items) != 0 || !strings.Contains(RenderRecall(RecallQuery{Question: "unicorn"}, result, "x"), "No item matches") {
		t.Fatalf("absent match %+v %v", result, err)
	}
}

func TestIndexNoticesEditsArrivalsRenamesAndDeletions(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(t *testing.T, dataDir, stateDir string)
	}{
		{"edit", func(t *testing.T, dataDir, _ string) {
			writeFile(t, filepath.Join(dataDir, "overlord.md"), "## New ruling 2026-11-09\n\nA longer ruling replaces the old text entirely.\n")
		}},
		{"new fact", func(t *testing.T, dataDir, _ string) {
			writeFile(t, filepath.Join(dataDir, "memory", "lantern.md"), "---\nname: lantern\ndescription: lantern\n---\nlantern\n")
		}},
		{"rename", func(t *testing.T, dataDir, _ string) {
			if err := os.Rename(filepath.Join(dataDir, "memory", "northwind-cache-port.md"), filepath.Join(dataDir, "memory", "northwind-port.md")); err != nil {
				t.Fatal(err)
			}
		}},
		{"deletion", func(t *testing.T, dataDir, _ string) {
			if err := os.Remove(filepath.Join(dataDir, "memory-archive.md")); err != nil {
				t.Fatal(err)
			}
		}},
		{"new done report", func(t *testing.T, _, stateDir string) {
			writeFile(t, filepath.Join(stateDir, "still-working.status"), "2026-11-08T10:00:00Z working: building\n2026-11-09T10:00:00Z done: PR https://github.com/acme/harbor/pull/11\n")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataDir, stateDir := writeIndexFixture(t)
			index, err := BuildIndex(dataDir, stateDir, indexDate)
			if err != nil {
				t.Fatal(err)
			}
			if changed, err := index.IsChanged(); err != nil || changed {
				t.Fatalf("unchanged sources read as changed: %v", err)
			}
			test.change(t, dataDir, stateDir)
			if changed, err := index.IsChanged(); err != nil || !changed {
				t.Fatalf("the %s was not noticed: %v", test.name, err)
			}
		})
	}
}

func TestEvaluateCountsHitsAtDepthAndNamesMisses(t *testing.T) {
	index := buildFixtureIndex(t)
	report, err := index.Evaluate([]EvalCase{
		{Question: "is the harbor database hosted", Expect: []string{"harbor-database-is-live"}},
		{Question: "what does merging to main deploy", Expect: []string{"text:deploys the harbor site"}},
		{Question: "which port does the northwind cache use", Project: "northwind", Expect: []string{"northwind-cache-port"}},
		{Question: "unicorn stables", Expect: []string{"harbor-database-is-live"}},
	}, 5, 3*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if report.Questions != 4 || report.HitsAtDepth != 3 || report.HitsAtOne != 3 || len(report.Misses) != 1 || report.Misses[0].Question != "unicorn stables" || report.BuildMillis != 3 || report.MeanRank != 0.75 {
		t.Fatalf("report %+v", report)
	}
	if _, err := index.Evaluate(nil, 5, 0); err == nil {
		t.Fatal("an empty set was scored")
	}
	if _, err := index.Evaluate([]EvalCase{{Question: "harbor"}}, 5, 0); err == nil {
		t.Fatal("a question without an answer was scored")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
