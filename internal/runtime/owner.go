package runtime

import (
	"fmt"
	"path/filepath"
	"strings"
)

// OwnerKind names who a running thing belongs to. The four are the four
// answers the CFO needs before it can decide anything: leave it alone, it is
// the project's, it is the Overlord's, or nobody is coming back for it.
type OwnerKind string

const (
	// OwnerGoblin is a live goblin's bench stack. Never touch it.
	OwnerGoblin OwnerKind = "goblin"
	// OwnerProject is a project's own local stack, declared by a manifest the
	// project commits. The demo stack the Overlord walks prospects through is
	// this: it is not a goblin's, and it is not abandoned.
	OwnerProject OwnerKind = "project"
	// OwnerOverlord is something started from a directory no project and no
	// task record claims - the Overlord's own scratch stack.
	OwnerOverlord OwnerKind = "overlord"
	// OwnerUnowned is a leftover: its directory is gone, or its task has been
	// retired, or it declared no directory at all. This is the column that
	// should be empty and never is.
	OwnerUnowned OwnerKind = "unowned"
)

// Owner is an attribution and the evidence behind it. Evidence is not
// decoration: an attribution the CFO cannot check is one it will not act on,
// and the wrong answer here means either a killed working goblin or a demo
// stack left to rot.
type Owner struct {
	Kind     OwnerKind `json:"kind"`
	Project  string    `json:"project,omitempty"`
	TaskID   string    `json:"task_id,omitempty"`
	Evidence string    `json:"evidence"`
	// Reap records that `cfo reap` is the command that retires this. It is a
	// pointer, never an action: reap owns that decision and already refuses
	// to kill what is still working.
	Reap bool `json:"reap,omitempty"`
}

// Label renders the owner as the one string a grouped report heads a section
// with.
func (o Owner) Label() string {
	switch {
	case o.TaskID != "":
		return string(o.Kind) + " " + o.TaskID
	case o.Project != "":
		return string(o.Kind) + " " + o.Project
	default:
		return string(o.Kind)
	}
}

// Attribution resolves who owns a directory or a declared stack name. It is
// built once from an inventory and then asked; the lookups behind it are what
// make each answer provable.
type Attribution struct {
	// worktrees maps a normalized worktree path to the live task holding it.
	worktrees map[string]Task
	// tasks maps a task id to its live record. A task that recorded no
	// worktree is absent from worktrees but still live, and a name-based
	// lookup must not report it as retired.
	tasks map[string]Task
	// checkouts maps a normalized checkout path to its project name.
	checkouts map[string]Checkout
	// stacks maps a stack name a project's own manifest declares to the
	// project and the manifest that declared it.
	stacks map[string]declaredStack
	// retired is every task id whose record has been archived.
	retired map[string]bool
	// present records, for each directory the collector actually checked,
	// whether it is still on disk. A directory absent from this map was never
	// checked, which is a third state and not the same as missing: only a
	// directory looked for and not found is evidence of a leftover.
	present map[string]bool
}

// declaredStack is one project's claim on a stack name.
type declaredStack struct {
	project Project
	source  string
}

// NewAttribution indexes an inventory for attribution.
func NewAttribution(inv Inventory) Attribution {
	attribution := Attribution{
		worktrees: make(map[string]Task, len(inv.Tasks)),
		tasks:     make(map[string]Task, len(inv.Tasks)),
		checkouts: make(map[string]Checkout, len(inv.Checkouts)),
		stacks:    make(map[string]declaredStack),
		retired:   inv.Retired,
		present:   make(map[string]bool, len(inv.Checkouts)+len(inv.Present)),
	}
	if attribution.retired == nil {
		attribution.retired = map[string]bool{}
	}
	for _, task := range inv.Tasks {
		attribution.tasks[task.ID] = task
		if task.Worktree != "" {
			attribution.worktrees[normalize(task.Worktree)] = task
		}
	}
	for key, found := range inv.Present {
		attribution.present[normalize(key)] = found
	}
	for _, checkout := range inv.Checkouts {
		key := normalize(checkout.Path)
		attribution.checkouts[key] = checkout
		attribution.present[key] = true
	}
	for _, project := range inv.Projects {
		for _, stack := range project.Stacks {
			if stack.Name == "" {
				continue
			}
			// First declaration wins, so two projects that somehow declare
			// the same stack name resolve deterministically rather than by
			// map iteration order.
			key := strings.ToLower(stack.Name)
			if _, taken := attribution.stacks[key]; !taken {
				attribution.stacks[key] = declaredStack{project: project, source: stack.Source}
			}
		}
	}
	return attribution
}

