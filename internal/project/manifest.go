package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Command []string

type Service struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Provider    string   `json:"provider,omitempty"`
	Environment string   `json:"environment,omitempty"`
	Location    string   `json:"location,omitempty"`
	Env         []string `json:"env,omitempty"`
	Endpoint    string   `json:"endpoint,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Health      Command  `json:"health,omitempty"`
	Deploy      Command  `json:"deploy,omitempty"`
}

type DeployTarget struct {
	Name        string  `json:"name"`
	Provider    string  `json:"provider,omitempty"`
	Environment string  `json:"environment,omitempty"`
	Command     Command `json:"command"`
	Verify      Command `json:"verify,omitempty"`
}

type Deployment struct {
	Required bool           `json:"required"`
	Targets  []DeployTarget `json:"targets,omitempty"`
}
type Verification struct {
	Fast           []Command `json:"fast,omitempty"`
	Full           []Command `json:"full,omitempty"`
	Deep           []Command `json:"deep,omitempty"`
	ChangedOnly    bool      `json:"changed_only,omitempty"`
	MaxFastSeconds int       `json:"max_fast_seconds,omitempty"`
	MaxFullSeconds int       `json:"max_full_seconds,omitempty"`
}
type Security struct {
	Mode     string    `json:"mode,omitempty"`
	Fast     []Command `json:"fast,omitempty"`
	Deep     []Command `json:"deep,omitempty"`
	Triggers []string  `json:"triggers,omitempty"`
}
type Hygiene struct {
	SupersedeClean  bool      `json:"supersede_clean,omitempty"`
	DeadCode        []Command `json:"dead_code,omitempty"`
	AggressiveClean bool      `json:"aggressive_clean,omitempty"`
}
type Lane struct {
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}
type Routing struct {
	DefaultLane string          `json:"default_lane,omitempty"`
	EscalateTo  string          `json:"escalate_to,omitempty"`
	Lanes       map[string]Lane `json:"lanes,omitempty"`
}
type Budget struct {
	WarnContextTokens    int `json:"warn_context_tokens,omitempty"`
	CompactContextTokens int `json:"compact_context_tokens,omitempty"`
	RestartContextTokens int `json:"restart_context_tokens,omitempty"`
	MaxRepairRounds      int `json:"max_repair_rounds,omitempty"`
}
type Manifest struct {
	Project      string            `json:"project"`
	Services     []Service         `json:"services,omitempty"`
	Deployment   Deployment        `json:"deployment,omitempty"`
	Verification Verification      `json:"verification,omitempty"`
	Security     Security          `json:"security,omitempty"`
	Hygiene      Hygiene           `json:"hygiene,omitempty"`
	Routing      Routing           `json:"routing,omitempty"`
	Budgets      map[string]Budget `json:"budgets,omitempty"`
}

func Path(dataDir, projectName string) string {
	return filepath.Join(dataDir, "projects", projectName, "project.json")
}
func Load(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("project manifest: %w", err)
	}
	if dec.More() {
		return Manifest{}, errors.New("project manifest: trailing JSON")
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}
func LoadForCheckout(dataDir, checkout string) (Manifest, string, error) {
	name := filepath.Base(filepath.Clean(checkout))
	p := Path(dataDir, name)
	m, err := Load(p)
	return m, p, err
}
func (m Manifest) Validate() error {
	if strings.TrimSpace(m.Project) == "" {
		return errors.New("project manifest: project is required")
	}
	seen := map[string]bool{}
	for _, s := range m.Services {
		if strings.TrimSpace(s.Name) == "" {
			return errors.New("project manifest: service name is required")
		}
		if seen[s.Name] {
			return fmt.Errorf("project manifest: duplicate service %q", s.Name)
		}
		seen[s.Name] = true
		if strings.TrimSpace(s.Kind) == "" {
			return fmt.Errorf("project manifest: service %q kind is required", s.Name)
		}
		if err := validateCommand(s.Health); err != nil {
			return fmt.Errorf("project manifest: service %q health: %w", s.Name, err)
		}
		if err := validateCommand(s.Deploy); err != nil {
			return fmt.Errorf("project manifest: service %q deploy: %w", s.Name, err)
		}
	}
	for _, s := range m.Services {
		for _, d := range s.DependsOn {
			if !seen[d] {
				return fmt.Errorf("project manifest: service %q depends on unknown service %q", s.Name, d)
			}
		}
	}
	targets := map[string]bool{}
	for _, t := range m.Deployment.Targets {
		if t.Name == "" {
			return errors.New("project manifest: deployment target name is required")
		}
		if targets[t.Name] {
			return fmt.Errorf("project manifest: duplicate deployment target %q", t.Name)
		}
		targets[t.Name] = true
		if len(t.Command) == 0 {
			return fmt.Errorf("project manifest: deployment target %q command is required", t.Name)
		}
		if err := validateCommand(t.Command); err != nil {
			return err
		}
		if err := validateCommand(t.Verify); err != nil {
			return err
		}
	}
	if m.Deployment.Required && len(m.Deployment.Targets) == 0 {
		return errors.New("project manifest: deployment.required needs at least one target")
	}
	switch m.Security.Mode {
	case "", "off", "changed", "risk", "scheduled", "always":
	default:
		return fmt.Errorf("project manifest: invalid security mode %q", m.Security.Mode)
	}
	for name, l := range m.Routing.Lanes {
		if l.Harness == "" {
			return fmt.Errorf("project manifest: routing lane %q harness is required", name)
		}
	}
	if m.Routing.DefaultLane != "" {
		if _, ok := m.Routing.Lanes[m.Routing.DefaultLane]; !ok {
			return fmt.Errorf("project manifest: default lane %q is undefined", m.Routing.DefaultLane)
		}
	}
	if m.Routing.EscalateTo != "" {
		if _, ok := m.Routing.Lanes[m.Routing.EscalateTo]; !ok {
			return fmt.Errorf("project manifest: escalation lane %q is undefined", m.Routing.EscalateTo)
		}
	}
	return nil
}
func validateCommand(c Command) error {
	for _, a := range c {
		if strings.ContainsRune(a, '\x00') {
			return errors.New("command contains NUL")
		}
	}
	return nil
}
func (m Manifest) Capsule() string {
	var b strings.Builder
	b.WriteString("PROJECT RUNTIME\n")
	for _, s := range m.Services {
		fmt.Fprintf(&b, "- %s: kind=%s", s.Name, s.Kind)
		if s.Provider != "" {
			fmt.Fprintf(&b, " provider=%s", s.Provider)
		}
		if s.Environment != "" {
			fmt.Fprintf(&b, " env=%s", s.Environment)
		}
		if s.Location != "" {
			fmt.Fprintf(&b, " location=%s", s.Location)
		}
		if len(s.Env) > 0 {
			fmt.Fprintf(&b, " credentials=%s (injected; values withheld)", strings.Join(s.Env, ","))
		}
		if s.Endpoint != "" {
			fmt.Fprintf(&b, " endpoint=%s", s.Endpoint)
		}
		b.WriteByte('\n')
	}
	if m.Deployment.Required {
		b.WriteString("Delivery: production deployment is required; CI alone is insufficient.\n")
	} else {
		b.WriteString("Delivery: production deployment is not required by project policy.\n")
	}
	for _, t := range m.Deployment.Targets {
		fmt.Fprintf(&b, "- deploy %s (%s): %s\n", t.Name, t.Provider, strings.Join(t.Command, " "))
		if len(t.Verify) > 0 {
			fmt.Fprintf(&b, "  verify: %s\n", strings.Join(t.Verify, " "))
		}
	}
	return b.String()
}

func (s Security) NeedsDeep(changed []string) bool {
	if s.Mode == "always" || s.Mode == "scheduled" {
		return true
	}
	if s.Mode != "risk" {
		return false
	}
	for _, p := range changed {
		low := strings.ToLower(p)
		for _, t := range s.Triggers {
			if strings.Contains(low, strings.ToLower(t)) {
				return true
			}
		}
	}
	return false
}
