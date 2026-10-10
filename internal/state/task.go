package state

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
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
	ResumeOperation  string
	PipelineClass    string
	PipelineHash     string
	Backend          string
	HerdrSession     string
	HerdrWorkspaceID string
	HerdrTabID       string
	HerdrPaneID      string
	// FirstGen is the spawn generation the task was spawned under. Every
	// relaunch, a pause's resume, a switch and a comeback, gives the task a
	// new SpawnGen and keeps this one, so it says where this task's own
	// reports begin in a status log that an earlier task of the same id,
	// cleaned up since, wrote to before it. A task an older build spawned has
	// none until its first relaunch records the generation it ran on until
	// then.
	FirstGen string
	// Title is the task's short title: the one cfo spawn was given with
	// --title, else its backlog row's, or the one cfo title wrote since;
	// empty when it had none.
	Title string
	// GoblinName and GoblinTitle are the fun first name and title the goblin
	// was given at spawn, such as Jerry and Code Designer, which the board,
	// the merge train and the CFO call it by.
	GoblinName  string
	GoblinTitle string
	// Scratch is the task's scratch folder under the home, which its pane's
	// TEMP, TMPDIR and GOTMPDIR name and which goes with the task. A task an
	// older build spawned has none; its Go temporary directory is
	// GoTmpDir's.
	Scratch string
	// Extras are the extra worktrees `cfo worktree add` made for the task
	// beside its own, which go with it.
	Extras []string
	// Parent is the goblin a helper works for: the task that asked the
	// supervisor for it, whose branch its own is cut from and to which it
	// reports. Empty for every task the CFO dispatched.
	Parent string
	// Credentials names the services of the project's auth manifest whose
	// credentials the task's terminal carries: what its spawn gave it, plus
	// what cfo auth grant added since. Every later terminal of the task, a
	// resume's or a switch's, carries the same ones. It holds names, never a
	// value.
	Credentials []string
	// HasCredentials says the record names the task's services at all. It is
	// false only for a task spawned by a build that gave every task all of
	// its project's stored credentials: such a record names none, and its
	// next terminal is given what its brief asks for, as a spawn would.
	HasCredentials bool
	// MCPServers names the MCP servers of the project's .mcp.json the task
	// is given although their entry holds a value: the ones its brief named,
	// plus what cfo auth grant added since. A record that names none is a
	// task given none of those, which is also every task an older build
	// spawned. It holds names, never a value.
	MCPServers []string
}

// CarriedServices names, for a table, the services whose credentials the
// task's terminal carries: the ones its record names, none, or, for a task an
// older build spawned, that it holds all its project had stored. It holds
// names, never a value.
func (m TaskMeta) CarriedServices() string {
	switch {
	case !m.HasCredentials:
		return "all stored (older build)"
	case len(m.Credentials) == 0:
		return noCredentials
	}
	return strings.Join(m.Credentials, ", ")
}

// noCredentials is how a record says its task carries no service's
// credentials, which an absent key cannot: that is a task an older build
// spawned. No service is named by it, since a manifest refuses the name.
const noCredentials = "none"

// credentialsSeparator joins the services a record names. A manifest refuses
// it in a service's name.
const credentialsSeparator = ","

// extrasSeparator joins a task's extra worktrees in its record. Windows
// refuses it in a file name, so no path can hold it.
const extrasSeparator = "|"

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
//
// A cleanup holds the task's lifecycle and record locks with it, from before
// it removes anything to its end, so it and a pause, a resume, a stop, a
// switch or any other change of the record exclude each other: whichever
// takes the lock both need goes on, and the other changes nothing.
func CleanupLockName(id string) string {
	return ".cleanup-" + id + ".lock"
}

// LifecycleLockName is the per-task lock a pause, a resume and a stop hold
// from start to end, and under which a note for a paused goblin's resume is
// kept.
func LifecycleLockName(id string) string {
	return ".lifecycle-" + id + ".lock"
}

// CleanupPurpose is what a cleanup records in the lifecycle and record locks
// it holds, so a command that finds one held says in one line that the task
// is being cleaned up, and the scheduler knows a resume refused for it.
func CleanupPurpose(id string) string {
	return "the cleanup of " + id
}

// PipelineLockName is the per-task lock the pipeline commands hold to
// serialise round acceptance. It is deliberately not the cleanup lock: a gate
// runs for hours, and a cleanup lock held that long would make an auth refresh
// report a live task as being cleaned up and never deliver its credentials.
func PipelineLockName(id string) string {
	return ".pipeline-" + id + ".lock"
}

// MetadataLockName is the per-task lock held across every read-modify-write
// of the flat task metadata record. It prevents one command from publishing a
// stale record after another command has updated an unrelated field.
func MetadataLockName(id string) string {
	return ".metadata-" + id + ".lock"
}

