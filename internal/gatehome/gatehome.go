// Package gatehome finds and places the gate's own home, where no-mistakes
// keeps its database, the repositories it gates and a full checkout for each
// run, on the Dev Drive the fleet's worktrees, scratch and caches moved to.
//
// The home moves whole and NM_HOME names it, never a junction at the old
// path: Go's filepath.EvalSymlinks fails below a junction that crosses to
// another volume, which broke the gate's test preparation when the gate home
// was linked into the Dev Drive by hand on 2026-11-09 (no-mistakes #172).
package gatehome

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/installbase"
)

// Variable names the gate's home to no-mistakes and to every program that
// runs it.
const Variable = "NM_HOME"

// DevDriveFolder is the folder on the Dev Drive the gate's home moves to,
// beside the fleet's own there.
const DevDriveFolder = "no-mistakes"

// Root is the gate's home: NM_HOME in this process, else the user's
// persistent NM_HOME, which a move onto the Dev Drive sets and which a process
// started before the move does not carry, else no-mistakes' own default.
func Root() (string, error) {
	if root := os.Getenv(Variable); root != "" {
		return filepath.Abs(root)
	}
	root, isSet, err := installbase.UserVariable(Variable)
	if err != nil {
		return "", err
	}
	if isSet && root != "" {
		return filepath.Abs(root)
	}
	return Default()
}

// Default is where no-mistakes keeps its home when nothing names one:
// .no-mistakes in the user's profile.
func Default() (string, error) {
	profile, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(profile, ".no-mistakes"), nil
}

// Target is the gate's home on the Dev Drive folder devDrive.
func Target(devDrive string) string {
	return filepath.Join(devDrive, DevDriveFolder)
}

// Place is where the gate's home is against the fleet's Dev Drive folder.
type Place string

const (
	// PlaceNoDevDrive is a fleet whose heavy folders are in its home, so the
	// gate's home has nowhere to go.
	PlaceNoDevDrive Place = "no-dev-drive"
	// PlaceDevDrive is a gate home in the Dev Drive folder.
	PlaceDevDrive Place = "dev-drive"
	// PlaceLinked is a gate home on the system drive that reaches the Dev
	// Drive through junctions, which breaks the gate.
	PlaceLinked Place = "linked"
	// PlaceSystem is a gate home on the system drive.
	PlaceSystem Place = "system"
)

// Placement is where the gate's home is.
type Placement struct {
	Root string
	// DevDrive is the fleet's Dev Drive folder, empty without one.
	DevDrive string
	Place    Place
	// Links are the junctions or links in the home that cross into the Dev
	// Drive, the home's own path among them.
	Links []string
}

// Read reads where the gate's home at root is against the fleet's Dev Drive
// folder devDrive, empty without one.
func Read(root, devDrive string) Placement {
	placement := Placement{Root: root, DevDrive: devDrive}
	switch {
	case devDrive == "":
		placement.Place = PlaceNoDevDrive
		return placement
	case isUnder(root, devDrive):
		placement.Place = PlaceDevDrive
		return placement
	}
	placement.Links = linksInto(root, devDrive)
	placement.Place = PlaceSystem
	if len(placement.Links) > 0 {
		placement.Place = PlaceLinked
	}
	return placement
}

// Line says where the gate's home is, for cfo doctor and cfo dev-drive, and
// warns when the fleet's heavy folders are on a Dev Drive while the gate's are
// not, or reach it through junctions.
func (p Placement) Line() string {
	move := fmt.Sprintf("cfo dev-drive move --to %s moves it to %s and names it with NM_HOME once no gate run is in flight", p.DevDrive, Target(p.DevDrive))
	switch p.Place {
	case PlaceNoDevDrive:
		return p.Root + ", where no-mistakes keeps it, as the fleet has no Dev Drive"
	case PlaceDevDrive:
		return p.Root + ", on the Dev Drive, named by NM_HOME"
	case PlaceLinked:
		return fmt.Sprintf("WARNING: %s reaches the Dev Drive through junctions (%s), below which Go cannot resolve a path, so gate test preparation fails; %s", p.Root, strings.Join(p.Links, ", "), move)
	}
	return fmt.Sprintf("WARNING: %s is not on the Dev Drive the fleet's worktrees, scratch and caches use, so every gate checkout lands where Defender scans it as it opens; %s", p.Root, move)
}

// linksInto are root itself, its entries and their entries when they are
// links into devDrive, as a move by hand made them.
func linksInto(root, devDrive string) []string {
	var links []string
	if target, isLink := linkTarget(root); isLink {
		if isUnder(target, devDrive) {
			links = append(links, root)
		}
		return links
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if target, isLink := linkTarget(path); isLink {
			if isUnder(target, devDrive) {
				links = append(links, path)
			}
			continue
		}
		if !entry.IsDir() {
			continue
		}
		children, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, child := range children {
			if target, isLink := linkTarget(filepath.Join(path, child.Name())); isLink && isUnder(target, devDrive) {
				links = append(links, filepath.Join(path, child.Name()))
			}
		}
	}
	return links
}

// linkTarget is the folder path links to, and whether it is a link at all.
func linkTarget(path string) (string, bool) {
	target, err := os.Readlink(path)
	if err != nil {
		return "", false
	}
	return target, true
}

func isUnder(path, folder string) bool {
	rel, err := filepath.Rel(folder, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}
