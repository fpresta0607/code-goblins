package runtime

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Report is the typed answer to all five questions, shared by the JSON and
// Markdown renderers exactly as fleet-view's snapshot is.
type Report struct {
	Schema string `json:"schema"`
	Home   string `json:"home"`
	// Stacks are the container groups, one per owner and stack name.
	Stacks []Stack `json:"stacks"`
	// LooseVolumes are named volumes no container references. They are the
	// quiet half of the disk bill: 11 GB of them here, none attached to
	// anything.
	LooseVolumes []LooseVolume `json:"loose_volumes"`
	// Servers are the listening dev servers and APIs.
	Servers []Server `json:"servers"`
	// Folded accounts for the listeners left out of Servers and why. They are
	// counted rather than dropped: a report that silently hides a port is one
	// the CFO cannot trust when it does not find the port it is looking for.
	Folded []Folded `json:"folded"`
	// Headroom is what the machine has left to give.
	Headroom Machine `json:"headroom"`
	// Limits is the memory each running stack declared, so a dispatch
	// decision can see whether another one fits.
	Limits []StackLimit `json:"limits"`
	// Projects is where each project deploys and how to run it locally.
	Projects []Project `json:"projects"`
	// Notes name each source that could not be read.
	Notes []string `json:"notes"`
}

// Stack is one owner's group of containers sharing a compose or Supabase
// project name.
type Stack struct {
	Name       string      `json:"name"`
	Owner      Owner       `json:"owner"`
	Containers []Container `json:"containers"`
}

// Running reports whether any container in the stack is up.
func (s Stack) Running() int {
	count := 0
	for _, container := range s.Containers {
		if container.Running() {
			count++
		}
	}
	return count
}

// Looping returns every container in the stack that is failing repeatedly.
func (s Stack) Looping() []Container {
	var looping []Container
	for _, container := range s.Containers {
		if container.RestartLooping() {
			looping = append(looping, container)
		}
	}
	return looping
}

// MemoryLimit is the memory the stack's running containers declared, and
// whether every one of them declared a limit. A stack where some containers
// declared nothing has no meaningful ceiling, and saying so is the difference
// between a headroom number a dispatch can trust and one it cannot.
func (s Stack) MemoryLimit() (total int64, complete bool) {
	complete = true
	for _, container := range s.Containers {
		if !container.Running() {
			continue
		}
		if container.MemoryLimitBytes == 0 {
			complete = false
			continue
		}
		total += container.MemoryLimitBytes
	}
	return total, complete
}

// StackLimit is one running stack's declared memory ceiling.
type StackLimit struct {
	Stack string `json:"stack"`
	Owner string `json:"owner"`
	// Bytes is the sum of the declared limits of the stack's running
	// containers.
	Bytes int64 `json:"bytes"`
	// Containers is how many of the stack's containers are running.
	Containers int `json:"containers"`
	// Undeclared is how many of them declared no limit at all, and so may
	// grow without bound.
	Undeclared int `json:"undeclared"`
}

// LooseVolume is a named volume nothing references, with whatever ownership
// its name still carries.
type LooseVolume struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
	Owner Owner  `json:"owner"`
}

// Server is one listening process and the verdict on stopping it.
type Server struct {
	Port    int    `json:"port"`
	Address string `json:"address"`
	PID     int    `json:"pid"`
	Process string `json:"process"`
	WorkDir string `json:"work_dir"`
	// Directory says what kind of directory the server is running in: a live
	// goblin worktree, a retired one, or a main checkout. It is the whole
	// point of this section.
	Directory DirectoryKind `json:"directory"`
	Owner     Owner         `json:"owner"`
	// Stop is the plain answer to whether this one is safe to stop, which is
	// the check that keeps catching stale servers after a goblin is retired.
	Stop StopVerdict `json:"stop"`
}

// DirectoryKind names what a server's working directory is.
type DirectoryKind string

