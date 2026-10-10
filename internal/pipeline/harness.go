package pipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// gateHarnesses are the harnesses whose model and effort no-mistakes can
// prove before it starts an agent, which is what lets one daemon run each
// gate on the harness its own run names.
var gateHarnesses = []string{"claude", "codex"}

// gateEfforts are the reasoning efforts no-mistakes names for every harness.
var gateEfforts = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

// codexServiceTier is the service tier the policy's own Codex arguments set.
// no-mistakes proves a Codex agent's tier with its model and effort, so a
// launch selection names it.
const codexServiceTier = "default"

// GateHarness refuses a harness no gate can run on under version 6.
func GateHarness(harness string) error {
	if !slices.Contains(gateHarnesses, harness) {
		return fmt.Errorf("pipeline: a gate runs on its task's own harness, and no-mistakes can prove an agent on %s only, not on %q", strings.Join(gateHarnesses, " or "), harness)
	}
	return nil
}

// gateProfile checks one agent of a gate chain. Its model and effort are
// named outright, because no-mistakes proves what a launch names and never a
// harness's own default.
func gateProfile(profile Reviewer) error {
	if err := GateHarness(profile.Harness); err != nil {
		return err
	}
	if !isGateToken(profile.Model) {
		return fmt.Errorf("pipeline: a gate agent on %s needs its model named as an exact identifier, not %q", profile.Harness, profile.Model)
	}
	if !slices.Contains(gateEfforts, profile.Effort) {
		return fmt.Errorf("pipeline: a gate agent on %s needs one of the efforts %s, not %q", profile.Harness, strings.Join(gateEfforts, ", "), profile.Effort)
	}
	return nil
}

// isGateToken matches the identifiers no-mistakes accepts for a model.
func isGateToken(value string) bool {
	if value == "" || len(value) > 256 || value[0] == '-' {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._-/", character)) {
			return false
		}
	}
	return true
}

// GateChain is the agents a version 6 gate may start, in order: the task's
// own harness with its model and effort, then the fallback the operator named
// when that is another harness and isSignedIn says somebody is signed in to
// it. With no fallback named nothing is asked and no second harness starts.
func (p Policy) GateChain(own Reviewer, isSignedIn func(harness string) bool) ([]Reviewer, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.Version < 6 {
		return nil, errors.New("pipeline: a policy before version 6 fixes its own gate chain")
	}
	if err := gateProfile(own); err != nil {
		return nil, err
	}
	chain := []Reviewer{own}
	if p.Fallback != (Reviewer{}) && p.Fallback.Harness != own.Harness && isSignedIn(p.Fallback.Harness) {
		chain = append(chain, p.Fallback)
	}
	return chain, nil
}

type launchProfile struct {
	Harness     string `json:"harness"`
	Model       string `json:"model"`
	Effort      string `json:"effort"`
	ServiceTier string `json:"service_tier,omitempty"`
}

// LaunchSelection is how one run carries its own agents: a no-mistakes launch
// assertion that the daemon applies to that run alone, giving every gate role
// the chain, and then proves against the trusted commit before any agent
// starts. A build that cannot apply it refuses the launch.
func LaunchSelection(trustedSHA string, chain []Reviewer) ([]byte, error) {
	if len(trustedSHA) != 40 || strings.Trim(trustedSHA, "0123456789abcdef") != "" {
		return nil, errors.New("pipeline: a launch selection names the trusted commit by its full lowercase SHA")
	}
	if len(chain) == 0 {
		return nil, errors.New("pipeline: a launch selection names at least one gate agent")
	}
	profiles := make([]launchProfile, 0, len(chain))
	for _, agent := range chain {
		if err := gateProfile(agent); err != nil {
			return nil, err
		}
		if slices.ContainsFunc(profiles, func(named launchProfile) bool { return named.Harness == agent.Harness }) {
			return nil, fmt.Errorf("pipeline: a launch selection names %s twice", agent.Harness)
		}
		profile := launchProfile{Harness: agent.Harness, Model: agent.Model, Effort: agent.Effort}
		if agent.Harness == "codex" {
			profile.ServiceTier = codexServiceTier
		}
		profiles = append(profiles, profile)
	}
	return json.Marshal(struct {
		TrustedSHA string                     `json:"trusted_sha"`
		Profiles   map[string][]launchProfile `json:"profiles"`
		Apply      bool                       `json:"apply"`
	}{trustedSHA, map[string][]launchProfile{"primary": profiles, "reviewer": profiles, "fixer": profiles}, true})
}

// MachineProfile is the model and effort the machine config's agent_config
// gives a harness, which is the operator's default for a task that names
// none. A harness it does not list has neither.
func MachineProfile(config []byte, harness string) (Reviewer, error) {
	doc, err := parseYAML(config)
	if err != nil {
		return Reviewer{}, err
	}
	var machine struct {
		AgentConfig map[string]struct {
			Model  string `yaml:"model"`
			Effort string `yaml:"effort"`
		} `yaml:"agent_config"`
	}
	if err := doc.Decode(&machine); err != nil {
		return Reviewer{}, errors.New("pipeline: agent_config must map each harness to its model and effort")
	}
	profile := machine.AgentConfig[harness]
	return Reviewer{Harness: harness, Model: profile.Model, Effort: profile.Effort}, nil
}
