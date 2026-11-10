package memory

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
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
	for _, want := range []string{"lesson: " + ids[0], "source: done report of harbor-saves, https://github.com/acme/harbor/pull/12"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("the fact lacks %q:\n%s", want, content)
		}
	}
	if want := "\n\nCited status lines of harbor-saves:\n\n```\n- 2026-11-09T10:00:00Z working: the cache test needs the port free; fixed\n```\n\n<!--a:2026-11-09-->\n"; !strings.HasSuffix(string(content), want) {
		t.Fatalf("the fact does not end with its cited lines in a code block and the aging marker of its acceptance day:\n%s", content)
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
	if err != nil || strings.Contains(startup, "The harbor cache test needs its port free.") || !strings.Contains(startup, "The release needs approval.") {
		t.Fatalf("a CEO session's startup memory must hold its rulings and leave the lesson out: %q %v", startup, err)
	}
	harbor, err := catalog.Retrieve(Query{Project: "harbor"})
	if err != nil || !strings.Contains(harbor, "The harbor cache test needs its port free.") || strings.Contains(harbor, "legacy=true") {
		t.Fatalf("harbor's memory must carry the lesson as no legacy fact: %q %v", harbor, err)
	}
	added := catalog.Sources[len(catalog.Sources)-1]
	if added.Path != "memory/"+name+".md" || len(added.Entries) != 1 || added.Entries[0].ID != ids[0] || added.Entries[0].Authority != "fact" || added.Entries[0].Location != "cold" || added.Entries[0].Trigger != "The harbor cache test needs its port free." || strings.Join(added.Entries[0].Projects, ",") != "harbor" {
		t.Fatalf("the lesson was cataloged as %+v", added)
	}
}

func TestAcceptLessonNeverTakesTheFactsTierFromACitedLine(t *testing.T) {
	for _, marker := range []string{"<!--P-->", "<!--a:2099-01-01-->", "<!--t:capture-->", "<!--g-->"} {
		t.Run(marker, func(t *testing.T) {
			dataDir, _ := catalogFixture(t)
			stateDir := t.TempDir()
			status := append(slices.Clone(harborStatus), "2026-11-09T12:00:00Z working: the cache stays warm "+marker)
			resolved, err := ResolveLessons([]Lesson{{Text: "The harbor cache test needs its port free.", Cites: []string{"stays warm"}}}, status)
			if err != nil {
				t.Fatal(err)
			}
			ids, err := ProposeLessons(stateDir, harborCandidate(), resolved)
			if err != nil {
				t.Fatal(err)
			}

			_, err = AcceptLesson(dataDir, stateDir, ids[0], Acceptance{Now: referenceDate})

			if err != nil {
				t.Fatal(err)
			}
			catalog, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatalf("the catalog no longer loads: %v", err)
			}
			lesson, err := catalog.Retrieve(Query{ID: ids[0]})
			if err != nil || !strings.Contains(lesson, " authority=fact tier=aging ") || strings.Contains(lesson, "STOW REQUIRED") || !strings.Contains(lesson, "the cache stays warm "+marker) {
				t.Fatalf("a goblin's cited line set the lesson's tier: %q %v", lesson, err)
			}
		})
	}
}

func TestAcceptLessonsAtOnceTakeTurnsSoTheCatalogLoadsWithEachFact(t *testing.T) {
	for name, test := range map[string]struct {
		texts     []string
		accepts   []int
		wantFacts int
	}{
		"two lessons":      {[]string{"The harbor cache test needs its port free.", "Free the port before the gate."}, []int{0, 1}, 2},
		"one lesson twice": {[]string{"The harbor cache test needs its port free."}, []int{0, 0}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir, _ := catalogFixture(t)
			stateDir := t.TempDir()
			ids := proposeHarborLessons(t, stateDir, test.texts...)
			files, err := GovernedFiles(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			names, errs := make([]string, len(test.accepts)), make([]error, len(test.accepts))
			var accepting sync.WaitGroup

			for position, lesson := range test.accepts {
				accepting.Go(func() {
					names[position], errs[position] = AcceptLesson(dataDir, stateDir, ids[lesson], Acceptance{Now: referenceDate})
				})
			}
			accepting.Wait()

			catalog, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatalf("the catalog no longer loads: %v", err)
			}
			filesAfter, err := GovernedFiles(dataDir)
			if err != nil || len(filesAfter)-len(files) != test.wantFacts {
				t.Fatalf("memory files %v then %v, want %d more: %v", files, filesAfter, test.wantFacts, err)
			}
			accepted := 0
			for position, err := range errs {
				if err != nil {
					if !strings.Contains(err.Error(), "was already accepted") {
						t.Fatalf("accept %d: %v", position, err)
					}
					continue
				}
				accepted++
				if !slices.ContainsFunc(catalog.Sources, func(source Source) bool { return source.Path == "memory/"+names[position]+".md" }) {
					t.Fatalf("fact %s is not in the catalog", names[position])
				}
			}
			if accepted != test.wantFacts {
				t.Fatalf("%d accepts succeeded, want %d: %v", accepted, test.wantFacts, errs)
			}
		})
	}
}