const (
	// DirLiveWorktree is a worktree a task still holds.
	DirLiveWorktree DirectoryKind = "live worktree"
	// DirRetiredWorktree is a worktree whose task has been retired. A server
	// still running in one is the leak this section exists to find.
	DirRetiredWorktree DirectoryKind = "retired worktree"
	// DirRecordlessWorktree is a worktree whose task appears in neither the
	// live records nor the archive. It is not evidence the task finished; it
	// is evidence the fleet cannot see it.
	DirRecordlessWorktree DirectoryKind = "worktree with no task record"
	// DirCheckout is a project's main checkout.
	DirCheckout DirectoryKind = "main checkout"
	// DirOther is a directory that is none of those.
	DirOther DirectoryKind = "other"
)

// StopVerdict is the plain answer about stopping a server.
type StopVerdict string

const (
	// StopNo is a live goblin's server. Stopping it breaks a working goblin.
	StopNo StopVerdict = "no"
	// StopSafe is a server whose task is finished. `cfo reap` retires it.
	StopSafe StopVerdict = "safe"
	// StopAsk is the Overlord's own. It is not the fleet's to stop.
	StopAsk StopVerdict = "ask"
	// StopUnknown is a server in a worktree the fleet has no task record for.
	// A record it cannot read is not a record that says the task finished, so
	// nothing may be concluded about it either way.
	StopUnknown StopVerdict = "unknown"
)

// Build turns an inventory into the report. It is a pure function: it reads
// no files, runs no commands and consults no clock, so the attribution can be
// tested over fixture inventories rather than over whatever happens to be
// running on the machine today.
func Build(home string, inv Inventory) Report {
	attribution := NewAttribution(inv)
	report := Report{
		Schema:       Schema,
		Home:         home,
		Stacks:       buildStacks(inv, attribution),
		LooseVolumes: buildLooseVolumes(inv, attribution),
		Servers:      buildServers(inv, attribution),
		Folded:       buildFolded(inv),
		Headroom:     inv.Machine,
		Projects:     inv.Projects,
		Notes:        inv.Notes,
	}
	report.Limits = buildLimits(report.Stacks)
	if report.Notes == nil {
		report.Notes = []string{}
	}
	return report
}

// buildStacks groups containers by owner and stack name. Grouping by owner
// rather than by stack name alone is deliberate: one stack name can hold
// containers with two different owners, which is exactly the confusion this
// command exists to end. A goblin that restarted one service of the
// Overlord's demo stack from its own worktree shows up as its own group,
// named, instead of hiding inside twelve containers that look identical.
func buildStacks(inv Inventory, attribution Attribution) []Stack {
	groups := map[string]*Stack{}
	for _, container := range inv.Containers {
		owner := attribution.Of(container.WorkDir, container.Stack)
		name := container.Stack
		if name == "" {
			name = "(no stack)"
		}
		key := owner.Label() + "\x00" + name
		group, ok := groups[key]
		if !ok {
			group = &Stack{Name: name, Owner: owner}
			groups[key] = group
		}
		group.Containers = append(group.Containers, container)
	}
	stacks := make([]Stack, 0, len(groups))
	for _, group := range groups {
		sort.Slice(group.Containers, func(i, j int) bool {
			return group.Containers[i].Name < group.Containers[j].Name
		})
		stacks = append(stacks, *group)
	}
	sortStacks(stacks)
	return stacks
}

// buildLooseVolumes finds named volumes no container mounts, and attributes
// each by the stack name its own name carries. A volume outlives every
// container that ever touched it, so its name is usually the only evidence
// left of who made it.
func buildLooseVolumes(inv Inventory, attribution Attribution) []LooseVolume {
	referenced := map[string]bool{}
	stackDir := map[string]string{}
	for _, container := range inv.Containers {
		for _, name := range container.Volumes {
			referenced[name] = true
		}
		if container.Stack != "" && container.WorkDir != "" {
			if _, seen := stackDir[container.Stack]; !seen {
				stackDir[container.Stack] = container.WorkDir
			}
		}
	}
	loose := make([]LooseVolume, 0)
	for _, volume := range inv.Volumes {
		if referenced[volume.Name] {
			continue
		}
		stack := volume.Stack()
		loose = append(loose, LooseVolume{
			Name:  volume.Name,
			Bytes: volume.SizeBytes,
			Owner: attribution.Of(stackDir[stack], stack),
		})
	}
	sort.Slice(loose, func(i, j int) bool { return loose[i].Name < loose[j].Name })
	return loose
}