// Of attributes something started from workDir and declaring the stack name
// stack. Either may be empty.
//
// The order is strongest evidence first, and the first rule that holds wins:
//
//  1. the directory is inside a live task's worktree - a goblin is working
//     in it;
//  2. the project's own committed manifest declares this stack name - it is
//     the project's local stack wherever the CLI happened to be run from;
//  3. the directory is inside a project's main checkout;
//  4. the stack name itself carries a task id - the last mark left on a
//     volume once every container that touched it is gone;
//  5. the directory belongs to a retired task, or is gone - a leftover;
//  6. the directory exists and nothing claims it - the Overlord's own.
//
// Rule 2 sitting above rule 3 is what finally separates the demo stack from a
// goblin's bench stack: both can be brought up from a worktree, but only the
// project commits the name.
func (a Attribution) Of(workDir, stack string) Owner {
	if task, ok := a.taskIn(workDir); ok {
		return Owner{
			Kind:     OwnerGoblin,
			Project:  projectName(task.Project),
			TaskID:   task.ID,
			Evidence: fmt.Sprintf("started from %s, inside the live worktree of task %s", workDir, task.ID),
		}
	}
	if declared, ok := a.stacks[strings.ToLower(stack)]; ok {
		return Owner{
			Kind:    OwnerProject,
			Project: declared.project.Name,
			Evidence: fmt.Sprintf("%s declares the stack name %q, so it is %s's own local stack",
				declared.source, stack, declared.project.Name),
		}
	}
	if checkout, ok := a.checkoutIn(workDir); ok {
		return Owner{
			Kind:     OwnerProject,
			Project:  checkout.Name,
			Evidence: fmt.Sprintf("started from %s, inside the main checkout of %s", workDir, checkout.Name),
		}
	}
	// A stack name that is itself a task id is the last owner mark left once
	// a container is gone and only its volumes remain. compose derives the
	// stack name from the directory it was run in, so a stack brought up in
	// gb-<id> is named gb-<id>, and that survives the worktree itself.
	if id, ok := a.taskFromStack(stack); ok {
		if task, live := a.liveTask(id); live {
			return Owner{
				Kind:     OwnerGoblin,
				Project:  projectName(task.Project),
				TaskID:   task.ID,
				Evidence: fmt.Sprintf("its name carries task %s, which is live", task.ID),
			}
		}
		return Owner{
			Kind:     OwnerUnowned,
			TaskID:   id,
			Evidence: fmt.Sprintf("its name carries task %s, which has been retired", id),
			Reap:     true,
		}
	}
	if workDir == "" {
		if stack != "" {
			return Owner{
				Kind:     OwnerUnowned,
				Evidence: fmt.Sprintf("its name carries the stack %q, which no task record and no project manifest claims", stack),
			}
		}
		return Owner{
			Kind:     OwnerUnowned,
			Evidence: "carries neither a working directory nor a stack name, so nothing can be traced back to a project or a task",
		}
	}
	if taskID, ok := worktreeTaskID(workDir); ok && a.retired[taskID] {
		return Owner{
			Kind:     OwnerUnowned,
			Project:  projectFromWorktree(workDir),
			TaskID:   taskID,
			Evidence: fmt.Sprintf("started from %s, whose task %s has been retired", workDir, taskID),
			Reap:     true,
		}
	}
	found, checked := a.present[normalize(workDir)]
	if checked && !found {
		owner := Owner{
			Kind:     OwnerUnowned,
			Project:  projectFromWorktree(workDir),
			Evidence: fmt.Sprintf("started from %s, which is no longer on disk", workDir),
			Reap:     true,
		}
		if taskID, ok := worktreeTaskID(workDir); ok {
			owner.TaskID = taskID
		}
		return owner
	}
	if !checked {
		// Never looked for. Reporting it as abandoned would put a `cfo reap`
		// pointer on a directory nothing has established is gone.
		return Owner{
			Kind:     OwnerUnowned,
			Project:  projectFromWorktree(workDir),
			Evidence: fmt.Sprintf("started from %s, which no project manifest and no task record claims and which was not checked on disk", workDir),
		}
	}
	return Owner{
		Kind:     OwnerOverlord,
		Project:  projectFromWorktree(workDir),
		Evidence: fmt.Sprintf("started from %s, which exists but no project manifest and no task record claims", workDir),
	}
}

