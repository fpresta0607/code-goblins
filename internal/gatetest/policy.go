package gatetest

import (
	"encoding/json"
	"fmt"
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
	return p.Contracts != nil || p.Outside != nil
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
	return policy, nil
}