// Folded is one class of listener the servers section leaves out, with the
// ports it covers.
type Folded struct {
	Reason string `json:"reason"`
	Ports  []int  `json:"ports"`
}

// foldReason decides whether a listener belongs in the servers section, and
// returns the reason it does not when it does not.
//
// Three classes are left out, each for a reason that is proven rather than
// guessed from a process name:
//
//   - a port a container publishes, which the containers section already owns;
//   - a process whose working directory cannot be read at this privilege,
//     which makes it another user's and not the fleet's to stop;
//   - a process running inside the Windows directory, which is the operating
//     system's own service rather than anything a project started.
//
// Without these, twenty rows of svchost and lsass bury the four that matter,
// and the section stops being read - which costs more than the small chance
// that an elevated dev server hides in the second class. The counts are
// printed, so nothing vanishes.
func foldReason(listener Listener, published map[int]bool, systemRoot string) string {
	if published[listener.Port] {
		return "published by a container (see Containers)"
	}
	if listener.WorkDir == "" {
		return "at a privilege this command cannot read, so not the fleet's to stop"
	}
	if systemRoot != "" && strings.HasPrefix(normalize(listener.WorkDir), normalize(systemRoot)) {
		return "running inside the Windows directory, so the operating system's own"
	}
	return ""
}

// publishedPorts is every host port a container binds.
func publishedPorts(containers []Container) map[int]bool {
	published := map[int]bool{}
	for _, container := range containers {
		for _, binding := range container.Ports {
			host, _, found := strings.Cut(binding, "->")
			if !found {
				continue
			}
			if index := strings.LastIndex(host, ":"); index >= 0 {
				host = host[index+1:]
			}
			if port, err := strconv.Atoi(host); err == nil {
				published[port] = true
			}
		}
	}
	return published
}

func buildFolded(inv Inventory) []Folded {
	published := publishedPorts(inv.Containers)
	byReason := map[string]map[int]bool{}
	for _, listener := range inv.Listeners {
		reason := foldReason(listener, published, inv.SystemRoot)
		if reason == "" {
			continue
		}
		if byReason[reason] == nil {
			byReason[reason] = map[int]bool{}
		}
		byReason[reason][listener.Port] = true
	}
	folded := make([]Folded, 0, len(byReason))
	for reason, ports := range byReason {
		list := make([]int, 0, len(ports))
		for port := range ports {
			list = append(list, port)
		}
		sort.Ints(list)
		folded = append(folded, Folded{Reason: reason, Ports: list})
	}
	sort.Slice(folded, func(i, j int) bool { return folded[i].Reason < folded[j].Reason })
	return folded
}

// buildServers attributes each listening process by the directory it is
// actually running in, and turns that into the one verdict the CFO needs.
func buildServers(inv Inventory, attribution Attribution) []Server {
	published := publishedPorts(inv.Containers)
	servers := make([]Server, 0, len(inv.Listeners))
	for _, listener := range inv.Listeners {
		if foldReason(listener, published, inv.SystemRoot) != "" {
			continue
		}
		owner := attribution.Of(listener.WorkDir, "")
		server := Server{
			Port:      listener.Port,
			Address:   listener.Address,
			PID:       listener.PID,
			Process:   listener.Process,
			WorkDir:   listener.WorkDir,
			Directory: directoryKind(listener, attribution, owner),
			Owner:     owner,
		}
		server.Stop = stopVerdict(server.Directory, owner)
		servers = append(servers, server)
	}
	sort.Slice(servers, func(i, j int) bool {
		if servers[i].Port != servers[j].Port {
			return servers[i].Port < servers[j].Port
		}
		return servers[i].PID < servers[j].PID
	})
	return servers
}

