package devdrive

import (
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

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
	report := Describe(m, home.Home{Root: `C:\Users\op\AppData\Local\CodeGoblins`}, "")

	// Assert: room on C: does not matter when no new drive is made.
	if report.State != StatePresent || report.Volume.Root != `G:\` || report.Untrusted() || !strings.Contains(report.Line, `G:\CodeGoblins`) {
		t.Errorf("report = %+v, want G:, the roomiest trusted Dev Drive", report)
	}
	m.Volumes = m.Volumes[:2]
	if report := Describe(m, home.Home{}, ""); report.Volume.Root != `E:\` || !report.Untrusted() || !strings.Contains(report.Line, "does not trust") {
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
			if report := Describe(m, test.home, ""); report.State != test.state || !strings.Contains(report.Line, test.says) {
				t.Errorf("report = %+v, want %s saying %q", report, test.state, test.says)
			}
		})
	}
}

// The board's Workspace panel says the Dev Drive in one short note under the
// row's name and button (the Overlord, 2026-10-08: "text can be note below
// it"): the doctor's line, with its status word and its semicolons, and the
// sentence saying what a Dev Drive is a second time are not for the board.
func TestEveryStateHasOneShortNoteForTheBoard(t *testing.T) {
	drive := func(trust Trust) Volume {
		return Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: trust, Free: 190 * gigabyte}
	}
	with := func(change func(*Machine), volumes ...Volume) Machine {
		m := ready()
		m.Volumes = append(m.Volumes, volumes...)
		if change != nil {
			change(&m)
		}
		return m
	}
	root := `C:\Users\op\AppData\Local\CodeGoblins`
	moved := home.Home{Root: root, DevDrive: `D:\CodeGoblins`}
	for name, test := range map[string]struct {
		machine Machine
		home    home.Home
		note    string
	}{
		"none yet":                  {with(nil), home.Home{Root: root}, "An optional drive that Defender scans in performance mode, so files open faster."},
		"one here, not used yet":    {with(nil, drive(Trusted)), home.Home{Root: root}, "D: is a Dev Drive the worktrees and caches can move to."},
		"on a trusted Dev Drive":    {with(nil, drive(Trusted)), moved, `The worktrees and caches are on D:\CodeGoblins.`},
		"on one of unknown trust":   {with(nil, drive(TrustUnknown)), moved, `The worktrees and caches are on D:\CodeGoblins.`},
		"on an untrusted Dev Drive": {with(nil, drive(Untrusted)), moved, "Windows does not trust D: yet, so performance mode is off."},
		"on a plain drive":          {with(nil), home.Home{Root: root, DevDrive: `C:\CodeGoblins`}, "The worktrees and caches are on C:, which is not a Dev Drive."},
		"its drive not attached":    {with(nil), moved, "D: is not attached, so no goblin starts."},
		"none can be made":          {with(func(m *Machine) { m.SystemFree = 99 * gigabyte }), home.Home{Root: root}, "Not available here because C: has 99 GB free, under the 100 GB a new Dev Drive needs."},
		"one here it cannot use":    {with(func(m *Machine) { m.Defender = false }, drive(Trusted)), home.Home{Root: root}, "D: is not used because Microsoft Defender's real-time protection is off, and a Dev Drive's performance mode needs it on."},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			report := Describe(test.machine, test.home, "")

			// Assert
			if report.Note != test.note {
				t.Errorf("note = %q, want %q", report.Note, test.note)
			}
			if strings.ContainsAny(report.Note, ";\u2014") || strings.Contains(report.Note, "exclusion") {
				t.Errorf("note %q has a semicolon, a dash or the explanation again", report.Note)
			}
		})
	}
}

// A Dev Drive in a file says how much of the system drive the file holds and
// that space goblins free on the Dev Drive stays taken there, or why that
// could not be read, in one line, and the board's note says the same.
func TestADevDriveInAFileSaysHowMuchOfItsDriveTheFileHolds(t *testing.T) {
	vhd := `C:\DevDrives\CodeGoblins.vhdx`
	for name, test := range map[string]struct {
		image Image
		file  string
	}{
		"its size read":        {Image{Path: vhd, Size: 142 * gigabyte / 10}, `C:\DevDrives\CodeGoblins.vhdx holds 14.2 GB of C:, and stays that size when goblins free space on D:`},
		"its size unread":      {Image{Path: vhd, Unread: "only a process run as administrator may read it"}, `C:\DevDrives\CodeGoblins.vhdx holds space on C: that stays taken when goblins free space on D:, and how much could not be read: only a process run as administrator may read it`},
		"its file unnamed":     {Image{Unread: "The device is not ready"}, "whether D: lives in a file on another drive could not be read: The device is not ready"},
		"on a disk of its own": {Image{}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			m := ready()
			m.Volumes = append(m.Volumes, Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: Trusted, Free: 188 * gigabyte, Total: 200 * gigabyte, Image: test.image})
			moved := home.Home{Root: `C:\Users\op\AppData\Local\CodeGoblins`, DevDrive: `D:\CodeGoblins`}

			// Act
			report := Describe(m, moved, "")

			// Assert
			note := `The worktrees and caches are on D:\CodeGoblins.`
			if test.file != "" {
				note += " " + strings.ToUpper(test.file[:1]) + test.file[1:] + "."
			}
			if report.File != test.file || report.Note != note {
				t.Errorf("file = %q, note = %q, want %q and %q", report.File, report.Note, test.file, note)
			}
			if strings.ContainsAny(report.File, ";\u2014") {
				t.Errorf("file %q has a semicolon or a dash", report.File)
			}
		})
	}
}
