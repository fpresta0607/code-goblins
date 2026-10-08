package devdrive

import (
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

const gigabyte = 1 << 30

// ready is a machine that can have a Dev Drive and has none.
func ready() Machine {
	return Machine{Build: 26200, Revision: 9457, Defender: true, SystemDrive: "C:", SystemFree: 374 * gigabyte, Volumes: []Volume{{Root: `C:\`, FileSystem: "NTFS", Free: 374 * gigabyte, Total: 924 * gigabyte}}}
}

func TestAMachineThatCannotHaveADevDriveIsToldWhy(t *testing.T) {
	for name, test := range map[string]struct {
		change func(*Machine)
		says   string
	}{
		"Windows 10":                          {func(m *Machine) { m.Build, m.Revision = 19045, 5000 }, "older than Windows 11 build 22621.2338"},
		"Windows 11 before the first release": {func(m *Machine) { m.Build, m.Revision = 22621, 2337 }, "older than Windows 11 build 22621.2338"},
		"Defender's real-time protection off": {func(m *Machine) { m.Defender = false }, "real-time protection is off"},
		"Defender unreadable":                 {func(m *Machine) { m.Defender, m.DefenderUnread = false, "Get-MpComputerStatus: not found" }, "could not be read (Get-MpComputerStatus: not found)"},
		"too little room on the system drive": {func(m *Machine) { m.SystemFree = 99 * gigabyte }, "C: has 99 GB free, under the 100 GB"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			m := ready()
			test.change(&m)

			// Act
			report := Describe(m, home.Home{Root: `C:\Users\op\AppData\Local\CodeGoblins`})

			// Assert
			if report.State != StateUnavailable || !strings.Contains(report.Line, test.says) || !strings.Contains(report.Line, "keeps its worktrees and caches in the home, as always") {
				t.Errorf("report = %+v, want unavailable saying %q and that nothing changes", report, test.says)
			}
		})
	}
}

func TestAMachineThatCanHaveOneHearsItIsOptionalAndNoExclusion(t *testing.T) {
	report := Describe(ready(), home.Home{Root: `C:\Users\op\AppData\Local\CodeGoblins`})
	if report.State != StateAbsent || !strings.Contains(report.Line, "optional") || !strings.Contains(report.Line, "not a Defender exclusion") {
		t.Errorf("report = %+v, want absent, optional and not an exclusion", report)
	}
}

func TestAnExistingDevDriveIsReusedTrustedFirstThenTheRoomiest(t *testing.T) {
	// Arrange
	m := ready()
	m.SystemFree = 20 * gigabyte
	m.Volumes = append(m.Volumes,
		Volume{Root: `E:\`, FileSystem: "ReFS", Dev: true, Trust: Untrusted, Free: 400 * gigabyte},
		Volume{Root: `F:\`, FileSystem: "ReFS", Dev: true, Trust: Trusted, Free: 60 * gigabyte},
		Volume{Root: `G:\`, FileSystem: "ReFS", Dev: true, Trust: Trusted, Free: 90 * gigabyte},
		Volume{Root: `H:\`, FileSystem: "ReFS", Free: 900 * gigabyte})

	// Act
	report := Describe(m, home.Home{Root: `C:\Users\op\AppData\Local\CodeGoblins`})

	// Assert: room on C: does not matter when no new drive is made.
	if report.State != StatePresent || report.Volume.Root != `G:\` || report.Untrusted() || !strings.Contains(report.Line, `G:\CodeGoblins`) {
		t.Errorf("report = %+v, want G:, the roomiest trusted Dev Drive", report)
	}
	m.Volumes = m.Volumes[:2]
	if report := Describe(m, home.Home{}); report.Volume.Root != `E:\` || !report.Untrusted() || !strings.Contains(report.Line, "does not trust") {
		t.Errorf("report = %+v, want the untrusted E: named as untrusted", report)
	}
}

func TestAHomeOnItsDevDriveIsOnOrMissing(t *testing.T) {
	m := ready()
	m.Volumes = append(m.Volumes, Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: Trusted, Free: 190 * gigabyte})
	moved := home.Home{Root: `C:\Users\op\AppData\Local\CodeGoblins`, DevDrive: `D:\CodeGoblins`}
	for name, test := range map[string]struct {
		home  home.Home
		state State
		says  string
	}{
		"on a trusted Dev Drive": {moved, StateOn, "performance mode"},
		"its drive not attached": {home.Home{Root: moved.Root, DevDrive: `E:\CodeGoblins`}, StateMissing, "no goblin starts"},
		"on a plain drive":       {home.Home{Root: moved.Root, DevDrive: `C:\CodeGoblins`}, StateOn, "not a Dev Drive"},
	} {
		t.Run(name, func(t *testing.T) {
			if report := Describe(m, test.home); report.State != test.state || !strings.Contains(report.Line, test.says) {
				t.Errorf("report = %+v, want %s saying %q", report, test.state, test.says)
			}
		})
	}
}

func TestANewDevDriveIsSizedToLeaveTheSystemDriveRoom(t *testing.T) {
	for free, want := range map[uint64]int{374 * gigabyte: 200, 250 * gigabyte: 200, 180 * gigabyte: 130, 100 * gigabyte: 50, 60 * gigabyte: 50} {
		m := ready()
		m.SystemFree = free
		if got := m.SizeGB(); got != want {
			t.Errorf("SizeGB with %d GB free = %d, want %d", free/gigabyte, got, want)
		}
	}
}