// taskIn resolves a directory to the live task whose worktree contains it. A
// dev server is started in the directory it serves, which is often a
// subdirectory of the worktree - gb-X\frontend - so matching the worktree path
// alone reported a working goblin's server as nobody's.
func (a Attribution) taskIn(dir string) (Task, bool) {
	for _, key := range ancestors(dir) {
		if task, ok := a.worktrees[key]; ok {
			return task, true
		}
	}
	return Task{}, false
}

// checkoutIn resolves a directory to the project checkout containing it.
//
// The search stops at a fleet worktree: a worktree lives inside the checkout
// it was made from but is never part of it, and without that stop every
// leftover in a retired worktree would read as the project's own checkout.
func (a Attribution) checkoutIn(dir string) (Checkout, bool) {
	if _, inWorktree := worktreeRoot(dir); inWorktree {
		return Checkout{}, false
	}
	for _, key := range ancestors(dir) {
		if checkout, ok := a.checkouts[key]; ok {
			return checkout, true
		}
	}
	return Checkout{}, false
}

// ancestors returns the directory and each of its parents, normalized and
// deepest first, so a lookup over them finds the longest known path that
// contains the directory - the same longest-match rule internal/reap resolves
// a worktree by.
func ancestors(dir string) []string {
	key := normalize(dir)
	var walk []string
	for key != "" {
		walk = append(walk, key)
		parent := normalize(filepath.Dir(key))
		if parent == key {
			break
		}
		key = parent
	}
	return walk
}

// taskFromStack recovers the task id a stack name carries, with or without
// the gb- prefix spawn gives a worktree. Only an id the fleet has a record of
// counts, live or retired: a stack that merely happens to be named like a
// task is not one, and inventing a task id for it would be worse than
// reporting no owner at all.
func (a Attribution) taskFromStack(stack string) (string, bool) {
	if stack == "" {
		return "", false
	}
	for _, candidate := range []string{strings.TrimPrefix(stack, worktreePrefix), stack} {
		if candidate == "" {
			continue
		}
		if _, live := a.liveTask(candidate); live {
			return candidate, true
		}
		if a.retired[candidate] {
			return candidate, true
		}
	}
	return "", false
}

// liveTask finds a task by id among the records that still exist.
func (a Attribution) liveTask(id string) (Task, bool) {
	task, ok := a.tasks[id]
	return task, ok
}

// worktreePrefix is the directory every fleet worktree lives under, and the
// gb- prefix spawn gives each one. Together they are what lets a bare path
// name the task that held it, long after the task's record is archived.
const (
	worktreeDirName = ".worktrees"
	worktreePrefix  = "gb-"
)

// worktreeRoot finds the fleet worktree a directory is inside: the directory
// itself, or the nearest ancestor whose parent is .worktrees. It returns false
// for any path that is not inside a fleet worktree, so a project checkout or
// an unrelated directory never gets a task attributed to it.
func worktreeRoot(dir string) (string, bool) {
	cleaned := strings.TrimRight(filepath.Clean(dir), `\/`)
	for cleaned != "" {
		parent := filepath.Dir(cleaned)
		if strings.EqualFold(filepath.Base(parent), worktreeDirName) {
			if id, ok := strings.CutPrefix(filepath.Base(cleaned), worktreePrefix); ok && id != "" {
				return cleaned, true
			}
			return "", false
		}
		if parent == cleaned {
			break
		}
		cleaned = parent
	}
	return "", false
}

// worktreeTaskID recovers the task id of the worktree a directory is inside.
func worktreeTaskID(dir string) (string, bool) {
	root, ok := worktreeRoot(dir)
	if !ok {
		return "", false
	}
	return strings.TrimPrefix(filepath.Base(root), worktreePrefix), true
}

// projectFromWorktree names the project a worktree path belongs to: the
// directory holding the .worktrees/ the worktree sits in.
func projectFromWorktree(dir string) string {
	root, ok := worktreeRoot(dir)
	if !ok {
		return ""
	}
	return projectName(filepath.Dir(filepath.Dir(root)))
}

// projectName is a checkout path reduced to the name the fleet calls it by.
func projectName(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Base(strings.TrimRight(filepath.Clean(path), `\/`))
}

// normalize folds a Windows path for comparison, matching how internal/reap
// compares them: lowercase, forward slashes rewritten as backslashes, and no
// trailing separator, so a path written any of the three ways still matches.
func normalize(path string) string {
	if path == "" {
		return ""
	}
	folded := strings.ToLower(strings.ReplaceAll(path, "/", `\`))
	return strings.TrimRight(folded, `\`)
}
