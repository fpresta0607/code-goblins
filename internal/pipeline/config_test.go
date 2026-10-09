package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"gopkg.in/yaml.v3"
)

type runnerFunc func(context.Context, execx.Request) (execx.Result, error)

func (f runnerFunc) Run(c context.Context, r execx.Request) (execx.Result, error) { return f(c, r) }

func testPolicy(t *testing.T) Policy {
	t.Helper()
	p, err := Load(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRenderPreservesUnownedConfigAndIsIdempotent(t *testing.T) {
	p := testPolicy(t)
	before := []byte("# machine config\nagent: [claude]\nagent_args_override:\n  claude: [--model, opus, --effort, high]\nauto_fix:\n  review: 10\n  document: 4\nci_timeout: 168h\nprivate_token: sentinel-secret\n")
	after, drift, err := Render(before, p)
	if err != nil || len(drift) == 0 {
		t.Fatalf("render: %v %v", drift, err)
	}
	for _, keep := range []string{"# machine config", "document: 4", "ci_timeout: 168h", "private_token: sentinel-secret"} {
		if !strings.Contains(string(after), keep) {
			t.Fatalf("lost %q", keep)
		}
	}
	if strings.Contains(strings.Join(drift, " "), "sentinel-secret") {
		t.Fatal("drift leaked secret")
	}
	var rendered struct {
		Agent       []string `yaml:"agent"`
		AgentConfig map[string]struct {
			Model  string `yaml:"model"`
			Effort string `yaml:"effort"`
		} `yaml:"agent_config"`
		ReviewAgents map[string]struct {
			Agent  string `yaml:"agent"`
			Model  string `yaml:"model"`
			Effort string `yaml:"effort"`
		} `yaml:"review_agents"`
		AgentArgs map[string][]string `yaml:"agent_args_override"`
	}
	if err := yaml.Unmarshal(after, &rendered); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rendered.Agent, []string{"codex", "claude"}) {
		t.Fatalf("agent chain=%v", rendered.Agent)
	}
	profile := rendered.AgentConfig["codex"]
	if profile.Model != "gpt-6.1-sol" || profile.Effort != "xhigh" {
		t.Fatalf("primary profile=%+v", profile)
	}
	if _, ok := rendered.AgentConfig["claude"]; ok {
		t.Fatalf("the Claude fallback gained a profile: %+v", rendered.AgentConfig)
	}
	if rendered.ReviewAgents != nil {
		t.Fatalf("review roles pinned outside the chain: %+v", rendered.ReviewAgents)
	}
	if !reflect.DeepEqual(rendered.AgentArgs, gateAgentArgs) {
		t.Fatalf("agent args=%v, want the standard service tier and no MCP server for either agent", rendered.AgentArgs)
	}
	again, drift, err := Render(after, p)
	if err != nil || len(drift) != 0 || string(again) != string(after) {
		t.Fatalf("not idempotent: %v %v", drift, err)
	}
}

// gateAgentArgs is what version 5 hands each gate agent: the standard service
// tier, and none of the MCP servers the operator's own configuration starts.
var gateAgentArgs = map[string][]string{
	"codex":  {"-c", `service_tier="default"`, "--ignore-user-config", "--disable", "plugins", "--disable", "apps"},
	"claude": {"--strict-mcp-config"},
}

func versionFour(t *testing.T) Policy {
	t.Helper()
	p := testPolicy(t)
	p.Version = 4
	return p
}

func TestVersionFiveStartsGateAgentsWithoutMCPServers(t *testing.T) {
	after, drift, err := Render([]byte(machineConfig20261008), testPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(drift, []string{"agent_args_override.claude", "agent_args_override.codex"}) {
		t.Fatalf("drift=%v, want only the gate agents' arguments", drift)
	}
	var config struct {
		Agent     []string            `yaml:"agent"`
		AgentArgs map[string][]string `yaml:"agent_args_override"`
	}
	if err := yaml.Unmarshal(after, &config); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config.AgentArgs, gateAgentArgs) || !reflect.DeepEqual(config.Agent, []string{"codex", "claude"}) {
		t.Fatalf("agent=%v args=%v, want the chain kept and no MCP server for either agent", config.Agent, config.AgentArgs)
	}
	if !strings.Contains(string(after), "# Prefer Codex at the user's request; retain Claude as an available fallback.") {
		t.Fatalf("lost the operator's note on the chain:\n%s", after)
	}
	again, drift, err := Render(after, testPolicy(t))
	if err != nil || len(drift) != 0 || string(again) != string(after) {
		t.Fatalf("not idempotent: %v %v", drift, err)
	}
}

func TestVersionFiveOwnsTheClaudeArguments(t *testing.T) {
	for _, before := range []string{"agent_args_override:\n  claude: [--model, sonnet]\n", "agent_args_override:\n  claude: [--strict-mcp-config, --mcp-config, servers.json]\n"} {
		after, drift, err := Render([]byte(before), testPolicy(t))
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			AgentArgs map[string][]string `yaml:"agent_args_override"`
		}
		if err := yaml.Unmarshal(after, &config); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(drift, "agent_args_override.claude") || !reflect.DeepEqual(config.AgentArgs["claude"], gateAgentArgs["claude"]) {
			t.Fatalf("drift=%v claude=%v, want the gate's own Claude arguments", drift, config.AgentArgs["claude"])
		}
	}
}