func TestAcceptLessonWaitsForTheLessonsLockAnotherProcessHolds(t *testing.T) {
	dataDir, _ := catalogFixture(t)
	stateDir := t.TempDir()
	ids := proposeHarborLessons(t, stateDir, "The harbor cache test needs its port free.")
	before, err := os.ReadFile(filepath.Join(dataDir, "memory", "startup.json"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := GovernedFiles(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	// Another live process, this test's parent, holds the lock as a decision
	// another cfo makes at the same moment does.
	record := filepath.Join(stateDir, lessonLockName)
	if _, err := lock.AcquireNamedOwner(stateDir, lessonLockName, os.Getppid(), ""); err != nil {
		t.Fatal(err)
	}
	var name string
	var acceptErr error
	finished := make(chan struct{})

	go func() {
		defer close(finished)
		name, acceptErr = AcceptLesson(dataDir, stateDir, ids[0], Acceptance{Now: referenceDate})
	}()
	t.Cleanup(func() {
		_ = fsx.Remove(record)
		<-finished
	})

	deadline := time.Now().Add(30 * time.Second)
	for !lock.Shown(filepath.Join(stateDir, lessonLockName+".waiters")) {
		select {
		case <-finished:
			t.Fatalf("the accept went on while another process held the lessons' lock: %q %v", name, acceptErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the accept never waited in the line for the lessons' lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	held, readErr := os.ReadFile(filepath.Join(dataDir, "memory", "startup.json"))
	filesHeld, filesErr := GovernedFiles(dataDir)
	if readErr != nil || string(held) != string(before) || filesErr != nil || !reflect.DeepEqual(filesHeld, files) {
		t.Fatalf("an accept waiting for the lessons' lock wrote memory; files %v then %v: %v %v", files, filesHeld, readErr, filesErr)
	}
	if err := fsx.Remove(record); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Minute):
		t.Fatal("the accept did not finish once the other process let the lessons' lock go")
	}
	if acceptErr != nil {
		t.Fatal(acceptErr)
	}
	catalog, err := Load(dataDir, referenceDate)
	if err != nil {
		t.Fatalf("the catalog no longer loads: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "memory", name+".md")); err != nil || !slices.ContainsFunc(catalog.Sources, func(source Source) bool { return source.Path == "memory/"+name+".md" }) {
		t.Fatalf("fact %s was not written and cataloged: %v", name, err)
	}
}

func TestALessonDecisionSaysWhyItCouldNotTakeTheLessonsLock(t *testing.T) {
	for name, test := range map[string]struct {
		stateDir func(t *testing.T) string
		want     string
		isHeld   bool
	}{
		"a lock another process holds": {func(t *testing.T) string {
			stateDir := t.TempDir()
			if _, err := lock.AcquireNamedOwner(stateDir, lessonLockName, os.Getppid(), ""); err != nil {
				t.Fatal(err)
			}
			return stateDir
		}, "another decision on a lesson is still under way, so nothing was decided", true},
		"a state directory that is a file": {func(t *testing.T) string {
			stateDir := filepath.Join(t.TempDir(), "state")
			writeFile(t, stateDir, "not a folder\n")
			return stateDir
		}, "the lessons' lock could not be taken, so nothing was decided", false},
	} {
		t.Run(name, func(t *testing.T) {
			// Nobody lets the held lock go, so a pause of the machine can
			// only lengthen the wait, never end it with the lock taken.
			previousWait := lessonLockWait
			lessonLockWait = time.Second
			t.Cleanup(func() { lessonLockWait = previousWait })
			stateDir := test.stateDir(t)

			err := RejectLesson(stateDir, "lesson-000000000000", "too narrow", indexDate)

			if err == nil || !strings.Contains(err.Error(), test.want) || errors.Is(err, lock.ErrHeld) != test.isHeld {
				t.Fatalf("got %v, want %q", err, test.want)
			}
		})
	}
}

func TestAcceptLessonLeavesAStartupCatalogAsItFindsItWhenItCannotJoinIt(t *testing.T) {
	const text = "The harbor cache test needs its port free."
	for name, test := range map[string]struct {
		breaks func(t *testing.T, dataDir string, catalog Catalog)
		want   string
	}{
		"a catalog that does not load": {func(t *testing.T, dataDir string, _ Catalog) {
			writeMemoryFile(t, dataDir, "memory/current.md", []byte("Use the remote cache.\n"))
		}, "the memory catalog does not load"},
		"a catalog that already holds the lesson's id": {func(t *testing.T, dataDir string, catalog Catalog) {
			notes := []byte("The quay floods at spring tide. <!--a:2026-10-30-->\n")
			writeMemoryFile(t, dataDir, "memory/quay.md", notes)
			catalog.Sources = append(catalog.Sources, Source{Path: "memory/quay.md", SHA256: Hash(notes), Entries: []Entry{{
				ID: LessonID(harborCandidate().Task, text), End: len(notes), SHA256: Hash(notes), Authority: "fact", Location: "warm",
			}}})
			writeCatalog(t, dataDir, catalog)
		}, "would leave the memory catalog unreadable"},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir, catalog := catalogFixture(t)
			test.breaks(t, dataDir, catalog)
			stateDir := t.TempDir()
			resolved, err := ResolveLessons([]Lesson{{Text: text, Cites: []string{"port free"}}}, harborStatus)
			if err != nil {
				t.Fatal(err)
			}
			ids, err := ProposeLessons(stateDir, harborCandidate(), resolved)
			if err != nil {
				t.Fatal(err)
			}
			before := agedCatalog(t, dataDir)
			files, err := GovernedFiles(dataDir)
			if err != nil {
				t.Fatal(err)
			}

			_, err = AcceptLesson(dataDir, stateDir, ids[0], Acceptance{Now: referenceDate})

			filesAfter, filesErr := GovernedFiles(dataDir)
			if err == nil || !strings.Contains(err.Error(), test.want) || filesErr != nil || !reflect.DeepEqual(filesAfter, files) {
				t.Fatalf("got %v, want %q and the memory files unchanged; files %v then %v", err, test.want, files, filesAfter)
			}
			requireCatalogNeverWritten(t, dataDir, before)
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
