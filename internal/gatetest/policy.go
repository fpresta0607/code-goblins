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
