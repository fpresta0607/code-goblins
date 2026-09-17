// Package runtime answers one question the CFO otherwise reconstructs by hand
// before every dispatch: what is running on this machine, who owns it, what
// the machine has left to give, and where each project's work actually lands.
//
// No single source knows any of it. Docker knows containers but not which
// goblin started them; the process table knows listening ports but not which
// worktree is a live task's; the project checkouts know their deploy targets
// but nothing about what is running. So the collector reads each source once
// into one plain Inventory, and Build turns that into the report as a pure
// function - which is what lets the attribution be tested over fixtures
// instead of over whatever happens to be running today.
//
// Everything here is read-only. Where the report finds something that should
// be retired it names `cfo reap`, which already owns that decision and
// already refuses to kill what is still working.
package runtime

import (
	"strings"
	"time"
)

// Schema names the typed report, matching the convention fleet-view set.
const Schema = "runtime-report.v1"

// Inventory is everything the report is computed from: a plain value with no
// I/O, so attribution is a pure function over fixtures. Collect builds the
// production one.
type Inventory struct {
	// Containers is every container Docker still holds a record for, running
	// or exited.
	Containers []Container
	// Volumes is every named volume on the machine. Anonymous volumes (a
	// 64-character hex id) are Docker's own bookkeeping, not a stack's
	// declared storage, and are counted but not listed.
	Volumes []Volume
	// Listeners is every listening TCP socket worth attributing, already
	// paired with its owning process.
	Listeners []Listener
	// Machine is the headroom reading.
	Machine Machine
	// Projects is each project checkout the fleet knows about, with the
	// deploy and local-stack facts already read from its manifests.
	Projects []Project
	// Tasks is every task with a live state record.
	Tasks []Task
	// Retired is the id of every task whose record has been archived. It is
	// what separates a worktree a goblin finished with from one the Overlord
	// made by hand, and so a leftover `cfo reap` would retire from one it
	// would not.
	Retired map[string]bool
	// Checkouts is every project main checkout on disk, by absolute path.
	Checkouts []Checkout
	// SystemRoot is the Windows directory. A process running there is the
	// operating system's own, never a project's, and saying so needs the
	// real value rather than an assumption about the drive letter.
	SystemRoot string
	// Present is every directory the collector checked and found on disk,
	// normalized. A directory that was never checked is not the same as one
	// that is gone, and only the second is evidence of a leftover, so
	// attribution reads this rather than touching the filesystem itself.
	Present map[string]bool
	// Notes carry each source that could not be read, in First Mate's
	// bootstrap idiom: name the source, say what is degraded, say what fixes
	// it. An unreadable source must never render as an empty section, which
	// reads as "nothing is running" and is the opposite of the truth.
	Notes []string
}

// Task is one live task record, reduced to what attribution needs.
type Task struct {
	ID       string
	Project  string
	Worktree string
}

// Checkout is one project's main checkout.
type Checkout struct {
	Name string
	Path string
}

// Container is one container and the labels that say who started it.
type Container struct {
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	Image string    `json:"image"`
	State string    `json:"state"`
	Since time.Time `json:"since"`
	// RestartCount is Docker's own tally. It is the difference between a
	// container that happens to be restarting at the moment it was read and
	// one that has been failing all night.
	RestartCount int `json:"restart_count"`
	// Stack is the compose or Supabase CLI project name.
	Stack string `json:"stack"`
	// WorkDir is the directory the stack was brought up from, from
	// com.docker.compose.project.working_dir or com.supabase.cli.workdir.
	WorkDir string `json:"work_dir"`
	// Ports are the host bindings, rendered as Docker renders them.
	Ports []string `json:"ports"`
	// Volumes are the named volumes mounted into this container.
	Volumes []string `json:"volumes"`
	// MemoryLimitBytes is the limit the stack declared, or zero when it
	// declared none.
	MemoryLimitBytes int64 `json:"memory_limit_bytes"`
}

// Running reports whether the container is holding resources right now.
// Restarting counts: a container in a restart loop is consuming memory, disk
// and attention without doing its job.
func (c Container) Running() bool {
	return c.State == "running" || c.State == "restarting"
}

// restartLoopFloor is the restart count above which a container is reported
// as looping rather than as merely having been restarted. Docker restarts a
// container once on a transient failure and twice on an unlucky boot order;
// a third is a pattern.
const restartLoopFloor = 3

// RestartLooping reports whether the container is failing repeatedly. A
// container caught mid-restart is reported on that alone, because a single
// observation of "restarting" is already the state nobody wants to find.
func (c Container) RestartLooping() bool {
	return c.State == "restarting" || c.RestartCount >= restartLoopFloor
}