func directoryKind(listener Listener, attribution Attribution, owner Owner) DirectoryKind {
	if _, live := attribution.taskIn(listener.WorkDir); live {
		return DirLiveWorktree
	}
	if taskID, ok := worktreeTaskID(listener.WorkDir); ok {
		if attribution.retired[taskID] {
			return DirRetiredWorktree
		}
		return DirRecordlessWorktree
	}
	if _, checkout := attribution.checkoutIn(listener.WorkDir); checkout {
		return DirCheckout
	}
	if owner.Kind == OwnerProject {
		return DirCheckout
	}
	return DirOther
}

func stopVerdict(kind DirectoryKind, owner Owner) StopVerdict {
	switch {
	case owner.Kind == OwnerGoblin:
		return StopNo
	case kind == DirRecordlessWorktree:
		return StopUnknown
	case kind == DirRetiredWorktree:
		return StopSafe
	default:
		return StopAsk
	}
}

// StopReason explains a verdict in the one line the report prints beside it.
func (s Server) StopReason() string {
	switch s.Stop {
	case StopNo:
		return "task " + s.Owner.TaskID + " is live and working in this directory"
	case StopSafe:
		if s.Owner.TaskID != "" {
			return "task " + s.Owner.TaskID + " is finished; cfo reap retires it"
		}
		return "no task holds this worktree; cfo reap retires it"
	case StopUnknown:
		return "no task record for this worktree could be read, so it is not established that anything here is finished"
	default:
		if s.Owner.Project != "" {
			return "the Overlord's own, in " + s.Owner.Project
		}
		return "nothing in the fleet claims it; ask the Overlord"
	}
}

// buildLimits totals the declared memory ceiling of each running stack.
func buildLimits(stacks []Stack) []StackLimit {
	limits := make([]StackLimit, 0)
	for _, stack := range stacks {
		running := stack.Running()
		if running == 0 {
			continue
		}
		bytes, complete := stack.MemoryLimit()
		limit := StackLimit{
			Stack:      stack.Name,
			Owner:      stack.Owner.Label(),
			Bytes:      bytes,
			Containers: running,
		}
		if !complete {
			for _, container := range stack.Containers {
				if container.Running() && container.MemoryLimitBytes == 0 {
					limit.Undeclared++
				}
			}
		}
		limits = append(limits, limit)
	}
	sort.Slice(limits, func(i, j int) bool {
		if limits[i].Bytes != limits[j].Bytes {
			return limits[i].Bytes > limits[j].Bytes
		}
		return limits[i].Stack < limits[j].Stack
	})
	return limits
}

// ownerRank orders the report so the sections the CFO acts on come first: what
// must not be touched, then what is somebody's, then what nobody is coming
// back for.
func ownerRank(kind OwnerKind) int {
	switch kind {
	case OwnerGoblin:
		return 0
	case OwnerProject:
		return 1
	case OwnerOverlord:
		return 2
	default:
		return 3
	}
}

func sortStacks(stacks []Stack) {
	sort.Slice(stacks, func(i, j int) bool {
		left, right := stacks[i], stacks[j]
		if rank := ownerRank(left.Owner.Kind) - ownerRank(right.Owner.Kind); rank != 0 {
			return rank < 0
		}
		if left.Owner.Label() != right.Owner.Label() {
			return left.Owner.Label() < right.Owner.Label()
		}
		return left.Name < right.Name
	})
}

// Bytes renders a byte count the way a person reads it. Sizes here run from
// a few megabytes to seventy gigabytes, so one decimal at each scale is the
// whole requirement.
func Bytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor, exponent := int64(unit), 0
	for size := value / unit; size >= unit && exponent < 3; size /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %cB", float64(value)/float64(divisor), "KMGT"[exponent])
}

// Percent renders part of whole, guarding the empty reading a missing source
// produces.
func Percent(part, whole int64) string {
	if whole <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(part)/float64(whole))
}

// Truncate bounds an image name or a path to keep one row on one line. The
// full value stays in the JSON projection, so nothing is lost, only folded.
func Truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}
