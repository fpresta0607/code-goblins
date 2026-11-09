package memory

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

var harborStatus = []string{
	"2026-11-09T09:00:00Z working: gate run 01ABC failed on the cache test",
	"2026-11-09T10:00:00Z working: the cache test needs the port free; fixed",
	"2026-11-09T11:00:00Z working: gate run 01ABD passed",
}

func harborCandidate() LessonCandidate {
	return LessonCandidate{Task: "harbor-saves", Goblin: "Ivy", Project: "harbor", PR: "https://github.com/acme/harbor/pull/12", ProposedAt: indexDate}
}

func TestResolveLessonsResolvesEachCiteToTheTasksOwnStatusLine(t *testing.T) {
	lessons := []Lesson{
		{Text: "  The harbor cache test needs its port free.  ", Cites: []string{"2026-11-09T09:00:00Z", "PORT FREE", "2026-11-09T09:00:00Z"}},
		{Text: "A gate run passes once the port is free.", Cites: []string{"gate run"}},
	}

	resolved, err := ResolveLessons(lessons, harborStatus)

	if err != nil {
		t.Fatal(err)
	}
	want := []ProposedLesson{
		{Text: "The harbor cache test needs its port free.", Cites: []CitedLine{{"2026-11-09T09:00:00Z", "working: gate run 01ABC failed on the cache test"}, {"2026-11-09T10:00:00Z", "working: the cache test needs the port free; fixed"}}},
		{Text: "A gate run passes once the port is free.", Cites: []CitedLine{{"2026-11-09T11:00:00Z", "working: gate run 01ABD passed"}}},
	}
	if !reflect.DeepEqual(resolved, want) {
		t.Fatalf("resolved %+v, want %+v", resolved, want)
	}
}

func TestResolveLessonsRefusesWhatADoneReportCannotCarry(t *testing.T) {
	cited := []string{"gate run"}
	for name, test := range map[string]struct {
		lessons []Lesson
		want    string
	}{
		"four lessons":              {[]Lesson{{"a", cited}, {"b", cited}, {"c", cited}, {"d", cited}}, "at most 3 lessons"},
		"an empty lesson":           {[]Lesson{{" ", cited}}, "lesson 1 is empty"},
		"a lesson of two lines":     {[]Lesson{{"one\ntwo", cited}}, "lesson 1 spans lines or holds a control character"},
		"a terminal escape":         {[]Lesson{{"a", cited}, {"b\x1b[2Jc", cited}}, "lesson 2 spans lines or holds a control character"},
		"a lesson with no cite":     {[]Lesson{{"a", nil}}, "lesson 1 cites no status line"},
		"an empty cite":             {[]Lesson{{"a", []string{" "}}}, "a cite is empty"},
		"a stamp of no line":        {[]Lesson{{"a", []string{"2026-11-09T12:00:00Z"}}}, "no status line of this task is stamped 2026-11-09T12:00:00Z"},
		"words no line holds":       {[]Lesson{{"a", cited}, {"b", []string{"lighthouse"}}}, `lesson 2: no status line of this task holds "lighthouse"`},
		"a task with no status yet": {[]Lesson{{"a", cited}}, `no status line of this task holds "gate run"`},
	} {
		t.Run(name, func(t *testing.T) {
			status := harborStatus
			if name == "a task with no status yet" {
				status = nil
			}

			_, err := ResolveLessons(test.lessons, status)

			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %q", err, test.want)
			}
		})
	}
}

