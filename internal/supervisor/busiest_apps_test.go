package supervisor

import (
	"slices"
	"testing"
	"time"
)

// seconds is processor time in the process list's units of 100 ns.
func seconds(count float64) uint64 { return uint64(count * 1e7) }

// On 2026-10-09 a WMI service and Defender kept the Overlord's efficiency
// cores full all day and nothing on the board said who. The CPU meter's tip
// names the apps that used the most processor over the reading, an app being
// a first process and everything it started as in the commit holders, most
// first, and names none that used under a quarter of one core.
func TestBusiestAppsNamesTheAppsThatUsedTheMostProcessor(t *testing.T) {
	services := processCommit{pid: 800, parent: 4, name: "services.exe", created: 1}
	host := func(cpu uint64) processCommit {
		return processCommit{pid: 900, parent: 800, name: "svchost.exe", created: 2, cpu: cpu}
	}
	for _, test := range []struct {
		name          string
		before, after []processCommit
		want          []string
	}{
		{
			name: "two services and an idle app",
			before: []processCommit{services, host(0),
				{pid: 5200, parent: 900, name: "WmiPrvSE.exe", created: 3, cpu: seconds(100)},
				{pid: 6112, parent: 800, name: "MsMpEng.exe", created: 3, cpu: seconds(50)},
				{pid: 7000, parent: 900, name: "Notepad.exe", created: 4, cpu: seconds(9)},
			},
			after: []processCommit{services, host(0),
				{pid: 5200, parent: 900, name: "WmiPrvSE.exe", created: 3, cpu: seconds(178)},
				{pid: 6112, parent: 800, name: "MsMpEng.exe", created: 3, cpu: seconds(101)},
				{pid: 7000, parent: 900, name: "Notepad.exe", created: 4, cpu: seconds(10)},
			},
			want: []string{"WmiPrvSE", "MsMpEng"},
		},
		{
			name: "a goblin's build counts as the fleet, with a compiler that started since and one that ended",
			before: []processCommit{services, host(0),
				{pid: 100, parent: 900, name: "goblins.exe", created: 5, cpu: seconds(1)},
				{pid: 110, parent: 100, name: "go.exe", created: 6, cpu: seconds(2)},
				{pid: 111, parent: 110, name: "compile.exe", created: 7, cpu: seconds(40)},
				{pid: 6112, parent: 800, name: "MsMpEng.exe", created: 3, cpu: seconds(50)},
			},
			after: []processCommit{services, host(0),
				{pid: 100, parent: 900, name: "goblins.exe", created: 5, cpu: seconds(1)},
				{pid: 110, parent: 100, name: "go.exe", created: 6, cpu: seconds(12)},
				{pid: 112, parent: 110, name: "compile.exe", created: 9, cpu: seconds(170)},
				{pid: 6112, parent: 800, name: "MsMpEng.exe", created: 3, cpu: seconds(80)},
			},
			want: []string{"goblins", "MsMpEng"},
		},
		{
			name: "a process that took an ended one's number is new, not the old one's use run backwards",
			before: []processCommit{services, host(0),
				{pid: 5200, parent: 900, name: "WmiPrvSE.exe", created: 3, cpu: seconds(500)},
			},
			after: []processCommit{services, host(0),
				{pid: 5200, parent: 900, name: "Notepad.exe", created: 8, cpu: seconds(30)},
			},
			want: []string{"Notepad"},
		},
		{
			name: "three busy apps name the two busiest",
			before: []processCommit{services, host(0),
				{pid: 1, parent: 900, name: "a.exe", created: 3},
				{pid: 2, parent: 900, name: "b.exe", created: 3},
				{pid: 3, parent: 900, name: "c.exe", created: 3},
			},
			after: []processCommit{services, host(0),
				{pid: 1, parent: 900, name: "a.exe", created: 3, cpu: seconds(30)},
				{pid: 2, parent: 900, name: "b.exe", created: 3, cpu: seconds(90)},
				{pid: 3, parent: 900, name: "c.exe", created: 3, cpu: seconds(60)},
			},
			want: []string{"b", "c"},
		},
		{
			name:   "nothing busy names nobody",
			before: []processCommit{services, host(0), {pid: 7000, parent: 900, name: "Notepad.exe", created: 4, cpu: seconds(9)}},
			after:  []processCommit{services, host(seconds(3)), {pid: 7000, parent: 900, name: "Notepad.exe", created: 4, cpu: seconds(20)}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := busiestApps(test.before, test.after, time.Minute)

			// Assert
			if !slices.Equal(got, test.want) {
				t.Errorf("busiestApps = %v, want %v", got, test.want)
			}
		})
	}
}

// No time between two readings says nothing of who was busy.
func TestBusiestAppsNamesNobodyOverNoTime(t *testing.T) {
	// Arrange
	process := []processCommit{{pid: 1, parent: 0, name: "a.exe", created: 3, cpu: seconds(90)}}

	// Act
	got := busiestApps(nil, process, 0)

	// Assert
	if len(got) != 0 {
		t.Errorf("busiestApps = %v, want nobody", got)
	}
}
