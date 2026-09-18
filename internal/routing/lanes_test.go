package routing

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadReadsTheLaneTableBesideTheRules(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{
		"rules":[{"fault":"auth","switch":{"harness":"claude"}}],
		"default_lane":"build","escalate_to":"deep",
		"lanes":{
			"deep":{"harness":"claude","model":"fable","effort":"xhigh","note":"high risk"},
			"build":{"harness":"claude","model":"opus","effort":"high","note":"ordinary"}
		}
	}`)
	policy, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(policy.Rules) != 1 || len(policy.Lanes) != 2 || policy.DefaultLane != "build" || policy.EscalateTo != "deep" {
		t.Fatalf("policy = %+v, want rules and lanes side by side", policy)
	}
	table, err := policy.LaneTable()
	if err != nil {
		t.Fatalf("LaneTable: %v", err)
	}
	if table.Source != "fleet table "+filepath.Join(dir, FileName) {
		t.Errorf("source = %q, want the fleet table named with its path", table.Source)
	}
	deep := table.Lanes["deep"]
	if deep.Name != "deep" || deep.Harness != "claude" || deep.Model != "fable" || deep.Effort != "xhigh" || deep.Note != "high risk" {
		t.Errorf("deep = %+v", deep)
	}
}

func TestLaneTableRefusesATableASpawnCannotRouteThrough(t *testing.T) {
	cases := []struct{ name, json, want string }{
		{"lane without harness", `{"rules":[],"default_lane":"build","lanes":{"build":{"model":"opus"}}}`, `lane "build" has no harness`},
		{"lanes without default", `{"rules":[],"lanes":{"build":{"harness":"claude"}}}`, "lanes are defined but default_lane is not"},
		{"undefined default", `{"rules":[],"default_lane":"nope","lanes":{"build":{"harness":"claude"}}}`, `default_lane "nope" is not a defined lane`},
		{"undefined escalation", `{"rules":[],"default_lane":"build","escalate_to":"nope","lanes":{"build":{"harness":"claude"}}}`, `escalate_to "nope" is not a defined lane`},
	}
	for _, test := range cases {
		dir := t.TempDir()
		write(t, dir, test.json)
		policy, err := Load(dir)
		if err != nil {
			t.Fatalf("%s: Load: %v, want the lane table checked only when a spawn routes", test.name, err)
		}
		_, err = policy.LaneTable()
		if want := "routing: " + filepath.Join(dir, FileName) + ": " + test.want; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", test.name, err, want)
		}
	}
}

func TestLoadKeepsTheSwitchRulesWhenTheLaneTableIsInvalid(t *testing.T) {
	// The watcher loads the same file for its fault-switch rules; a lane typo
	// must never switch those off.
	dir := t.TempDir()
	write(t, dir, `{
		"rules":[{"harness":"kimi","fault":"rate-limit","switch":{"harness":"codex"},"auto":true}],
		"default_lane":"buld",
		"lanes":{"build":{"harness":"claude"}}
	}`)
	policy, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := policy.LaneTable(); err == nil {
		t.Fatal("premise: the lane table should be invalid")
	}
	rule, ok := policy.Match("kimi", RateLimit)
	if !ok || rule.Switch.Harness != "codex" || !rule.Auto {
		t.Errorf("Match = (%+v, %v), want the standing kimi rate-limit rule", rule, ok)
	}
}

func TestLoadKeepsAcceptingARulesOnlyPolicy(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"rules":[{"fault":"rate-limit","switch":{"harness":"codex"}}]}`)
	policy, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	table, err := policy.LaneTable()
	if err != nil || len(policy.Lanes) != 0 || len(table.Lanes) != 0 {
		t.Errorf("policy = %+v table = %+v err = %v, want no lanes", policy, table, err)
	}
}

// The table that ships in data/routing.json is the Overlord's direction of
// 2026-09-17; this pins it, with the fault rules untouched beside it.
func TestShippedRoutingTableMatchesTheDirection(t *testing.T) {
	policy, err := Load(filepath.Join("..", "..", "data"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if policy.DefaultLane != "build" || policy.EscalateTo != "deep" {
		t.Errorf("default %q escalate %q, want build and deep", policy.DefaultLane, policy.EscalateTo)
	}
	want := map[string]Lane{
		"deep":       {Harness: "claude", Model: "fable", Effort: "xhigh"},
		"build":      {Harness: "claude", Model: "opus", Effort: "high"},
		"mechanical": {Harness: "claude", Model: "sonnet", Effort: "medium"},
		"scout":      {Harness: "claude", Model: "fable", Effort: "xhigh"},
	}
	if len(policy.Lanes) != len(want) {
		t.Fatalf("lanes = %v, want exactly %d", policy.Lanes, len(want))
	}
	for name, lane := range want {
		got := policy.Lanes[name]
		if got.Harness != lane.Harness || got.Model != lane.Model || got.Effort != lane.Effort {
			t.Errorf("lane %s = %+v, want %+v", name, got, lane)
		}
		if got.Note == "" {
			t.Errorf("lane %s has no note saying what it is for", name)
		}
	}
	if len(policy.Rules) != 2 || policy.Rules[0].Harness != "kimi" || policy.Rules[0].Fault != RateLimit || policy.Rules[1].Fault != Auth {
		t.Errorf("rules = %+v, want the two standing switch rules unchanged", policy.Rules)
	}
}