func proposeHarborLessons(t *testing.T, stateDir string, texts ...string) []string {
	t.Helper()
	var lessons []Lesson
	for _, text := range texts {
		lessons = append(lessons, Lesson{Text: text, Cites: []string{"port free"}})
	}
	resolved, err := ResolveLessons(lessons, harborStatus)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := ProposeLessons(stateDir, harborCandidate(), resolved)
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestProposeLessonsRecordsEachCandidateOnceAndKeepsItsDecision(t *testing.T) {
	_, stateDir := writeIndexFixture(t)
	ids := proposeHarborLessons(t, stateDir, "The harbor cache test needs its port free.", "Free the port before the gate.")
	if err := RejectLesson(stateDir, ids[1], "already in the brief template", indexDate); err != nil {
		t.Fatal(err)
	}

	again := proposeHarborLessons(t, stateDir, "The harbor cache test needs its port free.", "Free the port before the gate.")
	pending, problems, err := PendingLessons(stateDir)

	if err != nil || len(problems) != 0 || strings.Join(again, " ") != strings.Join(ids, " ") {
		t.Fatalf("ids %v then %v: %v %v", ids, again, problems, err)
	}
	if len(pending) != 1 || pending[0].ID != ids[0] || pending[0].Task != "harbor-saves" || pending[0].Project != "harbor" || len(pending[0].Cites) != 1 || pending[0].Cites[0].Stamp != "2026-11-09T10:00:00Z" {
		t.Fatalf("pending %+v", pending)
	}
	rejected, err := ReadLesson(stateDir, ids[1])
	if err != nil || rejected.Decision != LessonRejected || rejected.Reason != "already in the brief template" {
		t.Fatalf("a proposal again reset a decision: %+v %v", rejected, err)
	}
}

func TestPendingLessonsNamesACandidateItCannotRead(t *testing.T) {
	_, stateDir := writeIndexFixture(t)
	proposeHarborLessons(t, stateDir, "The harbor cache test needs its port free.")
	writeFile(t, filepath.Join(stateDir, "lessons", "lesson-000000000000.json"), "{not json")

	pending, problems, err := PendingLessons(stateDir)

	if err != nil || len(pending) != 1 || len(problems) != 1 || !strings.Contains(problems[0], "lesson-000000000000") {
		t.Fatalf("pending %+v, problems %v: %v", pending, problems, err)
	}
}

func TestAcceptLessonWritesANewScopedFactAndLeavesWhatItSupersedes(t *testing.T) {
	dataDir, stateDir := writeIndexFixture(t)
	oldNote := "---\nname: harbor-cache-test-is-flaky\ndescription: the harbor cache test is flaky\nmetadata:\n  type: project\n  projects: [harbor]\n---\n\nthe harbor cache test is flaky.\n"
	writeFile(t, filepath.Join(dataDir, "memory", "harbor-cache-test-is-flaky.md"), oldNote)
	writeFile(t, filepath.Join(dataDir, "memory", "the-harbor-cache-test-needs-its-port-free.md"), "taken by an older note\n")
	ids := proposeHarborLessons(t, stateDir, "The harbor cache test needs its port free.")
	accepted := indexDate.Add(time.Hour)

	name, err := AcceptLesson(dataDir, stateDir, ids[0], Acceptance{Supersedes: []string{"harbor-cache-test-is-flaky"}, Now: accepted})

	if err != nil || name != "the-harbor-cache-test-needs-its-port-free-2" {
		t.Fatalf("accepted as %q: %v", name, err)
	}
	if content, err := os.ReadFile(filepath.Join(dataDir, "memory", "harbor-cache-test-is-flaky.md")); err != nil || string(content) != oldNote {
		t.Fatalf("the superseded fact was rewritten: %q %v", content, err)
	}
	index, err := BuildIndex(dataDir, stateDir, accepted)
	if err != nil {
		t.Fatal(err)
	}
	result, err := index.Recall(RecallQuery{Question: "harbor cache test", Limit: 5})
	if err != nil || len(result.Items) == 0 {
		t.Fatalf("%+v %v", result, err)
	}
	lesson := result.Items[0]
	if lesson.ID != name || lesson.Title != "The harbor cache test needs its port free." || strings.Join(lesson.Projects, ",") != "harbor" || strings.Join(lesson.Supersedes, ",") != "harbor-cache-test-is-flaky" || lesson.IsIncident || !lesson.Date.Equal(accepted) {
		t.Fatalf("the accepted lesson indexed as %+v", lesson.Item)
	}
	for _, item := range result.Items {
		if item.ID == "harbor-cache-test-is-flaky" {
			t.Fatalf("the superseded fact is still recalled: %+v", result.Items)
		}
	}
	content, err := os.ReadFile(lesson.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lesson: " + ids[0], "source: done report of harbor-saves, https://github.com/acme/harbor/pull/12", "Cited status lines of harbor-saves:\n- 2026-11-09T10:00:00Z working: the cache test needs the port free; fixed\n"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("the fact lacks %q:\n%s", want, content)
		}
	}
	decided, err := ReadLesson(stateDir, ids[0])
	if err != nil || decided.Decision != LessonAccepted || decided.Fact != name || !decided.DecidedAt.Equal(accepted) {
		t.Fatalf("decision %+v: %v", decided, err)
	}
	if _, err := AcceptLesson(dataDir, stateDir, ids[0], Acceptance{Now: accepted}); err == nil || !strings.Contains(err.Error(), "was already accepted on 2026-11-09") {
		t.Fatalf("a lesson was accepted twice: %v", err)
	}
}

