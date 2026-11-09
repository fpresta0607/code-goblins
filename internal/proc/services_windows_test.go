package proc

import (
	"slices"
	"testing"
	"time"
)

// A job's processes are judged a machine service's only by a service among
// them. A gate agent under the no-mistakes daemon runs ceo gate prepush, and
// a check of it runs in a job of its own: the daemon above the check made
// every process of the check read as the daemon's, so the end of the check
// at its time limit spared it all. A daemon started in the job, as a goblin's
// terminal starts one, is still spared there with what runs under it.
func TestServicesAmongAJobsProcessesCountsOnlyAServiceInTheJob(t *testing.T) {
	// Arrange
	at := time.Date(2026, 10, 12, 19, 0, 0, 0, time.UTC)
	later := func(seconds int) time.Time { return at.Add(time.Duration(seconds) * time.Second) }
	running := []ServiceProcess{
		{PID: 10, ParentPID: 1, ExeBase: "no-mistakes.exe", Arguments: []string{`C:\tools\no-mistakes.exe`, "daemon", "run"}, Start: later(0)},
		{PID: 11, ParentPID: 10, ExeBase: "ceo.exe", Arguments: []string{"ceo.exe", "gate", "prepush"}, Start: later(1)},
		{PID: 12, ParentPID: 11, ExeBase: "go.exe", Start: later(2)},
		{PID: 13, ParentPID: 12, ExeBase: "supervisor.test.exe", Start: later(3)},
		{PID: 20, ParentPID: 1, ExeBase: "claude.exe", Start: later(4)},
		{PID: 21, ParentPID: 20, ExeBase: "no-mistakes.exe", Arguments: []string{`C:\tools\no-mistakes.exe`, "daemon", "run"}, Start: later(5)},
		{PID: 22, ParentPID: 21, ExeBase: "claude.exe", Start: later(6)},
	}
	entry := func(pid int) Entry {
		for _, process := range running {
			if process.PID == pid {
				return Entry{PID: pid, ParentPID: process.ParentPID, ExeBase: process.ExeBase, Start: process.Start}
			}
		}
		t.Fatalf("no process %d", pid)
		return Entry{}
	}
	for _, c := range []struct {
		name    string
		members []Entry
		want    map[int]Service
	}{
		{name: "a check's job under the daemon", members: []Entry{entry(12), entry(13)}, want: map[int]Service{}},
		{name: "a terminal's job with a daemon started in it", members: []Entry{entry(20), entry(21), entry(22)}, want: map[int]Service{21: GateDaemon, 22: GateDaemon}},
		{name: "a member whose pid now names another process", members: []Entry{{PID: 21, ExeBase: "no-mistakes.exe", Start: later(99)}, entry(22)}, want: map[int]Service{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Act
			services := servicesAmong(running, c.members)

			// Assert
			for _, member := range c.members {
				if got := services[member.PID]; got != c.want[member.PID] {
					t.Errorf("%s pid %d is service %d, want %d", member.ExeBase, member.PID, got, c.want[member.PID])
				}
			}
		})
	}
}

// A process tree is judged a machine service's only by a service among its
// own processes, as a job's are. A tree read under the Scrawl server is not
// the server's, so ceo reap --force still ends it, while a Docker Desktop
// started in the tree is spared there with what runs under it.
func TestTreeOfCountsOnlyAServiceInTheTree(t *testing.T) {
	// Arrange
	at := time.Date(2026, 10, 12, 19, 0, 0, 0, time.UTC)
	later := func(seconds int) time.Time { return at.Add(time.Duration(seconds) * time.Second) }
	running := []ServiceProcess{
		{PID: 10, ParentPID: 1, ExeBase: "node.exe", Arguments: []string{`C:\tools\node.exe`, `C:\Users\o\AppData\Roaming\npm\node_modules\lavish-axi\dist\server.mjs`, "server"}, Start: later(0)},
		{PID: 11, ParentPID: 10, ExeBase: "ceo.exe", Start: later(1)},
		{PID: 12, ParentPID: 11, ExeBase: "ceo.test.exe", Start: later(2)},
		{PID: 20, ParentPID: 11, ExeBase: "Docker Desktop.exe", Start: later(3)},
		{PID: 21, ParentPID: 20, ExeBase: "ceo.test.exe", Start: later(4)},
	}
	for _, c := range []struct {
		name string
		pid  int
		pids []int
		want map[int]Service
	}{
		{name: "a reap under the Scrawl server", pid: 11, pids: []int{11, 12, 20, 21}, want: map[int]Service{20: DockerDesktop, 21: DockerDesktop}},
		{name: "the Scrawl server's own tree", pid: 10, pids: []int{10, 11, 12, 20, 21}, want: map[int]Service{10: PageServer, 11: PageServer, 12: PageServer, 20: DockerDesktop, 21: DockerDesktop}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Act
			tree, services, err := treeOf(running, c.pid)

			// Assert
			if err != nil {
				t.Fatalf("treeOf: %v", err)
			}
			var pids []int
			for _, process := range tree {
				pids = append(pids, process.PID)
			}
			if !slices.Equal(pids, c.pids) {
				t.Fatalf("tree holds %v, want %v", pids, c.pids)
			}
			for _, process := range tree {
				if got := services[process.PID]; got != c.want[process.PID] {
					t.Errorf("%s pid %d is service %d, want %d", process.ExeBase, process.PID, got, c.want[process.PID])
				}
			}
			if len(services) != len(c.want) {
				t.Errorf("services = %v, want only the tree's own", services)
			}
		})
	}
}
