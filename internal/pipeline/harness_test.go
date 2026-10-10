package pipeline

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var (
	claudeTask = Reviewer{Harness: "claude", Model: "claude-opus-5-5", Effort: "xhigh"}
	codexTask  = Reviewer{Harness: "codex", Model: "gpt-6.1-sol", Effort: "high"}
)

// versionSix is the policy whose gate runs on its task's own harness, with
// the fallback the operator named, if any.
func versionSix(t *testing.T, fallback Reviewer) Policy {
	t.Helper()
	policy := testPolicy(t)
	policy.Version, policy.Primary, policy.Reviewer, policy.Fixer, policy.Fallback = 6, Reviewer{}, Reviewer{}, Reviewer{}, fallback
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	return policy
}

// nobodyAsked fails a test in which the gate asks whether a harness is signed in.
func nobodyAsked(t *testing.T) func(string) bool {
	return func(harness string) bool {
		t.Errorf("the gate asked whether %s is signed in", harness)
		return true
	}
}

func TestVersionSixNamesNoHarness(t *testing.T) {
	policy := versionSix(t, Reviewer{})
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "codex", "pi", "primary", "reviewer", "fixer", "fallback"} {
		if strings.Contains(string(data), name) {
			t.Fatalf("the policy names %s: %s", name, data)
		}
	}
	for name, change := range map[string]func(*Policy){
		"a primary":  func(p *Policy) { p.Primary = codexTask },
		"a reviewer": func(p *Policy) { p.Reviewer = codexTask },
		"a fixer":    func(p *Policy) { p.Fixer = claudeTask },
	} {
		changed := policy
		change(&changed)
		if err := changed.Validate(); err == nil {
			t.Fatalf("a version 6 policy that fixes %s was accepted", name)
		}
	}
}

func TestVersionSixFallbackIsAWholeProfileOnAHarnessAGateCanProve(t *testing.T) {
	for _, fallback := range []Reviewer{codexTask, claudeTask} {
		versionSix(t, fallback)
	}
	policy := versionSix(t, Reviewer{})
	for name, fallback := range map[string]Reviewer{
		"no model":         {Harness: "claude", Effort: "high"},
		"no effort":        {Harness: "codex", Model: "gpt-6.1-sol"},
		"unknown effort":   {Harness: "codex", Model: "gpt-6.1-sol", Effort: "fastest"},
		"a model flag":     {Harness: "claude", Model: "--model", Effort: "high"},
		"pi":               {Harness: "pi", Model: "anthropic/claude-opus-5-5", Effort: "high"},
		"no harness":       {Model: "gpt-6.1-sol", Effort: "high"},
		"a spaced harness": {Harness: "claude code", Model: "opus", Effort: "high"},
	} {
		policy.Fallback = fallback
		if err := policy.Validate(); err == nil {
			t.Fatalf("a fallback with %s was accepted: %+v", name, fallback)
		}
	}
}

func TestGateChainIsTheTasksOwnHarnessAlone(t *testing.T) {
	policy := versionSix(t, Reviewer{})
	for _, own := range []Reviewer{claudeTask, codexTask} {
		chain, err := policy.GateChain(own, nobodyAsked(t))
		if err != nil || !reflect.DeepEqual(chain, []Reviewer{own}) {
			t.Fatalf("a %s task's gate chain = %+v, %v, want its own harness alone", own.Harness, chain, err)
		}
	}
}

func TestGateChainAddsOnlyANamedSignedInFallback(t *testing.T) {
	for _, test := range []struct {
		name       string
		own        Reviewer
		fallback   Reviewer
		isSignedIn bool
		want       []Reviewer
	}{
		{"named and signed in", claudeTask, codexTask, true, []Reviewer{claudeTask, codexTask}},
		{"named and signed out", claudeTask, codexTask, false, []Reviewer{claudeTask}},
		{"the other direction", codexTask, claudeTask, true, []Reviewer{codexTask, claudeTask}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var asked []string
			chain, err := versionSix(t, test.fallback).GateChain(test.own, func(harness string) bool {
				asked = append(asked, harness)
				return test.isSignedIn
			})
			if err != nil || !reflect.DeepEqual(chain, test.want) {
				t.Fatalf("chain = %+v, %v, want %+v", chain, err, test.want)
			}
			if !reflect.DeepEqual(asked, []string{test.fallback.Harness}) {
				t.Fatalf("asked about %v, want the named fallback alone", asked)
			}
		})
	}
	// A fallback on the task's own harness is no second harness.
	other := Reviewer{Harness: "claude", Model: "sonnet", Effort: "medium"}
	chain, err := versionSix(t, other).GateChain(claudeTask, nobodyAsked(t))
	if err != nil || !reflect.DeepEqual(chain, []Reviewer{claudeTask}) {
		t.Fatalf("chain = %+v, %v, want the task's own profile alone", chain, err)
	}
}