func TestAcceptLessonAsAnIncidentMarksItToFade(t *testing.T) {
	dataDir, stateDir := writeIndexFixture(t)
	ids := proposeHarborLessons(t, stateDir, "The harbor cache test needs its port free.")

	name, err := AcceptLesson(dataDir, stateDir, ids[0], Acceptance{IsIncident: true, Now: indexDate})

	if err != nil {
		t.Fatal(err)
	}
	index, err := BuildIndex(dataDir, stateDir, indexDate)
	if err != nil {
		t.Fatal(err)
	}
	result, err := index.Recall(RecallQuery{Question: "harbor cache port", Limit: 1})
	if err != nil || len(result.Items) != 1 || result.Items[0].ID != name || !result.Items[0].IsIncident {
		t.Fatalf("%+v %v", result.Items, err)
	}
}

func TestAcceptLessonRefusesWhatItCannotTake(t *testing.T) {
	for name, test := range map[string]struct {
		id         string
		supersedes []string
		want       string
	}{
		"a ruling":          {"", []string{"ruling-" + strings.Repeat("0", 12)}, "names no memory fact; a lesson supersedes facts, never a ruling"},
		"an unknown fact":   {"", []string{"harbor-nothing"}, "--supersedes harbor-nothing names no memory fact"},
		"an unknown lesson": {"lesson-000000000000", nil, "no lesson lesson-000000000000 was proposed"},
		"a path":            {`lesson-..\x`, nil, "is no lesson id"},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir, stateDir := writeIndexFixture(t)
			ids := proposeHarborLessons(t, stateDir, "The harbor cache test needs its port free.")
			id := test.id
			if id == "" {
				id = ids[0]
			}

			_, err := AcceptLesson(dataDir, stateDir, id, Acceptance{Supersedes: test.supersedes, Now: indexDate})

			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %q", err, test.want)
			}
			if _, err := os.Stat(filepath.Join(dataDir, "memory", "the-harbor-cache-test-needs-its-port-free.md")); !os.IsNotExist(err) {
				t.Fatalf("a refused acceptance wrote a fact: %v", err)
			}
			if pending, _, _ := PendingLessons(stateDir); len(pending) != 1 {
				t.Fatalf("a refused acceptance decided the lesson: %+v", pending)
			}
		})
	}
}

func TestAcceptLessonCatalogsItsFactSoTheStartupCatalogStillLoads(t *testing.T) {
	dataDir, _ := catalogFixture(t)
	stateDir := t.TempDir()
	ids := proposeHarborLessons(t, stateDir, "The harbor cache test needs its port free.")

	name, err := AcceptLesson(dataDir, stateDir, ids[0], Acceptance{Now: referenceDate})

	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(dataDir, referenceDate)
	if err != nil {
		t.Fatalf("the catalog no longer loads: %v", err)
	}
	startup, err := catalog.Retrieve(Query{})
	if err != nil || !strings.Contains(startup, "The harbor cache test needs its port free.") || !strings.Contains(startup, "The release needs approval.") {
		t.Fatalf("startup memory %q: %v", startup, err)
	}
	added := catalog.Sources[len(catalog.Sources)-1]
	if added.Path != "memory/"+name+".md" || len(added.Entries) != 1 || added.Entries[0].ID != ids[0] || added.Entries[0].Authority != "fact" || added.Entries[0].Location != "warm" || strings.Join(added.Entries[0].Projects, ",") != "harbor" {
		t.Fatalf("the lesson was cataloged as %+v", added)
	}
}

