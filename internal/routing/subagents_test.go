package routing

import (
	"path/filepath"
	"testing"
)

func TestSubagentsRouteByJobAndKeepVerificationIndependent(t *testing.T) {
	policy, err := Load(filepath.Join("..", "..", "data"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ harness, parent, search, verify string }{
		{"codex", "gpt-6-astra", "gpt-6-luna", "gpt-6-sol"},
		{"codex", "gpt-6-sol", "gpt-6-luna", "gpt-6-astra"},
		{"claude", "claude-opus-5-5", "haiku", "sonnet"},
		{"claude", "sonnet", "haiku", "claude-opus-5-5"},
	} {
		t.Run(test.harness+test.parent, func(t *testing.T) {
			routes, err := policy.SubagentRoutes(test.harness, test.parent)
			if err != nil || routes["search"].Model != test.search || routes["verify"].Model != test.verify || routes["review"].Effort != "xhigh" {
				t.Fatalf("routes=%+v err=%v", routes, err)
			}
		})
	}
}

func TestSubagentsRefuseVerificationWithoutKnownParentModel(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"default_lane":"build","lanes":{"build":{"harness":"claude","model":"opus"}},"subagents":{"claude":{"search":["build"],"review":["build"],"verify":["build"]}}}`)
	policy, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"", "default"} {
		if _, err := policy.SubagentRoutes("claude", model); err == nil {
			t.Errorf("verification of %q accepted without a known parent model", model)
		}
	}
}

func TestSubagentsAcceptUniformConfiguredModel(t *testing.T) {
	for _, harness := range []string{"codex", "claude"} {
		t.Run(harness, func(t *testing.T) {
			policy := Policy{DefaultLane: "build", Lanes: map[string]Lane{"build": {Harness: harness, Model: "selected-model", Effort: "xhigh"}}, Subagents: map[string]map[string][]string{harness: {"search": {"build"}, "review": {"build"}, "verify": {"build"}}}}
			routes, err := policy.SubagentRoutes(harness, "selected-model")
			if err != nil || routes["verify"].Model != "selected-model" {
				t.Fatalf("routes=%+v err=%v, want the configured model", routes, err)
			}
		})
	}
}