func TestGateChainRefusesWhatAGateCannotProve(t *testing.T) {
	policy := versionSix(t, Reviewer{})
	for name, own := range map[string]Reviewer{
		"a pi task":        {Harness: "pi", Model: "anthropic/claude-opus-5-5", Effort: "high"},
		"no model":         {Harness: "codex", Effort: "high"},
		"no effort":        {Harness: "claude", Model: "claude-opus-5-5"},
		"an unread effort": {Harness: "claude", Model: "claude-opus-5-5", Effort: "default"},
		"a context suffix": {Harness: "claude", Model: "claude-opus-5-5[1m]", Effort: "high"},
	} {
		if chain, err := policy.GateChain(own, nobodyAsked(t)); err == nil {
			t.Fatalf("%s got a gate chain: %+v", name, chain)
		}
	}
	chain := testPolicy(t)
	if _, err := chain.GateChain(claudeTask, nobodyAsked(t)); err == nil {
		t.Fatalf("a version %d policy, which fixes its own chain, handed out a task's", chain.Version)
	}
}

func TestLaunchSelectionRendersTheTasksHarnessForEveryRole(t *testing.T) {
	const trusted = "0123456789abcdef0123456789abcdef01234567"
	for _, test := range []struct {
		own   Reviewer
		other string
		entry string
	}{
		{claudeTask, "codex", `{"harness":"claude","model":"claude-opus-5-5","effort":"xhigh"}`},
		{codexTask, "claude", `{"harness":"codex","model":"gpt-6.1-sol","effort":"high","service_tier":"default"}`},
	} {
		t.Run(test.own.Harness, func(t *testing.T) {
			data, err := LaunchSelection(trusted, []Reviewer{test.own})
			if err != nil {
				t.Fatal(err)
			}
			want := `{"trusted_sha":"` + trusted + `","profiles":{"fixer":[` + test.entry + `],"primary":[` + test.entry + `],"reviewer":[` + test.entry + `]},"apply":true}`
			if string(data) != want {
				t.Fatalf("launch selection = %s\nwant %s", data, want)
			}
			if strings.Contains(string(data), test.other) {
				t.Fatalf("a %s task's gate names %s: %s", test.own.Harness, test.other, data)
			}
		})
	}
}

func TestLaunchSelectionKeepsANamedFallbackInOrderForEveryRole(t *testing.T) {
	data, err := LaunchSelection("0123456789abcdef0123456789abcdef01234567", []Reviewer{claudeTask, codexTask})
	if err != nil {
		t.Fatal(err)
	}
	var selection struct {
		Profiles map[string][]map[string]string `json:"profiles"`
	}
	if err := json.Unmarshal(data, &selection); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"primary", "reviewer", "fixer"} {
		chain := selection.Profiles[role]
		if len(chain) != 2 || chain[0]["harness"] != "claude" || chain[1]["harness"] != "codex" {
			t.Fatalf("%s chain = %v, want Claude then Codex", role, chain)
		}
	}
	if len(selection.Profiles) != 3 {
		t.Fatalf("roles = %v, want primary, reviewer and fixer alone", selection.Profiles)
	}
}

func TestLaunchSelectionRefusesAnUnreadableSourceOrChain(t *testing.T) {
	const trusted = "0123456789abcdef0123456789abcdef01234567"
	for name, sha := range map[string]string{"short": "0123abc", "upper case": strings.ToUpper(trusted), "empty": ""} {
		if _, err := LaunchSelection(sha, []Reviewer{claudeTask}); err == nil {
			t.Fatalf("a %s trusted commit was accepted", name)
		}
	}
	for name, chain := range map[string][]Reviewer{
		"empty":    nil,
		"repeated": {claudeTask, claudeTask},
		"pi":       {{Harness: "pi", Model: "m", Effort: "high"}},
	} {
		if _, err := LaunchSelection(trusted, chain); err == nil {
			t.Fatalf("a %s chain was accepted", name)
		}
	}
}

