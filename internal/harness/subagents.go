package harness

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/routing"
)

var subagentDescriptions = map[string]string{
	"search":    "Search, read and summarize a bounded question. Return evidence without editing files.",
	"review":    "Review code and design for correctness, risk and missing coverage. Return findings without editing files.",
	"verify":    "Independently verify completed work and test results. Return evidence and failures without editing files.",
	"implement": "Implement one bounded slice from the brief the lead hands over. Edit files and run the slice's tests, but never stage, commit or push. Return what changed and what ran.",
}

// implementReview is what the lead does with a slice's work before it
// commits, since only the lead commits on the branch.
const implementReview = " The worker edits and runs the slice's tests but never stages, commits or pushes: read its git diff and run those tests yourself before you commit, and send back or fix what falls short. Keep planning, integration and every commit yourself."

// Each selection has its own directory, so planning a switch never rewrites
// the agent files a still-running harness reads.
func configureSubagents(launch Launch, kind Kind, spec LaunchSpec) (Launch, error) {
	isImplementHandedOff := spec.Implement.Model != ""
	if len(spec.Subagents) == 0 && !isImplementHandedOff {
		return launch, nil
	}
	lanes := map[string]routing.ExecutionLane{}
	maps.Copy(lanes, spec.Subagents)
	var roles []string
	if len(spec.Subagents) > 0 {
		roles = []string{"search", "review", "verify"}
	}
	if isImplementHandedOff {
		lanes["implement"] = spec.Implement
		roles = append(roles, "implement")
	}
	data, err := json.Marshal(lanes)
	if err != nil {
		return Launch{}, err
	}
	directory := filepath.Join(spec.TaskTmp, "subagents", fmt.Sprintf("%x", sha256.Sum256(data))[:16])
	var instruction string
	for _, role := range roles {
		lane, ok := lanes[role]
		if !ok || lane.Model == "" || !validSharedEffort(lane.Effort) {
			return Launch{}, fmt.Errorf("harness: subagent %s requires a model and supported effort", role)
		}
		description := subagentDescriptions[role]
		if kind == Codex {
			if role == "implement" {
				continue
			}
			job := role
			if role == "search" {
				job = "search, reading and summaries"
			}
			instruction += fmt.Sprintf(" For %s work use spawn_agent with model %s, reasoning_effort %s and fork_turns none.", job, lane.Model, lane.Effort)
			continue
		}
		path := filepath.Join(directory, ".claude", "agents", "ceo-"+role+".md")
		content := fmt.Sprintf("---\nname: ceo-%s\ndescription: %s\nmodel: %s\neffort: %s\n---\n%s\n", role, strconv.Quote(description), strconv.Quote(lane.Model), strconv.Quote(lane.Effort), description)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return Launch{}, err
		}
		if err := fsx.AtomicWriteFile(path, []byte(content)); err != nil {
			return Launch{}, err
		}
	}
	if kind == Claude {
		launch.Args = append(launch.Args, "--add-dir", directory)
		if len(spec.Subagents) > 0 {
			instruction = " Use ceo-search for search, reading and summaries, ceo-review for review, and ceo-verify for verification. Keep their configured models."
		}
	} else if len(spec.Subagents) > 0 {
		search := spec.Subagents["search"]
		launch.Args = append(launch.Args, "-c", "agents.default_subagent_model="+search.Model, "-c", "agents.default_subagent_reasoning_effort="+search.Effort)
	}
	if len(spec.Subagents) > 0 {
		instruction += " Verification must be independent and must not implement the work it verifies."
	}
	if isImplementHandedOff {
		if kind == Claude {
			instruction += " Hand each bounded implementation slice to ceo-implement, with the slice's brief alone."
		} else {
			instruction += fmt.Sprintf(" Hand each bounded implementation slice to a worker: spawn_agent with model %s, reasoning_effort %s and fork_turns none, with the slice's brief alone.", spec.Implement.Model, spec.Implement.Effort)
		}
		instruction += implementReview
	}
	launch.SubagentInstruction = " Route native subagents by their job." + instruction
	return launch, nil
}
