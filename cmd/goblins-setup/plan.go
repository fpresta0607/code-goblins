package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
)

// Plan is what the window says before anything changes: the folder Code
// Goblins goes into, whether it is there already, a sentence about it, and
// whether Start at login is ticked, as it is unless the home it updates had
// it turned off, and the Dev Drive offer.
type Plan struct {
	Folder       string `json:"folder"`
	Update       bool   `json:"update"`
	Note         string `json:"note"`
	StartAtLogin bool   `json:"start_at_login"`
	// DevDriveOffer shows the Dev Drive box, unticked: this machine can have
	// a Dev Drive or has one the home does not use, and the home holds no
	// answer yet. DevDriveNote says instead, once, why this machine can
	// have none. DevDriveExplain is the sentence saying what one is.
	DevDriveOffer   bool   `json:"dev_drive_offer"`
	DevDriveNote    string `json:"dev_drive_note"`
	DevDriveExplain string `json:"dev_drive_explain"`
}

// findPlan is the plan for this machine.
func findPlan() (Plan, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return Plan{}, fmt.Errorf("LOCALAPPDATA is not set, so there is no folder to install Code Goblins in")
	}
	target, err := install.FindTarget(install.NewEnvStore(execx.OSRunner{}), filepath.Join(local, "CodeGoblins"))
	if err != nil {
		return Plan{}, err
	}
	choice, err := install.ReadStartAtLogin(filepath.Join(target.Root, "state"))
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Folder: target.Root, Update: target.Update, StartAtLogin: choice != install.StartAtLoginOff}
	if target.Kept {
		plan.Note = "You already run Code Goblins from this folder, so it is updated there and your goblins and their work stay as they are."
	}
	config, err := home.ReadDevDriveConfig(target.Root)
	if err != nil {
		return Plan{}, err
	}
	// A machine whose Dev Drive cannot be read gets no offer, as one that
	// cannot have one would, and the install goes on.
	if machine, err := devdrive.Read(context.Background(), execx.OSRunner{}); err == nil {
		plan.DevDriveOffer, plan.DevDriveNote = devDriveOffer(machine, config, target.Root)
		plan.DevDriveExplain = devdrive.Explain
	}
	return plan, nil
}

// devDriveOffer is what the setup says of a Dev Drive for the home at root:
// the box where this machine can have one or has one unused and the home holds
// no answer, or the line saying why it can have none.
func devDriveOffer(machine devdrive.Machine, config home.DevDriveConfig, root string) (bool, string) {
	if config.Choice != "" || config.Root != "" {
		return false, ""
	}
	report := devdrive.Describe(machine, home.Home{Root: root})
	switch report.State {
	case devdrive.StateAbsent, devdrive.StatePresent:
		return true, ""
	case devdrive.StateUnavailable:
		return false, strings.ToUpper(report.Line[:1]) + report.Line[1:]
	}
	return false, ""
}
