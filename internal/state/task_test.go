package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidTaskID(t *testing.T) {
	valid64 := strings.Repeat("a", 64)
	invalid65 := strings.Repeat("a", 65)
	tests := []struct {
		id   string
		want bool
	}{
		{id: "g1", want: true},
		{id: "A_1-2.3", want: true},
		{id: valid64, want: true},
		{id: "", want: false},
		{id: ".hidden", want: false},
		{id: "trailing.", want: false},
		{id: "has space", want: false},
		{id: "slash/name", want: false},
		{id: "goblin-\u00e9", want: false},
		{id: invalid65, want: false},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			err := ValidTaskID(test.id)
			if (err == nil) != test.want {
				t.Fatalf("ValidTaskID(%q) error = %v, want valid = %t", test.id, err, test.want)
			}
		})
	}
}

func TestReadTaskMetaLastValueWinsAndAcceptsCRLF(t *testing.T) {
	dir := t.TempDir()
	content := "window=old:pane\r\nworktree=C:\\work\\g1\r\nkind=scout\r\nwindow=fleet:pane-2\r\nbackend=herdr\r\nherdr_session=fleet\r\nherdr_workspace_id=ws\r\nherdr_tab_id=tab\r\nherdr_pane_id=pane-2\r\n"
	if err := os.WriteFile(filepath.Join(dir, "g1.meta"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	meta, err := ReadTaskMeta(dir, "g1")
	if err != nil {
		t.Fatalf("ReadTaskMeta: %v", err)
	}
	if meta.ID != "g1" || meta.Window != "fleet:pane-2" || meta.Kind != "scout" {
		t.Fatalf("meta = %+v, want ID, last window, and CRLF fields", meta)
	}
	if meta.HerdrSession != "fleet" || meta.HerdrWorkspaceID != "ws" || meta.HerdrTabID != "tab" || meta.HerdrPaneID != "pane-2" {
		t.Fatalf("Herdr metadata = %+v, want all endpoint fields", meta)
	}
}

func TestReadTaskMetaDefaultsKindAndLeavesOptionalFieldsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "g1.meta"), []byte("worktree=C:\\work\\g1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := ReadTaskMeta(dir, "g1")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Kind != "ship" {
		t.Errorf("Kind = %q, want ship", meta.Kind)
	}
	if meta.Model != "" || meta.Effort != "" || meta.HerdrPaneID != "" {
		t.Errorf("optional fields = %+v, want empty", meta)
	}
}

