package runtime

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// ContainerReader, VolumeReader and the rest are the seams the collector is
// tested through. They exist because the three sources that matter here -
// Docker, the socket table and the machine reading - have no test double
// anywhere else in the tree, and a test must never depend on what happens to
// be running on the machine that day.
type (
	// DockerSource reads container, volume and disk-usage state.
	DockerSource interface {
		Containers(ctx context.Context) ([]Container, error)
		Volumes(ctx context.Context) ([]Volume, error)
		Usage(ctx context.Context) (images, containers, volumes, buildCache, reclaimable int64, err error)
	}
	// SystemSource reads listening sockets and the machine's headroom.
	SystemSource interface {
		Listeners(ctx context.Context) ([]Listener, error)
		Machine(ctx context.Context, disk string) (Machine, error)
	}
)

// Collector assembles one Inventory from every source. Each source degrades
// on its own: a source that cannot be read leaves a note naming it and what
// fixes it, and the rest of the report is still produced.
//
// This is First Mate's bootstrap discipline, and it is the one thing this
// command cannot do without. A section that renders empty because Docker was
// not running reads exactly like a machine with nothing on it, which is the
// most expensive wrong answer this report could give - it is the answer that
// says there is room to dispatch.
type Collector struct {
	Home   home.Home
	Docker DockerSource
	System SystemSource
}

// Collect reads every source once.
func (c Collector) Collect(ctx context.Context) (Inventory, error) {
	inv := Inventory{
		Containers: []Container{},
		Volumes:    []Volume{},
		Listeners:  []Listener{},
		Retired:    map[string]bool{},
		Present:    map[string]bool{},
		SystemRoot: os.Getenv("SystemRoot"),
	}
	if inv.SystemRoot == "" {
		inv.SystemRoot = os.Getenv("windir")
	}

	tasks, unreadable, err := c.tasks()
	if err != nil {
		return Inventory{}, err
	}
	inv.Tasks = tasks
	if len(unreadable) > 0 {
		inv.Notes = append(inv.Notes, "TASK RECORDS UNREADABLE: "+strings.Join(unreadable, ", ")+
			" - a worktree whose task record cannot be read is not a worktree whose task finished; nothing running in one is reported as safe to stop until the record is readable")
	}
	inv.Retired = c.retired()
	inv.Checkouts = c.checkouts(tasks)

	if c.Docker != nil {
		containers, err := c.Docker.Containers(ctx)
		if err != nil {
			inv.Notes = append(inv.Notes, "CONTAINERS UNREADABLE: "+err.Error()+" - start Docker Desktop and run this again; until then no container on this machine is accounted for")
		} else {
			inv.Containers = containers
		}
		volumes, err := c.Docker.Volumes(ctx)
		if err != nil {
			inv.Notes = append(inv.Notes, "VOLUMES UNREADABLE: "+err.Error()+" - orphaned volumes are not accounted for in the disk figures below")
		} else {
			inv.Volumes = volumes
		}
		images, containerBytes, volumeBytes, buildCache, reclaimable, err := c.Docker.Usage(ctx)
		if err != nil {
			inv.Notes = append(inv.Notes, "DOCKER USAGE UNREADABLE: "+err.Error()+" - the disk figures below exclude everything Docker holds")
		} else {
			inv.Machine.DockerImages = images
			inv.Machine.DockerContainers = containerBytes
			inv.Machine.DockerVolumes = volumeBytes
			inv.Machine.DockerBuildCache = buildCache
			inv.Machine.DockerReclaimable = reclaimable
		}
	} else {
		inv.Notes = append(inv.Notes, "CONTAINERS UNREADABLE: no Docker source configured")
	}

	if c.System != nil {
		listeners, err := c.System.Listeners(ctx)
		if err != nil {
			inv.Notes = append(inv.Notes, "SERVERS UNREADABLE: "+err.Error()+" - stale dev servers holding worktrees are not accounted for")
		} else {
			inv.Listeners = listeners
		}
		machine, err := c.System.Machine(ctx, diskVolume(c.Home.Root))
		if err != nil {
			inv.Notes = append(inv.Notes, "HEADROOM UNREADABLE: "+err.Error()+" - do not treat the absence of a warning below as room to dispatch")
		} else {
			machine.DockerImages = inv.Machine.DockerImages
			machine.DockerContainers = inv.Machine.DockerContainers
			machine.DockerVolumes = inv.Machine.DockerVolumes
			machine.DockerBuildCache = inv.Machine.DockerBuildCache
			machine.DockerReclaimable = inv.Machine.DockerReclaimable
			inv.Machine = machine
		}
	} else {
		inv.Notes = append(inv.Notes, "SERVERS UNREADABLE: no system source configured")
	}

	var projectNotes []string
	inv.Projects, projectNotes = c.projects(inv.Checkouts)
	inv.Notes = append(inv.Notes, projectNotes...)
	c.markPresent(&inv)
	return inv, nil
}