// GoTmpDir is the per-task directory an older build pointed a goblin's
// GOTMPDIR at, before tasks had a scratch folder in the home; a task that
// build spawned still uses it, and cleanup removes it with the task. Go puts
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
// Two residuals remain and are accepted: an 8.3 short name (C:\PROGRA~1\state)
// and a symlinked or junctioned state directory each hash apart from the plain
// spelling, and neither is detectable without touching the filesystem. They
// SPLIT rather than share, and that asymmetry is what makes them acceptable: a
// split costs one wasted directory, while sharing is data destruction - one
// fleet's cleanup deleting another fleet's live GOTMPDIR. Splitting is the
// safe direction, which is the whole reason the fleet segment exists.
//
// The fleet segment hashes the cleaned state directory and is deliberately
// NOT resolved through the filesystem: two spellings of one directory hashing
// differently yields a separate unshared directory, which is harmless, while
// a resolve that succeeded in one process and failed in another could yield a
// shared one, which is the defect this scoping exists to prevent.
//
// Case folding is a property of the host's paths, not of paths in general, so
// it is applied only on Windows. Where the filesystem is case-sensitive two
// spellings differing only in case name two real directories, and folding
// them together would manufacture sharing rather than a harmless split.
//
// A relative state directory is refused rather than made absolute. CFO_STATE_
// OVERRIDE is taken verbatim, so two fleets launched from different working
// directories with the same relative override would hash one string and share
// one directory - the sharing this scoping exists to prevent. filepath.Abs
// would not fix it: the processes that must agree on this path do not share a
// working directory - a goblin runs in its worktree, cleanup runs elsewhere -
// so Abs would give one fleet two directories, which is the worse failure.
func GoTmpDir(stateDir, id string) (string, error) {
	if strings.TrimSpace(stateDir) == "" {
		return "", fmt.Errorf("state: go temporary directory needs the fleet state directory")
	}
	if !filepath.IsAbs(stateDir) {
		return "", fmt.Errorf("state: go temporary directory needs an absolute fleet state directory, got %q", stateDir)
	}
	// The directory is cleaned once and every later step reads that one value,
	// so no spelling can pass a check and then become something else before it
	// is hashed. filepath.Clean does not strip an extended-length prefix - and
	// it turns the forward-slash spelling //?/C:\x into one - so \\?\C:\x and
	// C:\x hash apart and split one fleet in two. It is cheap to detect and
	// exotic enough that refusing beats splitting silently.
	cleaned := filepath.Clean(stateDir)
	if strings.HasPrefix(cleaned, `\\?\`) {
		return "", fmt.Errorf("state: go temporary directory needs a plain fleet state directory, got extended-length path %q", stateDir)
	}
	if err := ValidTaskID(id); err != nil {
		return "", err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("state: resolve user cache directory: %w", err)
	}
	fleetKey := cleaned
	if runtime.GOOS == "windows" {
		fleetKey = strings.ToLower(fleetKey)
	}
	sum := sha256.Sum256([]byte(fleetKey))
	fleet := hex.EncodeToString(sum[:])[:8]
	return filepath.Join(cache, "cfo", "gotmp", fleet, id), nil
}

// TaskScratch is the folder a task's pane's TEMP, TMPDIR and GOTMPDIR name: the
// scratch folder its record names, or, for a task an older build spawned with
// none, its Go temporary directory, which is where that build pointed
// GOTMPDIR.
func TaskScratch(stateDir string, meta TaskMeta) (string, error) {
	if meta.Scratch != "" {
		return meta.Scratch, nil
	}
	return GoTmpDir(stateDir, meta.ID)
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
// TaskMetaPath is where task id's record is.
func TaskMetaPath(stateDir, id string) string { return filepath.Join(stateDir, id+".meta") }

func ReadTaskMeta(stateDir, id string) (TaskMeta, error) {
	if err := ValidTaskID(id); err != nil {
		return TaskMeta{}, err
	}
	kv, err := ReadMeta(TaskMetaPath(stateDir, id))
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
		FirstGen:         kv["first_gen"],
		ResumeOperation:  kv["resume_operation"],
		PipelineClass:    kv["pipeline_class"],
		PipelineHash:     kv["pipeline_hash"],
		Backend:          kv["backend"],
		HerdrSession:     kv["herdr_session"],
		HerdrWorkspaceID: kv["herdr_workspace_id"],
		HerdrTabID:       kv["herdr_tab_id"],
		HerdrPaneID:      kv["herdr_pane_id"],
		Title:            kv["title"],
		GoblinName:       kv["goblin_name"],
		GoblinTitle:      kv["goblin_title"],
		Scratch:          kv["scratch"],
		Parent:           kv["parent"],
	}
	if extras := kv["extras"]; extras != "" {
		meta.Extras = strings.Split(extras, extrasSeparator)
	}
	if credentials := kv["credentials"]; credentials != "" {
		meta.HasCredentials = true
		if credentials != noCredentials {
			meta.Credentials = strings.Split(credentials, credentialsSeparator)
		}
	}
	if servers := kv["mcp"]; servers != "" {
		meta.MCPServers = strings.Split(servers, credentialsSeparator)
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
		"first_gen":          meta.FirstGen,
		"resume_operation":   meta.ResumeOperation,
		"pipeline_class":     meta.PipelineClass,
		"pipeline_hash":      meta.PipelineHash,
		"backend":            meta.Backend,
		"herdr_session":      meta.HerdrSession,
		"herdr_workspace_id": meta.HerdrWorkspaceID,
		"herdr_tab_id":       meta.HerdrTabID,
		"herdr_pane_id":      meta.HerdrPaneID,
		"title":              meta.Title,
		"goblin_name":        meta.GoblinName,
		"goblin_title":       meta.GoblinTitle,
		"scratch":            meta.Scratch,
		"extras":             strings.Join(meta.Extras, extrasSeparator),
		"parent":             meta.Parent,
		"mcp":                strings.Join(meta.MCPServers, credentialsSeparator),
	}
	if meta.Kind == "ship" {
		fields["mode"] = meta.Mode
		fields["yolo"] = meta.Yolo
	}
	if meta.HasCredentials {
		fields["credentials"] = noCredentials
		if len(meta.Credentials) > 0 {
			fields["credentials"] = strings.Join(meta.Credentials, credentialsSeparator)
		}
	}
	for key, value := range fields {
		if value == "" {
			delete(fields, key)
		}
	}
	return WriteMeta(filepath.Join(stateDir, meta.ID+".meta"), fields)
}

// WriteTaskCredentials names services as the ones task id carries, in its
// record, and leaves every other line of the record as it is, the ones this
// build does not read included. Its caller holds the record's lock.
func WriteTaskCredentials(stateDir, id string, services []string) error {
	if err := validateTaskMetaValues(TaskMeta{ID: id, Credentials: services, HasCredentials: true}); err != nil {
		return err
	}
	path := TaskMetaPath(stateDir, id)
	record, err := ReadMeta(path)
	if err != nil {
		return err
	}
	record["credentials"] = noCredentials
	if len(services) > 0 {
		record["credentials"] = strings.Join(services, credentialsSeparator)
	}
	return WriteMeta(path, record)
}

// WriteTaskMCPServers names servers as the MCP servers task id is given
// although their entry holds a value, in its record, and leaves every other
// line of the record as it is. Its caller holds the record's lock.
func WriteTaskMCPServers(stateDir, id string, servers []string) error {
	if err := validateTaskMetaValues(TaskMeta{ID: id, MCPServers: servers}); err != nil {
		return err
	}
	path := TaskMetaPath(stateDir, id)
	record, err := ReadMeta(path)
	if err != nil {
		return err
	}
	delete(record, "mcp")
	if len(servers) > 0 {
		record["mcp"] = strings.Join(servers, credentialsSeparator)
	}
	return WriteMeta(path, record)
}

func validateTaskMetaValues(meta TaskMeta) error {
	if meta.Parent != "" {
		if err := ValidTaskID(meta.Parent); err != nil {
			return fmt.Errorf("state: task metadata parent: %w", err)
		}
		if strings.EqualFold(meta.Parent, meta.ID) {
			return fmt.Errorf("state: task %s cannot be its own parent", meta.ID)
		}
	}
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
		{"first_gen", meta.FirstGen},
		{"resume_operation", meta.ResumeOperation},
		{"pipeline_class", meta.PipelineClass},
		{"pipeline_hash", meta.PipelineHash},
		{"backend", meta.Backend},
		{"herdr_session", meta.HerdrSession},
		{"herdr_workspace_id", meta.HerdrWorkspaceID},
		{"herdr_tab_id", meta.HerdrTabID},
		{"herdr_pane_id", meta.HerdrPaneID},
		{"title", meta.Title},
		{"goblin_name", meta.GoblinName},
		{"goblin_title", meta.GoblinTitle},
		{"scratch", meta.Scratch},
	}
	if len(meta.Credentials) > 0 && !meta.HasCredentials {
		return errors.New("state: task metadata names credentials it does not record")
	}
	for _, service := range meta.Credentials {
		if service == "" || service == noCredentials || strings.Contains(service, credentialsSeparator) {
			return fmt.Errorf("state: task metadata credentials service %q is empty, is %q or holds %q", service, noCredentials, credentialsSeparator)
		}
		fields = append(fields, struct {
			name  string
			value string
		}{"credentials", service})
	}
	for _, server := range meta.MCPServers {
		if server == "" || strings.Contains(server, credentialsSeparator) {
			return fmt.Errorf("state: task metadata MCP server %q is empty or holds %q", server, credentialsSeparator)
		}
		fields = append(fields, struct {
			name  string
			value string
		}{"mcp", server})
	}
	for _, extra := range meta.Extras {
		if extra == "" || strings.Contains(extra, extrasSeparator) {
			return fmt.Errorf("state: task metadata extra worktree %q is empty or holds %q", extra, extrasSeparator)
		}
		fields = append(fields, struct {
			name  string
			value string
		}{"extras", extra})
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