// Volume is one named Docker volume.
type Volume struct {
	Name string `json:"name"`
	// SizeBytes is what the volume occupies, or zero when Docker did not
	// report a size.
	SizeBytes int64 `json:"size_bytes"`
}

// Stack returns the stack a volume's name declares. It is the only owner mark
// a volume carries once every container that touched it is gone, which is the
// state every volume in the orphaned list is in.
//
// Compose names a volume <stack>_<declared-name>, so the stack is everything
// before the first underscore. The Supabase CLI names its own
// supabase_<service>_<stack> instead, and its service names contain
// underscores (edge_runtime, pg_meta) while its stack names cannot, so there
// the stack is everything after the last one.
func (v Volume) Stack() string {
	if strings.HasPrefix(v.Name, "supabase_") {
		return v.Name[strings.LastIndex(v.Name, "_")+1:]
	}
	index := strings.Index(v.Name, "_")
	if index <= 0 {
		return ""
	}
	return v.Name[:index]
}

// Listener is one listening socket and the process behind it. The command
// line is deliberately not collected: the working directory is what answers
// the ownership question, and another user's argv can carry the tokens their
// process was started with.
type Listener struct {
	Port    int    `json:"port"`
	Address string `json:"address"`
	PID     int    `json:"pid"`
	Process string `json:"process"`
	// WorkDir is the directory the process is running in, read from its own
	// parameter block. It is empty when the process could not be opened, which
	// is a process at a privilege this command cannot read: the report folds
	// those out of the servers table and counts them rather than guessing.
	WorkDir string `json:"work_dir"`
}

// Machine is the headroom reading, in bytes throughout.
type Machine struct {
	MemoryTotal int64 `json:"memory_total"`
	// MemoryAvailable is what a new process can take without paging: the free
	// list plus the standby list Windows will evict for it. On a dev machine
	// that is gigabytes wider than free memory, and it is the figure the
	// dispatch budget is read from.
	MemoryAvailable int64  `json:"memory_available"`
	DiskTotal       int64  `json:"disk_total"`
	DiskFree        int64  `json:"disk_free"`
	DiskName        string `json:"disk_name"`
	// WSL is the working set of the WSL virtual machine, the footprint that
	// has starved this fleet before by growing without anything on the
	// Windows side accounting for it.
	WSL int64 `json:"wsl"`
	// Docker's own occupancy, from `docker system df`.
	DockerImages      int64 `json:"docker_images"`
	DockerContainers  int64 `json:"docker_containers"`
	DockerVolumes     int64 `json:"docker_volumes"`
	DockerBuildCache  int64 `json:"docker_build_cache"`
	DockerReclaimable int64 `json:"docker_reclaimable"`
}

// DockerTotal is everything Docker occupies on disk.
func (m Machine) DockerTotal() int64 {
	return m.DockerImages + m.DockerContainers + m.DockerVolumes + m.DockerBuildCache
}

// Project is one project checkout with the facts read from its manifests.
type Project struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Targets is where this project's work lands in production, read from
	// the checkout's own manifests and the project's credential manifest.
	Targets []Target `json:"targets"`
	// Local is how to run the project's stack on this machine.
	Local Local `json:"local"`
	// Stacks are the local stack names this project's own committed
	// manifests declare. They are the strongest ownership evidence there is:
	// a stack name that resembles a project's is a coincidence, but one the
	// project commits is a claim, and it holds wherever the CLI was run from.
	Stacks []StackName `json:"stacks,omitempty"`
}

// StackName is one declared stack name and the manifest that declared it.
type StackName struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// Target is one production destination.
type Target struct {
	// Provider is the hosting provider: vercel, fly, supabase, ionos.
	Provider string `json:"provider"`
	// Detail is the addressable identity - the Vercel project and scope, the
	// Fly app and region, the Supabase project ref - or empty when only a
	// credential note establishes the target.
	Detail string `json:"detail,omitempty"`
	// Source names the file each fact came from, so a wrong answer is
	// traceable to the manifest that declared it rather than to this command.
	Source string `json:"source"`
	// Note is the project's own deploy note, carried verbatim.
	Note string `json:"note,omitempty"`
}

// Local is how a project's stack is brought up and taken down here.
type Local struct {
	Up   []string `json:"up,omitempty"`
	Down []string `json:"down,omitempty"`
	// Source names where the commands came from: the project's worktree
	// manifest when it declares them, otherwise the checkout file they were
	// derived from. A derived answer is never presented as a declared one.
	Source string `json:"source"`
	// Declared records that the project's worktree manifest named these
	// commands, rather than them being derived from what the checkout holds.
	Declared bool `json:"declared"`
}