// tasks reads every live task record. A task's worktree is what makes a
// container or a server attributable to a goblin rather than to nobody.
//
// It also returns the id of every record it found but could not read. A task
// missing from the inventory is indistinguishable from a task that never
// existed, and the report's worst answer is telling the Overlord that a live
// goblin's worktree holds nothing.
func (c Collector) tasks() ([]Task, []string, error) {
	scan, err := state.ScanIDs(c.Home.State)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var tasks []Task
	var unreadable []string
	for _, id := range scan.MetaIDs {
		if state.ValidTaskID(id) != nil {
			continue
		}
		meta, err := state.ReadTaskMeta(c.Home.State, id)
		if err != nil {
			unreadable = append(unreadable, id)
			continue
		}
		tasks = append(tasks, Task{ID: id, Project: meta.Project, Worktree: meta.Worktree})
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	sort.Strings(unreadable)
	return tasks, unreadable, nil
}

// retired reads the ids of tasks whose records have been archived. It is what
// separates a worktree a goblin finished with - which `cfo reap` returns -
// from a directory the Overlord made and still wants.
//
// An archived record is named <id>.<timestamp> or <id>.status.<timestamp>, so
// the id is everything before the first dot.
func (c Collector) retired() map[string]bool {
	retired := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(c.Home.State, "archive"))
	if err != nil {
		return retired
	}
	for _, entry := range entries {
		id, _, found := strings.Cut(entry.Name(), ".")
		if found && id != "" && state.ValidTaskID(id) == nil {
			retired[id] = true
		}
	}
	return retired
}

// checkouts finds every project main checkout the fleet knows about: each
// clone under the home's projects/, and the project of every live task, which
// is what reaches a project cloned outside the home. internal/reap walks the
// same two roots for the same reason.
func (c Collector) checkouts(tasks []Task) []Checkout {
	roots := map[string]string{}
	projects := filepath.Join(c.Home.Root, "projects")
	if entries, err := os.ReadDir(projects); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				path := filepath.Join(projects, entry.Name())
				roots[normalize(path)] = path
			}
		}
	}
	for _, task := range tasks {
		if task.Project != "" {
			path := filepath.Clean(task.Project)
			roots[normalize(path)] = path
		}
	}
	checkouts := make([]Checkout, 0, len(roots))
	for _, path := range roots {
		checkouts = append(checkouts, Checkout{Name: filepath.Base(path), Path: path})
	}
	sort.Slice(checkouts, func(i, j int) bool { return checkouts[i].Name < checkouts[j].Name })
	return checkouts
}

// projects reads each checkout's manifests once.
func (c Collector) projects(checkouts []Checkout) ([]Project, []string) {
	projects := make([]Project, 0, len(checkouts))
	var notes []string
	for _, checkout := range checkouts {
		project, warnings := ReadProject(c.Home.Data, checkout.Name, checkout.Path)
		projects = append(projects, project)
		notes = append(notes, warnings...)
	}
	return projects, notes
}

// markPresent records, for every directory something was started from,
// whether it is still on disk. A directory that is gone is the proof that a
// container or a server is a leftover; one that was simply never checked
// proves nothing, and the two must not read alike.
func (c Collector) markPresent(inv *Inventory) {
	check := func(dir string) {
		if dir == "" {
			return
		}
		key := normalize(dir)
		if _, seen := inv.Present[key]; seen {
			return
		}
		info, err := os.Stat(dir)
		inv.Present[key] = err == nil && info.IsDir()
	}
	for _, container := range inv.Containers {
		check(container.WorkDir)
	}
	for _, listener := range inv.Listeners {
		check(listener.WorkDir)
	}
}

// diskVolume is the drive the headroom reading accounts for: the one the CFO
// home is on. The value is interpolated into a PowerShell filter string, so
// anything that is not a bare drive letter is refused rather than escaped.
func diskVolume(root string) string {
	volume := strings.ToUpper(filepath.VolumeName(root))
	if len(volume) == 2 && volume[0] >= 'A' && volume[0] <= 'Z' && volume[1] == ':' {
		return volume
	}
	return "C:"
}
