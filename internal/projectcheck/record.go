package projectcheck

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/project"
)

// record assesses the project's record, project.json: that it is there, that
// the loader takes it, that it describes this project, and that the commands
// its verification and security tiers name are programs this machine has.
func (c *checker) record() {
	path := project.Path(c.DataDir, c.project)
	manifest, err := project.Load(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.add(AreaRecord, "record-missing", High,
			"the project has no record, so nothing steers its routing, verification or deployment",
			"no file at "+path,
			"run cfo project check with --draft for the record this run can vouch for, read it, complete it and place it at "+path)
		return
	case err != nil:
		c.add(AreaRecord, "record-invalid", High,
			"the loader refuses the project's record, so every command that reads it fails",
			path+": "+err.Error(),
			"correct the field the loader names")
		return
	}
	c.proven.record = &manifest
	c.add(AreaRecord, "record-valid", OK, "the project's record is present and the loader takes it", path, "")
	if manifest.Project != c.project {
		c.add(AreaRecord, "record-names-another-project", Medium,
			"the record names a project other than the checkout it is filed under",
			fmt.Sprintf("%s says project %q and is filed for the checkout folder %q", path, manifest.Project, c.project),
			fmt.Sprintf("set project to %q", c.project))
	}
	c.tiers(path, manifest)
}

// tiers checks the commands of the record's verification and security tiers.
func (c *checker) tiers(path string, manifest project.Manifest) {
	tiers := []struct {
		name     string
		commands []project.Command
	}{
		{"verification.fast", manifest.Verification.Fast},
		{"verification.full", manifest.Verification.Full},
		{"verification.deep", manifest.Verification.Deep},
		{"security.fast", manifest.Security.Fast},
		{"security.deep", manifest.Security.Deep},
	}
	var found, missing []string
	for _, tier := range tiers {
		for index, command := range tier.commands {
			if len(command) == 0 {
				continue
			}
			where, ok := c.program(segment{argv: command}, inWorktree)
			if ok {
				found = append(found, fmt.Sprintf("%s[%d] %s %s", tier.name, index, command[0], where))
				continue
			}
			missing = append(missing, fmt.Sprintf("%s[%d] %q: %s", tier.name, index, strings.Join(command, " "), where))
		}
	}
	if !verifies(manifest) {
		c.add(AreaRecord, "tiers-empty", Medium,
			"the record names no verification command, so cfo verify passes with nothing run",
			"verification.fast, verification.full and verification.deep are empty in "+path,
			"name the project's real test commands in verification.fast and verification.full")
	}
	if len(missing) > 0 {
		c.add(AreaRecord, "tier-command-missing", High,
			"a tier of the record names a program this machine does not have, so that tier fails every time",
			strings.Join(missing, ", "),
			"install the program or name the command the project really runs")
	}
	if len(found) > 0 {
		c.add(AreaRecord, "tier-commands-found", OK,
			"this machine has the program of "+count(len(found), "tier command")+" of the record",
			strings.Join(found, ", "), "")
	}
}
