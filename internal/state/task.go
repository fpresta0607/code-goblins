package state

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	PipelineClass    string
	PipelineHash     string
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

// AuthScriptName is the restricted credential script every pane shell
// dot-sources before the harness starts. It is regenerated, never edited: a
// hand-appended line is exactly how a credential stops matching the store.
// It lives here because spawn writes the script and cleanup removes it before
// archiving a tasktmp, and a name both sides own a half of belongs to neither.
const AuthScriptName = "auth.ps1"

// CleanupLockName is the per-task lock cleanup holds while it retires a
// task's record and archives its scratch directory. Any command that writes
// into a live task's tasktmp takes the same lock, so it can neither resurrect
// an archived directory nor write into one mid-archive.
func CleanupLockName(id string) string {
	return ".cleanup-" + id + ".lock"
}

// PipelineLockName is the per-task lock the pipeline commands hold to
// serialise round acceptance. It is deliberately not the cleanup lock: a gate
// runs for hours, and a cleanup lock held that long would make an auth refresh
// report a live task as being cleaned up and never deliver its credentials.
func PipelineLockName(id string) string {
	return ".pipeline-" + id + ".lock"
}

// GoTmpDir is the per-task directory a goblin's GOTMPDIR points at. Go puts
// build and test temporaries there, t.TempDir() included, so it is
// deliberately outside the fleet checkout: pointed inside it, every test a
// goblin runs creates files in the tree the goblin is editing. It lives here
// because spawn creates it and cleanup removes it, and a name both sides own
// a half of belongs to neither. cleanup.removeGoTmp is the one place that
// documents who removes it and when.
//
// It sits under the user cache directory rather than the machine temporary
// directory because a goblin task is live for days and %TEMP% is the one
// directory Windows itself prunes (Storage Sense, Disk Cleanup): pruned under
// a running pane, the goblin's next go build fails on a GOTMPDIR that no
// longer exists. The user cache directory is where Go already keeps go-build,
// so a Go temporary directory beside it is the idiomatic neighbour rather
// than a directory the OS treats as disposable.
//
// The path is keyed on the fleet as well as the task. The directory it
// replaced was inside a fleet's own state tree and so could never be shared;
// under a machine-global base, two fleet homes on one machine that each hold
// a task named g1 would share one directory, and cleaning up g1 in one would
// recursively delete the live GOTMPDIR of g1 in the other.
//
// The fleet segment hashes the state directory as written, lowercased because
// Windows paths are case-insensitive, and is deliberately NOT resolved through
// the filesystem: two spellings of one directory hashing differently yields a
// separate unshared directory, which is harmless, while a resolve that
// succeeded in one process and failed in another could yield a shared one,
// which is the defect this scoping exists to prevent.
func GoTmpDir(stateDir, id string) (string, error) {
	if strings.TrimSpace(stateDir) == "" {
		return "", fmt.Errorf("state: go temporary directory needs the fleet state directory")
	}
	if err := ValidTaskID(id); err != nil {
		return "", err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("state: resolve user cache directory: %w", err)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(stateDir))))
	fleet := hex.EncodeToString(sum[:])[:8]
	return filepath.Join(cache, "cfo", "gotmp", fleet, id), nil
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
		PipelineClass:    kv["pipeline_class"],
		PipelineHash:     kv["pipeline_hash"],
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

// RemoveTaskMeta retires state/<id>.meta, tolerating a reader that happens to
// hold the file open. Windows refuses to unlink a file while any handle is
// open, and every supervision cycle reads every meta (monitor.Service.Scan and
// reap.Collector.Collect both do), so an unlucky despawn or cleanup would
// otherwise fail with a sharing violation for the few microseconds a reader is
// inside os.ReadFile. Measured on Windows 11, a single tight-loop reader beats
// a plain os.Remove about 2.5% of the time. An already-absent meta is success:
// retiring a task that is already retired is the caller's intent either way.
func RemoveTaskMeta(stateDir, id string) error {
	if err := ValidTaskID(id); err != nil {
		return err
	}
	path := filepath.Join(stateDir, id+".meta")
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.Remove(path)
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
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
		"pipeline_class":     meta.PipelineClass,
		"pipeline_hash":      meta.PipelineHash,
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
		{"pipeline_class", meta.PipelineClass},
		{"pipeline_hash", meta.PipelineHash},
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
