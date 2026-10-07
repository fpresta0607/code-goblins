package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/install"
)

// Plan is what the window says before anything changes: the folder Code
// Goblins goes into, whether it is there already, a sentence about it, and
// whether Start at login is ticked, as it is unless the home it updates had
// it turned off.
type Plan struct {
	Folder       string `json:"folder"`
	Update       bool   `json:"update"`
	Note         string `json:"note"`
	StartAtLogin bool   `json:"start_at_login"`
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
	return plan, nil
}
