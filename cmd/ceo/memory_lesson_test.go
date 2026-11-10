package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/memory"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// lessonHome is a home whose task harbor-saves, Ivy's, has reported three
// status lines in the harbor repository.
func lessonHome(t *testing.T) (home.Home, commandRuntime) {
	t.Helper()
	h := testHome(t)
	for name, content := range map[string]string{
		"harbor-saves.meta":   "project=C:\\dev\\harbor\ntitle=Harbor saves\ngoblin_name=Ivy\n",
		"harbor-saves.status": "2026-11-09T09:00:00Z working: gate run 01ABC failed on the cache test\n2026-11-09T10:00:00Z working: the cache test needs the port free; fixed\n2026-11-09T11:00:00Z working: gate run 01ABD passed\n",
	} {
		if err := os.MkdirAll(h.State, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.State, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CEO_HOME", h.Root)
	t.Setenv("CEO_STATE_OVERRIDE", "")
	t.Setenv(harness.RoleVariable, "")
	t.Setenv(gateAgentVariable, "")
	runtime := testCommandRuntimeForHome(h)
	runtime.github = proofRunner{stdout: strings.Repeat("a", 40) + "\n"}
	return h, runtime
}

func drainText(t *testing.T, h home.Home) string {
	t.Helper()
	records, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := renderDrain(h.State, records, wake.Episode{}, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestALessonOnADoneReachesTheDrainAndOnceAcceptedTheRepositorysNextPack(t *testing.T) {
	h, runtime := lessonHome(t)
	var stdout, stderr bytes.Buffer

	exit := runNotify([]string{"harbor-saves", "--done", "--pr", "https://github.com/acme/harbor/pull/12",
		"--lesson", "The harbor cache test needs its port free.", "--cites", "2026-11-09T09:00:00Z", "--cites", "port free",
		"--lesson", "Free the port before the gate.", "--cites", "01ABD"}, &stdout, &stderr, runtime)

	if exit != 0 || !strings.Contains(stdout.String(), "Your lessons wait for the CEO's word at the drain as lesson-") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	status, err := os.ReadFile(filepath.Join(h.State, "harbor-saves.status"))
	if err != nil || !strings.HasSuffix(strings.TrimSpace(string(status)), "done: PR https://github.com/acme/harbor/pull/12") {
		t.Fatalf("the done line carries more than its report: %q %v", status, err)
	}
	pending, _, err := memory.PendingLessons(h.State)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending %+v: %v", pending, err)
	}
	drained := drainText(t, h)
	for _, want := range []string{
		"LESSONS WAITING FOR YOUR WORD: 2; accept with ceo memory lesson accept <id>",
		"  " + pending[0].ID + "  Ivy (harbor-saves) in harbor: ",
		"The harbor cache test needs its port free. [cites 2 status lines]",
		"Free the port before the gate. [cites 1 status lines]",
	} {
		if !strings.Contains(drained, want) {
			t.Fatalf("the drain lacks %q:\n%s", want, drained)
		}
	}
	ids := map[string]string{}
	for _, lesson := range pending {
		ids[lesson.Text] = lesson.ID
	}

	stdout.Reset()
	accepted := runMemory([]string{"lesson", "accept", ids["The harbor cache test needs its port free."]}, &stdout, &stderr)
	acceptOut := stdout.String()
	rejected := runMemory([]string{"lesson", "reject", ids["Free the port before the gate."], "--reason", "the gate frees it now"}, &stdout, &stderr)

	if accepted != 0 || rejected != 0 || !strings.Contains(acceptOut, filepath.Join(h.Data, "memory", "the-harbor-cache-test-needs-its-port-free.md")) {
		t.Fatalf("accept %d, reject %d: %q %q", accepted, rejected, stdout.String(), stderr.String())
	}
	if drained := drainText(t, h); strings.Contains(drained, "LESSONS WAITING") {
		t.Fatalf("decided lessons still wait at the drain:\n%s", drained)
	}
	index, err := memory.BuildIndex(h.Data, h.State, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pack, err := index.BuildPack(memory.PackInput{Task: "harbor-next", Project: "harbor", Title: "Harbor gallery captions", Brief: "## Task\n\ncaptions\n", Budget: memory.DefaultPackBudget, Now: time.Now()})
	if err != nil || !strings.Contains(pack.Text, "the-harbor-cache-test-needs-its-port-free (") || strings.Contains(pack.Text, "Free the port before the gate.") {
		t.Fatalf("the next harbor pack does not carry exactly the accepted lesson: %v\n%s", err, pack.Text)
	}
}

func TestNotifyRefusesALessonItCannotRecordAndRecordsNothing(t *testing.T) {
	for name, test := range map[string]struct {
		args []string
		want string
	}{
		"a lesson on a working report": {[]string{"--working", "building", "--lesson", "a", "--cites", "port free"}, "--lesson goes with --done"},
		"a cite before any lesson":     {[]string{"--done", "--pr", "https://github.com/acme/harbor/pull/12", "--cites", "port free", "--lesson", "a"}, "--cites follows the --lesson it supports"},
		"a cite no line holds":         {[]string{"--done", "--pr", "https://github.com/acme/harbor/pull/12", "--lesson", "a", "--cites", "lighthouse"}, `no status line of this task holds "lighthouse"`},
		"a lesson with no cite":        {[]string{"--done", "--pr", "https://github.com/acme/harbor/pull/12", "--lesson", "a"}, "lesson 1 cites no status line"},
		"four lessons": {[]string{"--done", "--pr", "https://github.com/acme/harbor/pull/12",
			"--lesson", "a", "--cites", "01ABC", "--lesson", "b", "--cites", "01ABC", "--lesson", "c", "--cites", "01ABC", "--lesson", "d", "--cites", "01ABC"}, "at most 3 lessons"},
	} {
		t.Run(name, func(t *testing.T) {
			h, runtime := lessonHome(t)
			before, err := os.ReadFile(filepath.Join(h.State, "harbor-saves.status"))
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer

			exit := runNotify(append([]string{"harbor-saves"}, test.args...), &stdout, &stderr, runtime)

			after, err := os.ReadFile(filepath.Join(h.State, "harbor-saves.status"))
			if exit != 2 || !strings.Contains(stderr.String(), test.want) || err != nil || string(after) != string(before) {
				t.Fatalf("exit=%d stderr=%q, want %q and no status line", exit, stderr.String(), test.want)
			}
			if _, err := os.Stat(filepath.Join(h.State, "lessons")); !os.IsNotExist(err) {
				t.Fatalf("a refused report proposed lessons: %v", err)
			}
		})
	}
}

func TestMemoryLessonRefusesWhatItCannotDecide(t *testing.T) {
	for name, test := range map[string]struct {
		env  map[string]string
		args []string
		exit int
		want string
	}{
		"no action":                  {nil, []string{"lesson"}, 2, "accept <id>"},
		"an unknown action":          {nil, []string{"lesson", "forget", "lesson-000000000000"}, 2, "accept <id>"},
		"an unknown lesson":          {nil, []string{"lesson", "accept", "lesson-000000000000"}, 1, "no lesson lesson-000000000000 was proposed"},
		"a flag of the other action": {nil, []string{"lesson", "reject", "lesson-000000000000", "--incident"}, 2, "flag provided but not defined: -incident"},
		"a goblin":                   {map[string]string{harness.RoleVariable: harness.RoleGoblin}, []string{"lesson", "accept", "lesson-000000000000"}, 2, "never a goblin's or a gate agent's"},
		"a gate agent":               {map[string]string{gateAgentVariable: "1"}, []string{"lesson", "reject", "lesson-000000000000", "--reason", "too narrow"}, 2, "never a goblin's or a gate agent's"},
	} {
		t.Run(name, func(t *testing.T) {
			lessonHome(t)
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			var stdout, stderr bytes.Buffer

			exit := runMemory(test.args, &stdout, &stderr)

			if exit != test.exit || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("exit=%d stderr=%q, want %d and %q", exit, stderr.String(), test.exit, test.want)
			}
		})
	}
}
