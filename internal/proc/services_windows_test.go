package proc

import (
	"testing"
	"time"
)

// A job's processes are judged a machine service's only by a service among
// them. A gate agent under the no-mistakes daemon runs cfo gate prepush, and
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
		{PID: 11, ParentPID: 10, ExeBase: "cfo.exe", Arguments: []string{"cfo.exe", "gate", "prepush"}, Start: later(1)},
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
