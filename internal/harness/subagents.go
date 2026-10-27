package harness

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

var subagentDescriptions = map[string]string{
	"search": "Search, read and summarize a bounded question. Return evidence without editing files.",
	"review": "Review code and design for correctness, risk and missing coverage. Return findings without editing files.",
	"verify": "Independently verify completed work and test results. Return evidence and failures without editing files.",
}

// Each selection has its own directory, so planning a switch never rewrites
// the agent files a still-running harness reads.
func configureSubagents(launch Launch, kind Kind, spec LaunchSpec) (Launch, error) {
	if len(spec.Subagents) == 0 {
		return launch, nil
	}
	data, err := json.Marshal(spec.Subagents)
	if err != nil {
		return Launch{}, err
	}
	directory := filepath.Join(spec.TaskTmp, "subagents", fmt.Sprintf("%x", sha256.Sum256(data))[:16])
	for _, role := range []string{"search", "review", "verify"} {
		lane, ok := spec.Subagents[role]
		if !ok || lane.Model == "" || !validSharedEffort(lane.Effort) {
			return Launch{}, fmt.Errorf("harness: subagent %s requires a model and supported effort", role)
		}
		description := subagentDescriptions[role]
		if kind == Codex {
			job := role
			if role == "search" {
				job = "search, reading and summaries"
			}
			launch.SubagentInstruction += fmt.Sprintf(" For %s work use spawn_agent with model %s, reasoning_effort %s and fork_turns none.", job, lane.Model, lane.Effort)
			continue
		}
		path := filepath.Join(directory, ".claude", "agents", "cfo-"+role+".md")
		content := fmt.Sprintf("---\nname: cfo-%s\ndescription: %s\nmodel: %s\neffort: %s\n---\n%s\n", role, strconv.Quote(description), strconv.Quote(lane.Model), strconv.Quote(lane.Effort), description)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return Launch{}, err
		}
		if err := fsx.AtomicWriteFile(path, []byte(content)); err != nil {
			return Launch{}, err
		}
	}
	if kind == Claude {
		launch.Args = append(launch.Args, "--add-dir", directory)
		launch.SubagentInstruction = " Use cfo-search for search, reading and summaries, cfo-review for review, and cfo-verify for verification. Keep their configured models."
	} else {
		search := spec.Subagents["search"]
		launch.Args = append(launch.Args, "-c", "agents.default_subagent_model="+search.Model, "-c", "agents.default_subagent_reasoning_effort="+search.Effort)
	}
	launch.SubagentInstruction = " Route native subagents by their job." + launch.SubagentInstruction + " Verification must be independent and must not implement the work it verifies."
	return launch, nil
}