func TestMachineProfileIsTheOperatorsDefaultForAHarness(t *testing.T) {
	config := []byte("agent: [claude]\nagent_config:\n  codex:\n    model: gpt-6.1-sol\n    effort: xhigh\n  claude: {model: opus}\n")
	for harness, want := range map[string]Reviewer{
		"codex":  {Harness: "codex", Model: "gpt-6.1-sol", Effort: "xhigh"},
		"claude": {Harness: "claude", Model: "opus"},
		"pi":     {Harness: "pi"},
	} {
		got, err := MachineProfile(config, harness)
		if err != nil || got != want {
			t.Fatalf("%s default = %+v, %v, want %+v", harness, got, err, want)
		}
	}
	if got, err := MachineProfile([]byte("agent: [claude]\n"), "codex"); err != nil || got != (Reviewer{Harness: "codex"}) {
		t.Fatalf("a config with no agent_config = %+v, %v", got, err)
	}
	if _, err := MachineProfile([]byte("agent_config: [codex]\n"), "codex"); err == nil {
		t.Fatal("an agent_config that is no mapping was read")
	}
}

// handEditedMachineConfig is the shared config as the CFO left it at 23:00Z on
// 2026-10-09: Claude only by hand, over the version 4 shape of 2026-10-08.
const handEditedMachineConfig = `# Machine gate configuration.
agent: [claude]
ci_timeout: 168h
auto_fix:
  review: 0
  test: 1
  lint: 1
  rebase: 1
  ci: 1
  document: 4
agent_args_override:
  codex:
    - -c
    - service_tier="default"
agent_config:
  codex:
    effort: xhigh
    model: gpt-6.1-sol
review_agent_timeout: "1h"
`

func TestVersionSixLeavesTheMachineChainToTheOperator(t *testing.T) {
	after, drift, err := Render([]byte(handEditedMachineConfig), versionSix(t, Reviewer{}))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(drift, []string{"agent_args_override.claude", "agent_args_override.codex"}) {
		t.Fatalf("drift=%v, want the two harnesses' arguments alone", drift)
	}
	var before, config map[string]interface{}
	if err := yaml.Unmarshal([]byte(handEditedMachineConfig), &before); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(after, &config); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"agent", "agent_config", "ci_timeout", "review_agent_timeout", "auto_fix"} {
		if !reflect.DeepEqual(config[key], before[key]) {
			t.Fatalf("%s changed from %v to %v", key, before[key], config[key])
		}
	}
	args := config["agent_args_override"].(map[string]interface{})
	if !reflect.DeepEqual(args["claude"], []interface{}{"--strict-mcp-config"}) {
		t.Fatalf("Claude arguments = %v", args["claude"])
	}
	chain := testPolicy(t)
	previous, _, err := Render([]byte("{}"), chain)
	if err != nil {
		t.Fatal(err)
	}
	var fixed map[string]interface{}
	if err := yaml.Unmarshal(previous, &fixed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args["codex"], fixed["agent_args_override"].(map[string]interface{})["codex"]) {
		t.Fatalf("Codex arguments = %v, want the version %d gate's", args["codex"], chain.Version)
	}
	again, drift, err := Render(after, versionSix(t, Reviewer{}))
	if err != nil || len(drift) != 0 || string(again) != string(after) {
		t.Fatalf("not idempotent: %v %v", drift, err)
	}
}

