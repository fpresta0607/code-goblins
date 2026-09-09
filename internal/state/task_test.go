package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestRemoveTaskMetaOutlastsAConcurrentReader(t *testing.T) {
	dir := t.TempDir()
	if err := WriteTaskMeta(dir, TaskMeta{ID: "g1", Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "g1.meta")

	// The supervision loop reads every meta on every cycle, and Windows
	// refuses to unlink a file while any handle is open. Hold one open across
	// the removal to prove the retry outlasts it rather than reporting a
	// sharing violation to a caller that would fail the whole cleanup.
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		reader.Close()
		close(closed)
	}()

	if err := RemoveTaskMeta(dir, "g1"); err != nil {
		t.Fatalf("RemoveTaskMeta = %v, want nil despite the open reader", err)
	}
	<-closed
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat after RemoveTaskMeta error = %v, want not-exist", err)
	}
}

func TestRemoveTaskMetaIsIdempotentAndValidatesID(t *testing.T) {
	dir := t.TempDir()
	if err := RemoveTaskMeta(dir, "never-existed"); err != nil {
		t.Fatalf("RemoveTaskMeta on an absent meta = %v, want nil", err)
	}
	if err := RemoveTaskMeta(dir, "../escape"); err == nil {
		t.Fatal("RemoveTaskMeta accepted a traversing task ID, want refusal")
	}
}

// A goblin's GOTMPDIR is the one task path deliberately outside the state
// tree, and cleanup removes it whole - so an id that traverses out of the
// user cache directory has to be refused here rather than at the two call
// sites.
func TestGoTmpDirIsOutsideTheStateTreeAndValidatesID(t *testing.T) {
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("UserCacheDir = %v, want the base GoTmpDir derives from", err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	dir, err := GoTmpDir(stateDir, "g1")
	if err != nil {
		t.Fatalf("GoTmpDir = %v, want a path", err)
	}
	if rel, relErr := filepath.Rel(cache, dir); relErr != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("GoTmpDir = %q, want it under the user cache directory %q", dir, cache)
	}
	if rel, relErr := filepath.Rel(stateDir, dir); relErr == nil && !strings.HasPrefix(rel, "..") {
		t.Errorf("GoTmpDir = %q, want it outside the state tree %q", dir, stateDir)
	}
	// The machine temporary directory is the one place it must not be: a
	// goblin task is live for days and Windows prunes %TEMP% on its own.
	if rel, relErr := filepath.Rel(os.TempDir(), dir); relErr == nil && !strings.HasPrefix(rel, "..") {
		t.Errorf("GoTmpDir = %q, want it outside the machine temporary directory %q", dir, os.TempDir())
	}
	if other, otherErr := GoTmpDir(stateDir, "g2"); otherErr != nil || other == dir {
		t.Errorf("GoTmpDir(g2) = %q, %v, want a directory of its own", other, otherErr)
	}
	if _, err := GoTmpDir(stateDir, "../escape"); err == nil {
		t.Fatal("GoTmpDir accepted a traversing task ID, want refusal")
	}
	if _, err := GoTmpDir("  ", "g1"); err == nil {
		t.Fatal("GoTmpDir accepted an empty state directory, want refusal rather than a machine-global path")
	}
	// CFO_STATE_OVERRIDE is taken verbatim, so two fleets launched from
	// different working directories with the same relative override would
	// hash one string and share one Go temporary directory.
	relative := filepath.Join("state", "fleet")
	if _, err := GoTmpDir(relative, "g1"); err == nil {
		t.Fatal("GoTmpDir accepted a relative state directory, want refusal rather than a directory two fleets could share")
	} else if !strings.Contains(err.Error(), fmt.Sprintf("%q", relative)) {
		t.Errorf("error = %v, want it to name the rejected state directory %q", err, relative)
	}
}

// Two fleet homes on one machine must never share a Go temporary directory:
// the path the fleet segment replaced lived inside a fleet's own state tree,
// so cleaning up a task named g1 in one fleet would otherwise recursively
// delete the live GOTMPDIR of g1 in the other.
func TestGoTmpDirIsScopedToTheFleet(t *testing.T) {
	root := t.TempDir()
	one, err := GoTmpDir(filepath.Join(root, "fleet-a", "state"), "g1")
	if err != nil {
		t.Fatal(err)
	}
	two, err := GoTmpDir(filepath.Join(root, "fleet-b", "state"), "g1")
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatalf("two fleets share the Go temporary directory %q for the same task id", one)
	}

	// The same fleet must resolve to the same directory whatever the spelling,
	// or a switch would relaunch into a directory the spawn never created.
	// Each spelling is concatenated rather than joined: filepath.Join cleans its
	// own arguments, so a joined path would reach GoTmpDir already cleaned and
	// the assertion would compare a hash to itself.
	//
	// Every row the correctness argument leans on is pinned here. An untested
	// premise is how the first version of this assertion shipped vacuous.
	clean := filepath.Join(root, "fleet-a", "state")
	sep := string(filepath.Separator)
	base := filepath.Join(root, "fleet-a")
	for _, spelling := range []struct {
		name        string
		raw         string
		windowsOnly bool
	}{
		{name: "trailing separator", raw: clean + sep},
		{name: "dot segment", raw: base + sep + "." + sep + "state"},
		{name: "dot-dot segment", raw: base + sep + "sub" + sep + ".." + sep + "state"},
		{name: "doubled separator", raw: base + sep + sep + "state"},
		// Forward separators and case folding are Windows path properties. On a
		// case-sensitive filesystem an upper-cased spelling names a different
		// directory, and a backslash is an ordinary filename character, so
		// folding either together there would be the sharing this prevents.
		{name: "forward separators", raw: strings.ReplaceAll(clean, sep, "/"), windowsOnly: true},
		{name: "upper case", raw: strings.ToUpper(clean), windowsOnly: true},
	} {
		t.Run(spelling.name, func(t *testing.T) {
			if spelling.windowsOnly && runtime.GOOS != "windows" {
				t.Skip("this spelling names a different directory on a case-sensitive filesystem")
			}
			if spelling.raw == clean {
				t.Fatalf("spelling %q is identical to the baseline, so it asserts nothing", spelling.raw)
			}
			got, err := GoTmpDir(spelling.raw, "g1")
			if err != nil {
				t.Fatal(err)
			}
			if got != one {
				t.Errorf("GoTmpDir(%q) = %q, want the same directory as %q", spelling.raw, got, one)
			}
		})
	}
}

// An extended-length prefix survives filepath.Clean, so it would hash apart
// from the plain spelling and split one fleet in two.
func TestGoTmpDirRefusesAnExtendedLengthStateDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if _, err := GoTmpDir(`\\?\`+root, "g1"); err == nil {
		t.Fatal("GoTmpDir accepted an extended-length state directory, want refusal")
	}
	if _, err := GoTmpDir(root, "g1"); err != nil {
		t.Fatalf("GoTmpDir refused the plain spelling %q: %v", root, err)
	}
}