func TestWriteTaskMetaIsDeterministicAndRoundTripsHerdrFields(t *testing.T) {
	dir := t.TempDir()
	meta := TaskMeta{
		ID:               "g1",
		Window:           "fleet:pane-1",
		Worktree:         `C:\work\g1`,
		Harness:          "codex",
		Kind:             "ship",
		Backend:          "herdr",
		HerdrSession:     "fleet",
		HerdrWorkspaceID: "ws-1",
		HerdrTabID:       "tab-1",
		HerdrPaneID:      "pane-1",
	}
	if err := WriteTaskMeta(dir, meta); err != nil {
		t.Fatalf("WriteTaskMeta: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "g1.meta"))
	if err != nil {
		t.Fatal(err)
	}
	want := "backend=herdr\nharness=codex\nherdr_pane_id=pane-1\nherdr_session=fleet\nherdr_tab_id=tab-1\nherdr_workspace_id=ws-1\nkind=ship\nwindow=fleet:pane-1\nworktree=C:\\work\\g1\n"
	if string(data) != want {
		t.Errorf("metadata = %q, want %q", data, want)
	}

	got, err := ReadTaskMeta(dir, "g1")
	if err != nil {
		t.Fatal(err)
	}
	if got != meta {
		t.Errorf("round trip = %+v, want %+v", got, meta)
	}

	if err := WriteTaskMeta(dir, TaskMeta{ID: ".bad"}); err == nil {
		t.Error("WriteTaskMeta accepted an invalid task ID")
	}
	if _, err := ReadTaskMeta(dir, "missing"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadTaskMeta missing error = %v, want ErrNotExist", err)
	}
}

func TestWriteTaskMetaOmitsShipOnlyFieldsForScout(t *testing.T) {
	dir := t.TempDir()
	meta := TaskMeta{
		ID:               "scout-1",
		Kind:             "scout",
		Mode:             "no-mistakes",
		Yolo:             "on",
		Backend:          "herdr",
		HerdrSession:     "fleet",
		HerdrWorkspaceID: "ws-1",
		HerdrTabID:       "tab-1",
		HerdrPaneID:      "pane-1",
	}
	if err := WriteTaskMeta(dir, meta); err != nil {
		t.Fatalf("WriteTaskMeta: %v", err)
	}
	values, err := ReadMeta(filepath.Join(dir, "scout-1.meta"))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := values["mode"]; exists {
		t.Errorf("scout metadata includes ship-only mode=%q", values["mode"])
	}
	if _, exists := values["yolo"]; exists {
		t.Errorf("scout metadata includes ship-only yolo=%q", values["yolo"])
	}
}

func TestWriteTaskMetaRejectsControlCharactersInEveryValue(t *testing.T) {
	dir := t.TempDir()
	base := TaskMeta{
		ID:               "safe",
		Window:           "fleet:pane-1",
		EndpointTaskID:   "safe",
		Worktree:         `C:\work\safe`,
		Project:          `C:\project`,
		Harness:          "claude",
		Kind:             "ship",
		Mode:             "no-mistakes",
		Yolo:             "on",
		TaskTmp:          `C:\state\tasktmp\safe`,
		Model:            "model-a",
		Effort:           "high",
		SpawnGen:         "s1",
		Backend:          "herdr",
		HerdrSession:     "fleet",
		HerdrWorkspaceID: "workspace-1",
		HerdrTabID:       "tab-1",
		HerdrPaneID:      "pane-1",
	}
	if err := WriteTaskMeta(dir, base); err != nil {
		t.Fatalf("WriteTaskMeta ordinary Windows paths: %v", err)
	}

	fields := []struct {
		name string
		set  func(*TaskMeta)
	}{
		{"id", func(meta *TaskMeta) { meta.ID = "bad\nother" }},
		{"window", func(meta *TaskMeta) { meta.Window = "bad\nother" }},
		{"endpoint_task_id", func(meta *TaskMeta) { meta.EndpointTaskID = "bad\nother" }},
		{"worktree", func(meta *TaskMeta) { meta.Worktree = "bad\nother" }},
		{"project", func(meta *TaskMeta) { meta.Project = "bad\nother" }},
		{"harness", func(meta *TaskMeta) { meta.Harness = "bad\nother" }},
		{"kind", func(meta *TaskMeta) { meta.Kind = "bad\nother" }},
		{"mode", func(meta *TaskMeta) { meta.Mode = "bad\nother" }},
		{"yolo", func(meta *TaskMeta) { meta.Yolo = "bad\nother" }},
		{"tasktmp", func(meta *TaskMeta) { meta.TaskTmp = "bad\nother" }},
		{"model", func(meta *TaskMeta) { meta.Model = "bad\rother" }},
		{"effort", func(meta *TaskMeta) { meta.Effort = "bad\x00other" }},
		{"spawn_gen", func(meta *TaskMeta) { meta.SpawnGen = "bad\nother" }},
		{"backend", func(meta *TaskMeta) { meta.Backend = "bad\nother" }},
		{"herdr_session", func(meta *TaskMeta) { meta.HerdrSession = "bad\nother" }},
		{"herdr_workspace_id", func(meta *TaskMeta) { meta.HerdrWorkspaceID = "bad\nother" }},
		{"herdr_tab_id", func(meta *TaskMeta) { meta.HerdrTabID = "bad\nother" }},
		{"herdr_pane_id", func(meta *TaskMeta) { meta.HerdrPaneID = "bad\nother" }},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			meta := base
			meta.ID = "reject-" + field.name
			field.set(&meta)

			err := WriteTaskMeta(dir, meta)
			if err == nil || (field.name != "id" && !strings.Contains(err.Error(), "control character")) {
				t.Fatalf("WriteTaskMeta(%s) error = %v, want control character refusal", field.name, err)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "reject-"+field.name+".meta")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("WriteTaskMeta(%s) wrote metadata: stat error = %v", field.name, statErr)
			}
		})
	}

	values, err := ReadMeta(filepath.Join(dir, "safe.meta"))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := values["other"]; exists {
		t.Fatalf("ordinary metadata readback includes forged key: %v", values)
	}
}
