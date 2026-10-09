// Package devdrive reads whether this machine can have a Windows Dev Drive,
// which of its drives are Dev Drives and whether Windows trusts them, all
// without administrator rights, and says what that means for a home.
//
// A Dev Drive is a ReFS volume Windows 11 formats for developer work.
// Microsoft Defender keeps scanning it, in performance mode: a file opens now
// and is scanned just after, instead of the open waiting for the scan. It is
// not a Defender exclusion, and nothing here adds one or changes a Defender
// setting. Performance mode needs the drive trusted and Defender's real-time
// protection on.
package devdrive

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// Trust is whether Windows trusts a Dev Drive, which is what turns Defender's
// performance mode on there.
type Trust string

const (
	Trusted   Trust = "trusted"
	Untrusted Trust = "untrusted"
	// TrustUnknown is a Dev Drive whose trust Windows did not say to a
	// process without administrator rights; fsutil devdrv query, run as
	// administrator, says it.
	TrustUnknown Trust = "unknown"
)

// Volume is one fixed drive with a letter.
type Volume struct {
	// Root is the drive's root folder, such as D:\.
	Root       string
	Label      string
	FileSystem string
	// Dev is a volume formatted as a Dev Drive.
	Dev   bool
	Trust Trust
	Free  uint64
	Total uint64
}

// Letter is the volume's drive, such as D:.
func (v Volume) Letter() string { return filepath.VolumeName(v.Root) }

// Machine is what this machine says about Dev Drives.
type Machine struct {
	// Build and Revision are Windows' CurrentBuildNumber and UBR.
	Build, Revision int
	// Defender is whether Microsoft Defender's real-time protection is on;
	// DefenderUnread says why it could not be read.
	Defender       bool
	DefenderUnread string
	// SystemDrive is the drive Windows runs from, such as C:, and SystemFree
	// its free space: a new Dev Drive's file goes there.
	SystemDrive string
	SystemFree  uint64
	Volumes     []Volume
	// Letters are the drive letters in use, bit 0 for A:, every kind of
	// drive counted.
	Letters uint32
}

// The first Windows with Dev Drives is Windows 11 build 22621.2338; every
// edition has them.
const (
	firstBuild    = 22621
	firstRevision = 2338
)

// MinimumFree is the free space on the system drive a new Dev Drive needs: its
// 50 GB minimum size and as much again to grow into.
const MinimumFree = 100 << 30

// Unavailable says why this machine can have no Dev Drive Code Goblins would
// use, or "" when it can.
func (m Machine) Unavailable() string {
	switch {
	case m.Build < firstBuild || m.Build == firstBuild && m.Revision < firstRevision:
		return fmt.Sprintf("this Windows (build %d.%d) is older than Windows 11 build %d.%d, the first with Dev Drives", m.Build, m.Revision, firstBuild, firstRevision)
	case m.DefenderUnread != "":
		return "Microsoft Defender's real-time protection could not be read (" + m.DefenderUnread + "), and a Dev Drive's performance mode needs it on"
	case !m.Defender:
		return "Microsoft Defender's real-time protection is off, and a Dev Drive's performance mode needs it on"
	}
	return ""
}

// CannotCreate says why a new Dev Drive cannot be made here, or "" when it
// can: everything Unavailable checks, and room on the system drive.
func (m Machine) CannotCreate() string {
	if reason := m.Unavailable(); reason != "" {
		return reason
	}
	if m.SystemFree < MinimumFree {
		return fmt.Sprintf("%s has %.0f GB free, under the %d GB a new Dev Drive needs", m.SystemDrive, disk.GB(m.SystemFree), MinimumFree>>30)
	}
	return ""
}

// Reusable is the Dev Drive already on this machine that Code Goblins would
// use: a trusted one first, then the one with the most free space.
func (m Machine) Reusable() (Volume, bool) {
	var best Volume
	found := false
	for _, v := range m.Volumes {
		switch {
		case !v.Dev:
		case !found, v.Trust == Trusted && best.Trust != Trusted:
			best, found = v, true
		case (v.Trust == Trusted) == (best.Trust == Trusted) && v.Free > best.Free:
			best = v
		}
	}
	return best, found
}

// VolumeOf finds the volume holding path.
func (m Machine) VolumeOf(path string) (Volume, bool) {
	letter := filepath.VolumeName(filepath.Clean(path))
	for _, v := range m.Volumes {
		if letter != "" && strings.EqualFold(v.Letter(), letter) {
			return v, true
		}
	}
	return Volume{}, false
}

// SizeGB is the size of a new Dev Drive: 200 GB, or less where the system
// drive has less room, keeping 50 GB of it free, in whole tens of GB and never
// under the 50 GB minimum. Its file grows only as files land on the drive.
func (m Machine) SizeGB() int {
	size := (int(m.SystemFree>>30) - 50) / 10 * 10
	return max(50, min(200, size))
}