func TestVersionSixRendersOneConfigForEveryHarnessAndFallback(t *testing.T) {
	plain, _, err := Render([]byte(handEditedMachineConfig), versionSix(t, Reviewer{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, fallback := range []Reviewer{codexTask, claudeTask} {
		after, drift, err := Render(plain, versionSix(t, fallback))
		if err != nil || len(drift) != 0 || string(after) != string(plain) {
			t.Fatalf("a %s fallback changed the machine config: %v %v", fallback.Harness, drift, err)
		}
	}
}

func TestVersionSixReleasesEveryReviewRoleAndTheCodexExecutable(t *testing.T) {
	before := []byte("agent: [codex, claude]\nreview_agents:\n  reviewer: {agent: codex, model: gpt-6.1-sol, effort: xhigh}\n  fixer: {agent: codex}\nagent_path_override:\n  codex: C:/tools/codex.exe\n  claude: C:/tools/claude.exe\n")
	after, drift, err := Render(before, versionSix(t, Reviewer{}))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]interface{}
	if err := yaml.Unmarshal(after, &config); err != nil {
		t.Fatal(err)
	}
	if _, ok := config["review_agents"]; ok {
		t.Fatalf("a review role stays tied to one harness:\n%s", after)
	}
	if !reflect.DeepEqual(config["agent_path_override"], map[string]interface{}{"claude": "C:/tools/claude.exe"}) {
		t.Fatalf("executable overrides = %v", config["agent_path_override"])
	}
	if !reflect.DeepEqual(config["agent"], []interface{}{"codex", "claude"}) {
		t.Fatalf("the operator's chain changed: %v", config["agent"])
	}
	for _, want := range []string{"review_agents", "agent_path_override.codex"} {
		found := false
		for _, name := range drift {
			found = found || name == want
		}
		if !found {
			t.Fatalf("drift=%v, want %s named", drift, want)
		}
	}
}

func TestVersionSixInstructionSaysTheGateFollowsTheTask(t *testing.T) {
	for _, test := range []struct {
		fallback Reviewer
		want     string
		refuse   string
	}{
		{Reviewer{}, "names no fallback", "Codex"},
		{codexTask, "codex gpt-6.1-sol high", "Claude next"},
	} {
		selection, err := versionSix(t, test.fallback).Select("ordinary")
		if err != nil {
			t.Fatal(err)
		}
		instruction := selection.Instruction("task", "snapshot")
		if !strings.Contains(instruction, "this task's own harness") || !strings.Contains(instruction, test.want) || strings.Contains(instruction, test.refuse) {
			t.Fatalf("instruction = %s", instruction)
		}
	}
}

func TestEveryOlderFrozenPolicyMigratesToVersionSix(t *testing.T) {
	current := versionSix(t, Reviewer{})
	codex := Reviewer{"codex", "gpt-6.1-sol", "xhigh"}
	older := map[string]Policy{
		"v1": {Version: 1, Reviewer: Reviewer{"claude", "opus", "high"}, AutoFix: current.AutoFix, Classes: current.Classes},
		"v2": {Version: 2, Primary: Reviewer{"codex", "gpt-5.6-sol", "high"}, Reviewer: Reviewer{"codex", "gpt-5.6-sol", "high"}, Fixer: Reviewer{"codex", "gpt-5.6-sol", "high"}, AutoFix: current.AutoFix, Classes: current.Classes},
		"v3": {Version: 3, Primary: codex, Reviewer: codex, Fixer: codex, AutoFix: current.AutoFix, Classes: current.Classes},
		"v4": {Version: 4, Primary: codex, Fallback: Reviewer{Harness: "claude"}, AutoFix: current.AutoFix, Classes: current.Classes},
		"v5": {Version: 5, Primary: codex, Fallback: Reviewer{Harness: "claude"}, AutoFix: current.AutoFix, Classes: current.Classes},
	}
	for name, from := range older {
		for _, class := range []string{"ordinary", "high-risk", "mechanical"} {
			t.Run(name+"/"+class, func(t *testing.T) {
				old, err := from.Select(class)
				if err != nil {
					t.Fatalf("a task frozen at %s no longer loads: %v", name, err)
				}
				migrated, err := MigrateSelection(old, current)
				if err != nil {
					t.Fatal(err)
				}
				if migrated.Policy != current || migrated.Class != old.Class || migrated.ReviewCycles != old.ReviewCycles || migrated.Hash == old.Hash {
					t.Fatalf("migrated=%+v old=%+v", migrated, old)
				}
				if _, err := MigrateSelection(migrated, from); err == nil {
					t.Fatal("backward migration accepted")
				}
			})
		}
	}
}

func TestVersionSixTaskTakesANewlyNamedFallbackByMigration(t *testing.T) {
	old, err := versionSix(t, Reviewer{}).Select("high-risk")
	if err != nil {
		t.Fatal(err)
	}
	named := versionSix(t, codexTask)
	migrated, err := MigrateSelection(old, named)
	if err != nil {
		t.Fatalf("a version 6 task cannot take the fallback the operator named since: %v", err)
	}
	if migrated.Policy != named || migrated.Class != "high-risk" || migrated.ReviewCycles != old.ReviewCycles || migrated.Hash == old.Hash {
		t.Fatalf("migrated=%+v old=%+v", migrated, old)
	}
	if same, err := MigrateSelection(migrated, named); err != nil || same != migrated {
		t.Fatalf("the same policy is not a no-op: %+v %v", same, err)
	}
}