// machineConfig20261008 is the shared config's owned shape since the operator
// gave the gate its Claude fallback on 2026-10-08.
const machineConfig20261008 = `# no-mistakes global configuration
# Validation roles inherit this ordered harness chain unless explicitly overridden.
# Prefer Codex at the user's request; retain Claude as an available fallback.
agent:
  - codex
  - claude
ci_timeout: "168h"
session_reuse: true
agent_path_override: {}
auto_fix:
  rebase: 1
  lint: 1
  test: 1
  review: 0
  document: 10
  ci: 1
agent_args_override:
  codex:
    - -c
    - service_tier="default"
agent_config:
  codex:
    effort: xhigh
    model: gpt-6.1-sol
# Review and fix roles inherit the same harness chain as other validation steps.
review_agent_timeout: "1h"
`

func TestVersionFourRendersTheMachineConfigAsItStandsWithoutDrift(t *testing.T) {
	after, drift, err := Render([]byte(machineConfig20261008), versionFour(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 0 || string(after) != machineConfig20261008 {
		t.Fatalf("drift=%v, want the policy to name the machine's chain as it stands:\n%s", drift, after)
	}
}

func TestVersionFourReleasesReviewRolePinsIntoTheChain(t *testing.T) {
	chain := versionFour(t)
	previous := chain
	previous.Version, previous.Fallback = 3, Reviewer{}
	previous.Reviewer, previous.Fixer = previous.Primary, previous.Primary
	before, _, err := Render([]byte("review_agents:\n  reviewer_after_round: {agent: claude}\n"), previous)
	if err != nil {
		t.Fatal(err)
	}
	after, drift, err := Render(before, chain)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(drift, []string{"agent", "review_agents"}) {
		t.Fatalf("drift=%v, want the chain and the released review roles", drift)
	}
	var config map[string]interface{}
	if err := yaml.Unmarshal(after, &config); err != nil {
		t.Fatal(err)
	}
	if _, ok := config["review_agents"]; ok {
		t.Fatalf("review roles still pinned outside the chain:\n%s", after)
	}
	again, drift, err := Render(after, chain)
	if err != nil || len(drift) != 0 || string(again) != string(after) {
		t.Fatalf("not idempotent: %v %v", drift, err)
	}
}

func TestApplyWithoutDriftTakesNoIdleWindow(t *testing.T) {
	applied, _, err := Render([]byte("ci_timeout: 168h\n"), testPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, applied, 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{Path: path, Policy: testPolicy(t), Idle: func(context.Context) (func() error, error) {
		t.Error("a change of nothing asked for the daemon to be stopped")
		return nil, ErrBusy
	}}
	result, err := c.Apply(context.Background())
	if err != nil || len(result.Drift) != 0 || result.Backup != "" {
		t.Fatalf("apply=%+v err=%v", result, err)
	}
	now, err := os.ReadFile(path)
	if err != nil || string(now) != string(applied) {
		t.Fatalf("config changed: %s %v", now, err)
	}
}

func TestRenderOverridesCodexFastServiceTier(t *testing.T) {
	before := []byte("agent_args_override:\n  codex: [-c, 'service_tier=\"fast\"']\n")
	after, drift, err := Render(before, testPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(drift, "agent_args_override.codex") {
		t.Fatalf("drift=%v, want Codex argument ownership", drift)
	}
	var config struct {
		AgentArgs map[string][]string `yaml:"agent_args_override"`
	}
	if err := yaml.Unmarshal(after, &config); err != nil {
		t.Fatal(err)
	}
	if args := config.AgentArgs["codex"]; !reflect.DeepEqual(args, gateAgentArgs["codex"]) {
		t.Fatalf("effective Codex args=%v, want the standard service tier", args)
	}
}

func TestRenderRejectsAmbiguousYAML(t *testing.T) {
	for _, source := range []string{"agent: [claude]\nagent: [pi]\n", "agent: [claude]\n---\nagent: [pi]\n", "auto_fix: &x {review: 10}\nother: *x\n", "auto_fix: nope\n"} {
		if _, _, err := Render([]byte(source), testPolicy(t)); err == nil {
			t.Errorf("accepted ambiguous YAML %q", source)
		}
	}
}

// Up to version 4 the policy does not own Claude's arguments.
func TestRenderPreservesOperatorOwnedClaudeArguments(t *testing.T) {
	before := []byte("agent_args_override:\n  claude: [--model, sonnet]\n")
	after, _, err := Render(before, versionFour(t))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		AgentArgs map[string][]string `yaml:"agent_args_override"`
	}
	if err := yaml.Unmarshal(after, &config); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config.AgentArgs["claude"], []string{"--model", "sonnet"}) {
		t.Fatalf("operator Claude arguments changed: %v", config.AgentArgs["claude"])
	}
}

func TestRenderRemovesCodexExecutableOverrideOnly(t *testing.T) {
	before := []byte("agent_path_override:\n  codex: C:/tools/openrouter-codex.exe\n  claude: C:/tools/claude.exe\n")
	after, drift, err := Render(before, testPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		AgentPaths map[string]string `yaml:"agent_path_override"`
	}
	if err := yaml.Unmarshal(after, &config); err != nil {
		t.Fatal(err)
	}
	if _, ok := config.AgentPaths["codex"]; ok {
		t.Fatalf("Codex executable override remains: %v", config.AgentPaths)
	}
	if config.AgentPaths["claude"] != "C:/tools/claude.exe" {
		t.Fatalf("unrelated executable override changed: %v", config.AgentPaths)
	}
	if !slices.Contains(drift, "agent_path_override.codex") {
		t.Fatalf("drift=%v, want agent_path_override.codex", drift)
	}
	if strings.Contains(strings.Join(drift, " "), "openrouter") {
		t.Fatalf("drift leaked executable value: %v", drift)
	}
	again, drift, err := Render(after, testPolicy(t))
	if err != nil || len(drift) != 0 || string(again) != string(after) {
		t.Fatalf("not idempotent: %v %v", drift, err)
	}
}

func TestApplyRefusesBusyAndBacksUpIdleConfig(t *testing.T) {
	for _, busy := range []bool{true, false} {
		t.Run(map[bool]string{true: "busy", false: "idle"}[busy], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			before := "agent: [pi]\n"
			if err := os.WriteFile(path, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			applies := 0
			c := Config{Path: path, Policy: testPolicy(t), Idle: func(context.Context) (func() error, error) {
				applies++
				if busy {
					return nil, ErrBusy
				}
				return func() error { return nil }, nil
			}}
			result, err := c.Apply(context.Background())
			now, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if busy {
				if err == nil || string(now) != before || result.Backup != "" {
					t.Fatalf("busy mutation: %+v %v", result, err)
				}
			} else {
				if err != nil || result.Backup == "" || applies != 1 {
					t.Fatalf("apply: %+v %v", result, err)
				}
				old, err := os.ReadFile(result.Backup)
				if err != nil || string(old) != before {
					t.Fatalf("backup: %s %v", old, err)
				}
				_, drift, err := Render(now, testPolicy(t))
				if err != nil || len(drift) != 0 {
					t.Fatalf("applied drift: %v %v", drift, err)
				}
			}
		})
	}
}