// Folder is where a home's heavy folders go on a drive: CodeGoblins at its
// root.
func Folder(v Volume) string { return filepath.Join(v.Root, "CodeGoblins") }

// State is what doctor and the board say about a home's Dev Drive.
type State string

const (
	// StateUnavailable is a machine that can have no Dev Drive Code Goblins
	// would use; it keeps working exactly as it always has.
	StateUnavailable State = "unavailable"
	// StateAbsent is a machine with no Dev Drive that can have one.
	StateAbsent State = "absent"
	// StatePresent is a Dev Drive on the machine the home does not use yet.
	StatePresent State = "present"
	// StateOn is a home whose heavy folders are on a Dev Drive.
	StateOn State = "on"
	// StateMissing is a home whose Dev Drive folder cannot be found, such
	// as a drive whose disk is not attached.
	StateMissing State = "missing"
)

// Report is a home's Dev Drive state, the volume it concerns, one line
// saying it, with the fix when there is one, and the short note the board
// shows under its Dev Drive row.
type Report struct {
	State  State
	Volume Volume
	Line   string
	Note   string
}

// Untrusted is a report on a Dev Drive Windows does not trust or did not say
// it trusts, which runs without performance mode.
func (r Report) Untrusted() bool {
	return (r.State == StatePresent || r.State == StateOn) && r.Volume.Trust != Trusted
}

// BoardSteps says where a person sets a Dev Drive up: every step is a
// Command Center item, made once the board's setting is pressed.
const BoardSteps = "Set up under Dev Drive in the board's Workspace panel puts each step in the Command Center"

// Explain is the sentence every surface uses to say what a Dev Drive is.
const Explain = "A Dev Drive is a drive Windows 11 formats for developer work: Microsoft Defender keeps scanning it, in performance mode, so opening a file no longer waits for the scan. It is not a Defender exclusion."

// Describe reports h's Dev Drive on machine m.
func Describe(m Machine, h home.Home) Report {
	if h.DevDrive != "" {
		v, found := m.VolumeOf(h.DevDrive)
		switch {
		case !found:
			return Report{State: StateMissing, Line: fmt.Sprintf("the home's worktrees, scratch and caches are set to %s, but %s is not there, so no goblin starts; the Attach item in the Command Center attaches it again", h.DevDrive, filepath.VolumeName(h.DevDrive)),
				Note: filepath.VolumeName(h.DevDrive) + " is not attached, so no goblin starts."}
		case !v.Dev:
			return Report{State: StateOn, Volume: v, Line: fmt.Sprintf("the home's worktrees, scratch and caches are on %s, but %s is not a Dev Drive, so Defender scans it as any drive", h.DevDrive, v.Letter()),
				Note: "The worktrees and caches are on " + v.Letter() + ", which is not a Dev Drive."}
		}
		report := Report{State: StateOn, Volume: v, Line: fmt.Sprintf("on: the home's worktrees, scratch and caches are on %s, %s", h.DevDrive, trustPhrase(v)), Note: "The worktrees and caches are on " + h.DevDrive + "."}
		if v.Trust == Untrusted {
			report.Line += "; " + BoardSteps
			report.Note = "Windows does not trust " + v.Letter() + " yet, so performance mode is off."
		}
		return report
	}
	if v, found := m.Reusable(); found {
		if reason := m.Unavailable(); reason != "" {
			return Report{State: StateUnavailable, Volume: v, Line: fmt.Sprintf("not used: %s is a Dev Drive, but %s; Code Goblins keeps its worktrees and caches in the home, as always", v.Letter(), reason),
				Note: v.Letter() + " is not used because " + reason + "."}
		}
		return Report{State: StatePresent, Volume: v, Line: fmt.Sprintf("present, not used yet: %s is %s; the home's worktrees, scratch and caches can move to %s; %s", v.Letter(), trustPhrase(v), Folder(v), BoardSteps),
			Note: v.Letter() + " is a Dev Drive the worktrees and caches can move to."}
	}
	if reason := m.CannotCreate(); reason != "" {
		return Report{State: StateUnavailable, Line: "not available here: " + reason + "; Code Goblins keeps its worktrees and caches in the home, as always", Note: "Not available here because " + reason + "."}
	}
	return Report{State: StateAbsent, Line: fmt.Sprintf("absent: this machine can have one (%s), and it is optional; %s", strings.TrimSuffix(Explain, "."), BoardSteps),
		Note: "An optional drive that Defender scans in performance mode, so files open faster."}
}

func trustPhrase(v Volume) string {
	switch v.Trust {
	case Trusted:
		return "a trusted Dev Drive, which Defender scans in performance mode"
	case Untrusted:
		return "a Dev Drive Windows does not trust, so Defender scans it in real-time mode and the speed is lost until it is trusted"
	}
	return "a Dev Drive whose trust Windows does not tell a process without administrator rights; fsutil devdrv query, run as administrator, says it"
}