func TestAcceptLessonLeavesAStartupCatalogAsItFindsItWhenItCannotJoinIt(t *testing.T) {
	status := append(slices.Clone(harborStatus), "2026-11-09T12:00:00Z working: the cache stays warm <!--a:2099-01-01-->")
	for name, test := range map[string]struct {
		cite   string
		breaks func(t *testing.T, dataDir string)
		want   string
	}{
		"a catalog that does not load": {"port free", func(t *testing.T, dataDir string) {
			writeMemoryFile(t, dataDir, "memory/current.md", []byte("Use the remote cache.\n"))
		}, "the memory catalog does not load"},
		"a fact ending in a marker the catalog refuses": {"stays warm", func(*testing.T, string) {}, "would leave the memory catalog unreadable"},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir, _ := catalogFixture(t)
			test.breaks(t, dataDir)
			stateDir := t.TempDir()
			resolved, err := ResolveLessons([]Lesson{{Text: "The harbor cache test needs its port free.", Cites: []string{test.cite}}}, status)
			if err != nil {
				t.Fatal(err)
			}
			ids, err := ProposeLessons(stateDir, harborCandidate(), resolved)
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(dataDir, "memory", "startup.json"))
			if err != nil {
				t.Fatal(err)
			}
			files, err := GovernedFiles(dataDir)
			if err != nil {
				t.Fatal(err)
			}

			_, err = AcceptLesson(dataDir, stateDir, ids[0], Acceptance{Now: referenceDate})

			after, readErr := os.ReadFile(filepath.Join(dataDir, "memory", "startup.json"))
			filesAfter, filesErr := GovernedFiles(dataDir)
			if err == nil || !strings.Contains(err.Error(), test.want) || readErr != nil || string(after) != string(before) || filesErr != nil || !reflect.DeepEqual(filesAfter, files) {
				t.Fatalf("got %v, want %q, the catalog and memory files unchanged; files %v then %v", err, test.want, files, filesAfter)
			}
			if pending, _, _ := PendingLessons(stateDir); len(pending) != 1 {
				t.Fatalf("a refused acceptance decided the lesson: %+v", pending)
			}
		})
	}
}

func TestLessonSlugNamesTheFactByItsFirstWordsAndNeverEndsACutOnAStopword(t *testing.T) {
	for _, test := range []struct{ text, want string }{
		{"The harbor cache test needs its port free.", "the-harbor-cache-test-needs-its-port-free"},
		{"Run the repository root guard tests before the first push of a branch.", "run-the-repository-root-guard-tests-before-the-first-push"},
		{"Free the port, then run it", "free-the-port-then-run-it"},
		{"!!! ???", "lesson"},
	} {
		t.Run(test.want, func(t *testing.T) {
			if got := lessonSlug(test.text); got != test.want {
				t.Fatalf("lessonSlug(%q) = %q, want %q", test.text, got, test.want)
			}
		})
	}
}

func TestRejectLessonNeedsAReasonAndWritesNoMemory(t *testing.T) {
	dataDir, stateDir := writeIndexFixture(t)
	ids := proposeHarborLessons(t, stateDir, "The harbor cache test needs its port free.")
	before, err := os.ReadDir(filepath.Join(dataDir, "memory"))
	if err != nil {
		t.Fatal(err)
	}

	unreasoned := RejectLesson(stateDir, ids[0], " ", indexDate)
	err = RejectLesson(stateDir, ids[0], "too narrow", indexDate)

	after, readErr := os.ReadDir(filepath.Join(dataDir, "memory"))
	if unreasoned == nil || err != nil || readErr != nil || len(after) != len(before) {
		t.Fatalf("rejections %v then %v; memory files %d then %d", unreasoned, err, len(before), len(after))
	}
	if pending, _, _ := PendingLessons(stateDir); len(pending) != 0 {
		t.Fatalf("a rejected lesson still waits: %+v", pending)
	}
}

func TestRecallFadesIncidentsByAgeAndNeverRules(t *testing.T) {
	dataDir, stateDir := writeIndexFixture(t)
	fact := func(name, extra string, modified time.Time) {
		writeFile(t, filepath.Join(dataDir, "memory", name+".md"), "---\nname: "+name+"\ndescription: lighthouse lamp keeper\nmetadata:\n  type: project\n  modified: "+modified.Format(time.RFC3339)+"\n"+extra+"---\n\nlighthouse lamp keeper\n")
	}
	fact("lamp-rule-old", "", indexDate.Add(-90*24*time.Hour))
	fact("lamp-incident-new", "  incident: true\n", indexDate.Add(-24*time.Hour))
	fact("lamp-incident-old", "  incident: true\n", indexDate.Add(-60*24*time.Hour))
	index, err := BuildIndex(dataDir, stateDir, indexDate)
	if err != nil {
		t.Fatal(err)
	}

	result, err := index.Recall(RecallQuery{Question: "lighthouse lamp keeper", Limit: 3})

	if err != nil || len(result.Items) != 3 {
		t.Fatalf("%+v %v", result.Items, err)
	}
	ranked := []string{result.Items[0].ID, result.Items[1].ID, result.Items[2].ID}
	if strings.Join(ranked, " ") != "lamp-rule-old lamp-incident-new lamp-incident-old" {
		t.Fatalf("ranked %v", ranked)
	}
	if ratio := result.Items[2].Score / result.Items[0].Score; ratio < 0.249 || ratio > 0.251 {
		t.Fatalf("an incident two half-lives old scored %.3f of a rule's equal match, want 0.25", ratio)
	}
}
