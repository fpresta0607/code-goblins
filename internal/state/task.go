package state

import (
	"fmt"
	"path/filepath"
	"unicode"
)

// TaskMeta is the typed view of an upstream-compatible flat task metadata
// record. ID belongs to the filename, while every other field keeps its
// upstream key name in state/<id>.meta.
type TaskMeta struct {
	ID               string
	Window           string
	EndpointTaskID   string
	Worktree         string
	Project          string
	Harness          string
	Kind             string
	Mode             string
	Yolo             string
	TaskTmp          string
	Brief            string
	Model            string
	Effort           string
	SpawnGen         string
	Backend          string
	HerdrSession     string
	HerdrWorkspaceID string
	HerdrTabID       string
	HerdrPaneID      string
}

// ArchiveDirName holds the scratch directories of finished tasks, one per
// cleanup. It is a directory under the state tree so the spawn-time id
// collision scan, which considers files and the tasktmp tree, never sees it.
// It lives here because cleanup writes the layout and spawn reads it, and a
// name both sides own a half of belongs to neither.
const ArchiveDirName = "archive"

// CleanupLockName is the per-task lock cleanup holds while it retires a
// task's record and archives its scratch directory. Any command that writes
// into a live task's tasktmp takes the same lock, so it can neither resurrect
// an archived directory nor write into one mid-archive.
func CleanupLockName(id string) string {
	return ".cleanup-" + id + ".lock"
}

// ValidTaskID rejects IDs that would escape or ambiguously name a task's
// state files. IDs are deliberately ASCII-only because they also become Herdr
// tab labels and wake keys.
func ValidTaskID(id string) error {
	if id == "" {
		return fmt.Errorf("state: task ID is required")
	}
	if len(id) > 64 {
		return fmt.Errorf("state: task ID %q exceeds 64 bytes", id)
	}
	if id[0] == '.' {
		return fmt.Errorf("state: task ID %q must not begin with '.'", id)
	}
	if id[len(id)-1] == '.' {
		return fmt.Errorf("state: task ID %q must not end with '.'", id)
	}
	for _, ch := range id {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		return fmt.Errorf("state: task ID %q has invalid character %q", id, ch)
	}
	return nil
}

// ReadTaskMeta reads the compatibility record through ReadMeta, retaining its
// CRLF tolerance and last-value-wins semantics.
func ReadTaskMeta(stateDir, id string) (TaskMeta, error) {
	if err := ValidTaskID(id); err != nil {
		return TaskMeta{}, err
	}
	kv, err := ReadMeta(filepath.Join(stateDir, id+".meta"))
	if err != nil {
		return TaskMeta{}, err
	}
	meta := TaskMeta{
		ID:               id,
		Window:           kv["window"],
		EndpointTaskID:   kv["endpoint_task_id"],
		Worktree:         kv["worktree"],
		Project:          kv["project"],
		Harness:          kv["harness"],
		Kind:             kv["kind"],
		Mode:             kv["mode"],
		Yolo:             kv["yolo"],
		TaskTmp:          kv["tasktmp"],
		Brief:            kv["brief"],
		Model:            kv["model"],
		Effort:           kv["effort"],
		SpawnGen:         kv["spawn_gen"],
		Backend:          kv["backend"],
		HerdrSession:     kv["herdr_session"],
		HerdrWorkspaceID: kv["herdr_workspace_id"],
		HerdrTabID:       kv["herdr_tab_id"],
		HerdrPaneID:      kv["herdr_pane_id"],
	}
	if meta.Kind == "" {
		meta.Kind = "ship"
	}
	return meta, nil
}

// WriteTaskMeta atomically writes one deterministic upstream-compatible
// metadata map. Optional fields remain absent rather than receiving invented
// empty-value meaning.
func WriteTaskMeta(stateDir string, meta TaskMeta) error {
	if err := ValidTaskID(meta.ID); err != nil {
		return err
	}
	if err := validateTaskMetaValues(meta); err != nil {
		return err
	}
	if meta.Kind == "" {
		meta.Kind = "ship"
	}
	if meta.Backend == "herdr" {
		for key, value := range map[string]string{
			"herdr_session":      meta.HerdrSession,
			"herdr_workspace_id": meta.HerdrWorkspaceID,
			"herdr_tab_id":       meta.HerdrTabID,
			"herdr_pane_id":      meta.HerdrPaneID,
		} {
			if value == "" {
				return fmt.Errorf("state: herdr metadata requires %s", key)
			}
		}
	}

	fields := map[string]string{
		"window":             meta.Window,
		"endpoint_task_id":   meta.EndpointTaskID,
		"worktree":           meta.Worktree,
		"project":            meta.Project,
		"harness":            meta.Harness,
		"kind":               meta.Kind,
		"tasktmp":            meta.TaskTmp,
		"brief":              meta.Brief,
		"model":              meta.Model,
		"effort":             meta.Effort,
		"spawn_gen":          meta.SpawnGen,
		"backend":            meta.Backend,
		"herdr_session":      meta.HerdrSession,
		"herdr_workspace_id": meta.HerdrWorkspaceID,
		"herdr_tab_id":       meta.HerdrTabID,
		"herdr_pane_id":      meta.HerdrPaneID,
	}
	if meta.Kind == "ship" {
		fields["mode"] = meta.Mode
		fields["yolo"] = meta.Yolo
	}
	for key, value := range fields {
		if value == "" {
			delete(fields, key)
		}
	}
	return WriteMeta(filepath.Join(stateDir, meta.ID+".meta"), fields)
}

func validateTaskMetaValues(meta TaskMeta) error {
	fields := []struct {
		name  string
		value string
	}{
		{"window", meta.Window},
		{"endpoint_task_id", meta.EndpointTaskID},
		{"worktree", meta.Worktree},
		{"project", meta.Project},
		{"harness", meta.Harness},
		{"kind", meta.Kind},
		{"mode", meta.Mode},
		{"yolo", meta.Yolo},
		{"tasktmp", meta.TaskTmp},
		{"brief", meta.Brief},
		{"model", meta.Model},
		{"effort", meta.Effort},
		{"spawn_gen", meta.SpawnGen},
		{"backend", meta.Backend},
		{"herdr_session", meta.HerdrSession},
		{"herdr_workspace_id", meta.HerdrWorkspaceID},
		{"herdr_tab_id", meta.HerdrTabID},
		{"herdr_pane_id", meta.HerdrPaneID},
	}
	for _, field := range fields {
		if control, found := firstControlCharacter(field.value); found {
			return fmt.Errorf("state: task metadata %s contains control character %U", field.name, control)
		}
	}
	return nil
}

func firstControlCharacter(value string) (rune, bool) {
	for _, char := range value {
		if unicode.IsControl(char) {
			return char, true
		}
	}
	return 0, false
}
