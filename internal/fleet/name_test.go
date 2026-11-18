package fleet

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/goblinname"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// namedFleet is a home whose goblins the board has named: a queued task, one
// under way, one paused, and three finished, two of which held the queued
// task's name before it.
func namedFleet(t *testing.T) home.Home {
	t.Helper()
	h := snapshotHome(t)
	writeBacklog(t, h.Data, "## Queued\n- [ ] queued-task - Tidy the export page (repo: example) blocked-by: memory 8 GB\n- [ ] unnamed-task - Not named yet\n")
	names, err := json.Marshal(map[string]goblinname.Pair{"queued-task": {Name: "Sonny", Title: "Export Elf"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goblinname.QueuedPath(h.State), names, 0o600); err != nil {
		t.Fatal(err)
	}
	live := writeSnapshotMeta(t, h, "live-task", t.TempDir(), "example")
	live.Title, live.GoblinName, live.GoblinTitle = "Speed up the cache", "Wanda", "Cache Keeper"
	if err := state.WriteTaskMeta(h.State, live); err != nil {
		t.Fatal(err)
	}
	paused := writeSnapshotMeta(t, h, "paused-task", t.TempDir(), "example")
	paused.Title, paused.GoblinName, paused.GoblinTitle = "Trim the logs", "Otto", "Log Lumberjack"
	if err := state.WriteTaskMeta(h.State, paused); err != nil {
		t.Fatal(err)
	}
	pause, err := state.NewPauseCondition("memory", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: paused.ID, Generation: paused.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", Pause: &pause}); err != nil {
		t.Fatal(err)
	}
	finished := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, outcome := range []state.Outcome{
		{ID: "oldest-task", Title: "Mend the disk check", GoblinName: "Sonny", GoblinTitle: "Disk Doctor", Project: "example", Phase: "done", Evidence: "reported pull request", At: finished.Add(-48 * time.Hour)},
		{ID: "older-task", Title: "Water the gate", GoblinName: "sonny", GoblinTitle: "Gate Gardener", Project: "example", Phase: "stopped", At: finished},
		{ID: "other-task", Title: "Hunt the bug", GoblinName: "Mabel", GoblinTitle: "Bug Hunter", Project: "example", Phase: "done", Evidence: "reported pull request", At: finished.Add(-24 * time.Hour)},
	} {
		if err := state.WriteOutcome(h.State, outcome); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func TestFleetViewCarriesTheBoardsNameAndTitleForEveryTask(t *testing.T) {
	// Arrange
	h := namedFleet(t)

	// Act
	snapshot, err := BuildSnapshot(t.Context(), h, &snapshotEndpoint{})
	var markdown, data bytes.Buffer
	markdownErr, jsonErr := RenderMarkdown(&markdown, snapshot), RenderJSON(&data, snapshot)

	// Assert
	if err != nil || markdownErr != nil || jsonErr != nil {
		t.Fatalf("snapshot %v, markdown %v, json %v", err, markdownErr, jsonErr)
	}
	for _, want := range []string{
		"| live-task | Speed up the cache |",
		"| paused-task | Trim the logs |",
		"| queued-task | Tidy the export page |",
		"| other-task | Hunt the bug |",
	} {
		if strings.Contains(markdown.String(), want) {
			t.Errorf("fleet view shows a named goblin by its id alone, %q:\n%s", want, markdown.String())
		}
	}
	for _, want := range []string{
		"| Wanda - Cache Keeper (live-task) | Speed up the cache |",
		"| Otto - Log Lumberjack (paused-task) | Trim the logs |",
		"| Sonny - Export Elf (queued-task) | Tidy the export page |",
		"| unnamed-task | Not named yet |",
		"| Mabel - Bug Hunter (other-task) | Hunt the bug |",
	} {
		if !strings.Contains(markdown.String(), want) {
			t.Errorf("fleet view lacks %q:\n%s", want, markdown.String())
		}
	}
	var decoded struct {
		Tasks []struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			GoblinName  string `json:"goblin_name"`
			GoblinTitle string `json:"goblin_title"`
		} `json:"tasks"`
		Backlog struct {
			Queued []struct {
				ID          string `json:"id"`
				Title       string `json:"title"`
				GoblinName  string `json:"goblin_name"`
				GoblinTitle string `json:"goblin_title"`
			} `json:"queued"`
		} `json:"backlog"`
		Completed []struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			GoblinName  string `json:"goblin_name"`
			GoblinTitle string `json:"goblin_title"`
		} `json:"completed"`
	}
	if err := json.Unmarshal(data.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	named := map[string][3]string{}
	for _, task := range decoded.Tasks {
		named[task.ID] = [3]string{task.GoblinName, task.GoblinTitle, task.Title}
	}
	for _, row := range decoded.Backlog.Queued {
		named[row.ID] = [3]string{row.GoblinName, row.GoblinTitle, row.Title}
	}
	for _, outcome := range decoded.Completed {
		named[outcome.ID] = [3]string{outcome.GoblinName, outcome.GoblinTitle, outcome.Title}
	}
	want := map[string][3]string{
		"live-task":    {"Wanda", "Cache Keeper", "Speed up the cache"},
		"paused-task":  {"Otto", "Log Lumberjack", "Trim the logs"},
		"queued-task":  {"Sonny", "Export Elf", "Tidy the export page"},
		"unnamed-task": {"", "", "Not named yet"},
		"oldest-task":  {"Sonny", "Disk Doctor", "Mend the disk check"},
		"older-task":   {"sonny", "Gate Gardener", "Water the gate"},
		"other-task":   {"Mabel", "Bug Hunter", "Hunt the bug"},
	}
	if !reflect.DeepEqual(named, want) {
		t.Errorf("fleet view JSON names %v, want %v:\n%s", named, want, data.String())
	}
}

func TestFindByNameFindsEachTaskTheBoardCallsByThatName(t *testing.T) {
	cases := []struct {
		name string
		want []NameMatch
	}{
		{"wanda", []NameMatch{{ID: "live-task", GoblinName: "Wanda", GoblinTitle: "Cache Keeper", State: "unknown", Project: "example", Title: "Speed up the cache"}}},
		{"OTTO", []NameMatch{{ID: "paused-task", GoblinName: "Otto", GoblinTitle: "Log Lumberjack", State: "paused", Project: "example", Title: "Trim the logs", WaitsOn: "Paused for memory; resumes after two readings with 5 GB of memory and commit free"}}},
		{"Mabel", []NameMatch{{ID: "other-task", GoblinName: "Mabel", GoblinTitle: "Bug Hunter", State: "finished", Project: "example", Title: "Hunt the bug"}}},
		{" Sonny ", []NameMatch{
			{ID: "queued-task", GoblinName: "Sonny", GoblinTitle: "Export Elf", State: "queued", Project: "example", Title: "Tidy the export page", WaitsOn: "memory 8 GB"},
			{ID: "older-task", GoblinName: "sonny", GoblinTitle: "Gate Gardener", State: "stopped", Project: "example", Title: "Water the gate"},
			{ID: "oldest-task", GoblinName: "Sonny", GoblinTitle: "Disk Doctor", State: "finished", Project: "example", Title: "Mend the disk check"},
		}},
		{"Nobody", nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			snapshot, err := BuildSnapshot(t.Context(), namedFleet(t), &snapshotEndpoint{})
			if err != nil {
				t.Fatal(err)
			}

			// Act
			matches := FindByName(snapshot, testCase.name)

			// Assert
			if !reflect.DeepEqual(matches, testCase.want) {
				t.Errorf("FindByName(%q) = %+v, want %+v", testCase.name, matches, testCase.want)
			}
		})
	}
}

func TestFindByNameNeverWritesTheBoardsNames(t *testing.T) {
	// Arrange
	h := namedFleet(t)
	before, err := os.ReadFile(goblinname.QueuedPath(h.State))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := BuildSnapshot(t.Context(), h, &snapshotEndpoint{})
	if err != nil {
		t.Fatal(err)
	}

	// Act
	FindByName(snapshot, "Sonny")

	// Assert
	after, err := os.ReadFile(goblinname.QueuedPath(h.State))
	if err != nil || !bytes.Equal(before, after) {
		t.Errorf("a lookup changed the queued names: before %s, after %s, err %v", before, after, err)
	}
}

func TestRenderNameMatchesPrintsEachMatchOnARow(t *testing.T) {
	// Arrange
	matches := []NameMatch{
		{ID: "queued-task", GoblinName: "Sonny", GoblinTitle: "Export Elf", State: "queued", Project: "example", Title: "Tidy the export page", WaitsOn: "memory 8 GB"},
		{ID: "older-task", GoblinName: "Sonny", State: "stopped", Title: "Water | the gate"},
	}
	var output bytes.Buffer

	// Act
	err := RenderNameMatches(&output, matches)

	// Assert
	want := "| Task | Goblin | State | Project | Title | Waits On |\n| --- | --- | --- | --- | --- | --- |\n" +
		"| queued-task | Sonny - Export Elf | queued | example | Tidy the export page | memory 8 GB |\n" +
		"| older-task | Sonny | stopped | - | Water \\| the gate | - |\n"
	if err != nil || output.String() != want {
		t.Errorf("RenderNameMatches = %q, %v, want %q", output.String(), err, want)
	}
}
