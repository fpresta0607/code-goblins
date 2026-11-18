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
var gateHarnesses = []string{"claude", "codex", "pi"}

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
	if profile.Harness == "pi" {
		provider, model, ok := strings.Cut(profile.Model, "/")
		if !ok || provider == "" || model == "" {
			return fmt.Errorf("pipeline: a gate agent on pi needs its model as provider/model, not %q", profile.Model)
		}
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

// GateChain is the agents one role of a version 6 gate may start, in order:
// the task's own harness with that role's model and effort, then the fallback
// the operator named when that is another harness and isSignedIn says
// somebody is signed in to it. isSignedIn is given the whole fallback, because
// pi signs in per provider and the fallback's model names its provider. With
// no fallback named nothing is asked and no second harness starts.
func (p Policy) GateChain(own Reviewer, isSignedIn func(fallback Reviewer) bool) ([]Reviewer, error) {
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
	if p.Fallback != (Reviewer{}) && p.Fallback.Harness != own.Harness && isSignedIn(p.Fallback) {
		chain = append(chain, p.Fallback)
	}
	return chain, nil
}

// GateRoles are the chains one run's gate roles start. The primary runs the
// Test step and every other step but review and its fixes, the reviewer
// reviews and the fixer applies the review's findings.
type GateRoles struct {
	Primary  []Reviewer
	Reviewer []Reviewer
	Fixer    []Reviewer
}

// GateRoleChains gives each gate role the GateChain of the profile it runs
// on: the primary the verify role's, since the Test step is the primary's,
// the reviewer the review role's and the fixer the fix role's. A fixer apart
// from the primary runs its own profile alone, because no-mistakes takes no
// fallback for a pinned fixer. The fallback's sign-in is asked once.
func (p Policy) GateRoleChains(verify, review, fix Reviewer, isSignedIn func(fallback Reviewer) bool) (GateRoles, error) {
	isAsked, isFallbackSignedIn := false, false
	askOnce := func(fallback Reviewer) bool {
		if !isAsked {
			isAsked, isFallbackSignedIn = true, isSignedIn(fallback)
		}
		return isFallbackSignedIn
	}
	primary, err := p.GateChain(verify, askOnce)
	if err != nil {
		return GateRoles{}, err
	}
	reviewer, err := p.GateChain(review, askOnce)
	if err != nil {
		return GateRoles{}, err
	}
	fixer, err := p.GateChain(fix, askOnce)
	if err != nil {
		return GateRoles{}, err
	}
	if !slices.Equal(fixer, primary) {
		fixer = fixer[:1]
	}
	return GateRoles{Primary: primary, Reviewer: reviewer, Fixer: fixer}, nil
}

type launchProfile struct {
	Harness     string `json:"harness"`
	Model       string `json:"model"`
	Effort      string `json:"effort"`
	ServiceTier string `json:"service_tier,omitempty"`
}

// LaunchSelection is how one run carries its own agents: a no-mistakes launch
// assertion that the daemon applies to that run alone, giving each gate role
// its chain, and then proves against the trusted commit before any agent
// starts. A reviewer or fixer chain equal to the primary runs that role on
// the primary chain; one that differs pins the role, which only a build with
// no-mistakes pull request 310 applies. A build that cannot apply it refuses
// the launch.
func LaunchSelection(trustedSHA string, roles GateRoles) ([]byte, error) {
	if len(trustedSHA) != 40 || strings.Trim(trustedSHA, "0123456789abcdef") != "" {
		return nil, errors.New("pipeline: a launch selection names the trusted commit by its full lowercase SHA")
	}
	if len(roles.Fixer) > 1 && !slices.Equal(roles.Fixer, roles.Primary) {
		return nil, errors.New("pipeline: a launch selection names one fixer agent unless the fixer runs the primary chain")
	}
	profiles := make(map[string][]launchProfile, 3)
	for _, role := range []struct {
		name  string
		chain []Reviewer
	}{{"primary", roles.Primary}, {"reviewer", roles.Reviewer}, {"fixer", roles.Fixer}} {
		if len(role.chain) == 0 {
			return nil, fmt.Errorf("pipeline: a launch selection names at least one %s agent", role.name)
		}
		for _, agent := range role.chain {
			if err := gateProfile(agent); err != nil {
				return nil, err
			}
			if slices.ContainsFunc(profiles[role.name], func(named launchProfile) bool { return named.Harness == agent.Harness }) {
				return nil, fmt.Errorf("pipeline: a launch selection's %s chain names %s twice", role.name, agent.Harness)
			}
			profile := launchProfile{Harness: agent.Harness, Model: agent.Model, Effort: agent.Effort}
			if agent.Harness == "codex" {
				profile.ServiceTier = codexServiceTier
			}
			profiles[role.name] = append(profiles[role.name], profile)
		}
	}
	return json.Marshal(struct {
		TrustedSHA string                     `json:"trusted_sha"`
		Profiles   map[string][]launchProfile `json:"profiles"`
		Apply      bool                       `json:"apply"`
	}{trustedSHA, profiles, true})
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
