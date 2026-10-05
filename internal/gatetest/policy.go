package gatetest

import (
	"encoding/json"
	"fmt"
	"slices"
)

// PolicyPath is where a repository keeps its verification policy, from its
// root.
const PolicyPath = "config/verify.json"

// policyVersion is the policy version this build reads.
const policyVersion = 1

// Policy is a repository's verification policy.
type Policy struct {
	Version int `json:"version"`
	// SlowPackages are the directories, from the repository root with forward
	// slashes and . for the root, of the packages whose tests take minutes:
	// the fast level vets them and leaves their tests to the affected level.
	SlowPackages []string `json:"slow_packages"`
	// Contracts name the packages whose tests read a file the import graph
	// cannot see, and Outside the files no Go check reads. A policy that has
	// either says what every changed file is, and a file it does not account
	// for is unknown, which requires the full level.
	Contracts []Contract `json:"contracts"`
	Outside   []Outside  `json:"outside"`
	// Checks are the repository's checks besides Go's, such as a frontend's,
	// and the files each reads.
	Checks []Check `json:"checks"`
}

// Check is a check besides Go's: a change to a file Paths names selects it,
// and the full level runs it whatever changed. Its Commands run one after
// another in Dir, from the repository root, after Install where that is
// needed, and then no file under Unchanged may differ from the commit, which
// is how a check says its commands must rebuild exactly what is committed.
// Left are what CI runs of it that this step does not, each with why.
type Check struct {
	Name      string     `json:"name"`
	Paths     []string   `json:"paths"`
	Dir       string     `json:"dir"`
	Install   *Install   `json:"install"`
	Commands  [][]string `json:"commands"`
	Unchanged []string   `json:"unchanged"`
	Left      []Deferred `json:"left"`
}

// Install prepares what a check's commands run with, such as npm ci, in the
// check's directory. It runs only when Output does not already hold an
// install made from the same Inputs, files from the repository root, and the
// same Versions, the output of commands that print a tool's version.
type Install struct {
	Command  []string   `json:"command"`
	Inputs   []string   `json:"inputs"`
	Versions [][]string `json:"versions"`
	Output   string     `json:"output"`
}

// Contract says that the tests of Packages read the files Paths name, so a
// change to one of those files selects them. Paths are patterns from the
// repository root (see matches), and Packages directories as SlowPackages
// names them.
type Contract struct {
	Paths    []string `json:"paths"`
	Packages []string `json:"packages"`
	Why      string   `json:"why"`
}

// Outside says that no Go check reads the files Paths name, and why.
type Outside struct {
	Paths []string `json:"paths"`
	Why   string   `json:"why"`
}

// Classifies reports whether the policy says what every changed file is. A
// policy written before it could names neither a contract nor anything
// outside the Go checks, and under it a file no package owns selects nothing,
// as it always did.
func (p Policy) Classifies() bool {
	return p.Contracts != nil || p.Outside != nil || p.Checks != nil
}

// ParsePolicy reads a policy. A field this build does not know is ignored,
// since the build a gate has installed is older than the policy a later one
// extended; a policy of another version, or one that does not parse, is
// refused, and the caller widens the run.
func ParsePolicy(data []byte) (Policy, error) {
	var policy Policy
	if err := json.Unmarshal(data, &policy); err != nil {
		return Policy{}, fmt.Errorf("gatetest: %s does not parse: %w", PolicyPath, err)
	}
	if policy.Version != policyVersion {
		return Policy{}, fmt.Errorf("gatetest: %s is version %d and this build reads version %d", PolicyPath, policy.Version, policyVersion)
	}
	for _, check := range policy.Checks {
		if err := check.validate(); err != nil {
			return Policy{}, fmt.Errorf("gatetest: %s: %w", PolicyPath, err)
		}
	}
	return policy, nil
}

// validate refuses a check that could not run as written: one with no name,
// no paths, no directory or no command, an empty command, or an install
// without its command, its inputs or its output.
func (c Check) validate() error {
	if c.Name == "" || len(c.Paths) == 0 || c.Dir == "" || len(c.Commands) == 0 {
		return fmt.Errorf("the check %q needs a name, paths, a dir and commands", c.Name)
	}
	if slices.ContainsFunc(c.Commands, func(command []string) bool { return len(command) == 0 }) {
		return fmt.Errorf("the check %q has an empty command", c.Name)
	}
	if c.Install != nil && (len(c.Install.Command) == 0 || len(c.Install.Inputs) == 0 || c.Install.Output == "" || slices.ContainsFunc(c.Install.Versions, func(command []string) bool { return len(command) == 0 })) {
		return fmt.Errorf("the install of the check %q needs a command, inputs and an output, and no empty version command", c.Name)
	}
	return nil
}
