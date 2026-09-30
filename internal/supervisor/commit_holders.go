package supervisor

import (
	"cmp"
	"slices"
	"strings"
)

// commitHolderCount is how many apps the meter names when commit is short.
const commitHolderCount = 3

// CommitHolder is one app's commit: the private memory of the app's first
// process and every process it started, which the app's name stands for.
type CommitHolder struct {
	Name      string `json:"name"`
	Commit    uint64 `json:"commit"`
	Processes int    `json:"processes"`
}

// processCommit is one process's commit, with what ties it to its parent.
type processCommit struct {
	pid, parent uint32
	name        string
	// created is the process's creation time, in the units the system
	// process list gives, which only compare.
	created int64
	commit  uint64
}

// shellHosts start apps without being part of them: the Windows shell and
// service hosts, which every app a person opens or a service runs hangs from.
var shellHosts = []string{"system", "registry", "smss.exe", "csrss.exe", "wininit.exe", "winlogon.exe", "services.exe", "svchost.exe", "explorer.exe", "sihost.exe", "userinit.exe"}

// topCommitHolders groups processes by app, the ancestor each descends from
// just under a shell host or where its parent is gone, so the Codex app's
// MCP servers count as the Codex app rather than as python; apps of one name
// count as one. It names the count apps holding the most commit, most first.
func topCommitHolders(processes []processCommit, count int) []CommitHolder {
	byPID := make(map[uint32]processCommit, len(processes))
	for _, process := range processes {
		byPID[process.pid] = process
	}
	app := func(process processCommit) string {
		for range len(processes) {
			parent, ok := byPID[process.parent]
			// A parent created after the process is another process that
			// took its parent's reused ID, and the nameless Idle process
			// starts nothing.
			if !ok || parent.name == "" || parent.pid == process.pid || parent.created > process.created || slices.Contains(shellHosts, strings.ToLower(parent.name)) {
				break
			}
			process = parent
		}
		return process.name
	}
	groups := map[string]*CommitHolder{}
	for _, process := range processes {
		if process.name == "" {
			continue
		}
		name := app(process)
		if strings.HasSuffix(strings.ToLower(name), ".exe") {
			name = name[:len(name)-len(".exe")]
		}
		key := strings.ToLower(name)
		if groups[key] == nil {
			groups[key] = &CommitHolder{Name: name}
		}
		groups[key].Commit += process.commit
		groups[key].Processes++
	}
	holders := make([]CommitHolder, 0, len(groups))
	for _, holder := range groups {
		holders = append(holders, *holder)
	}
	slices.SortFunc(holders, func(a, b CommitHolder) int {
		return cmp.Or(cmp.Compare(b.Commit, a.Commit), strings.Compare(a.Name, b.Name))
	})
	return holders[:min(count, len(holders))]
}
