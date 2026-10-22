package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/ciparity"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// ciParityLimit bounds reading the parity record's new pairs from GitHub.
const ciParityLimit = 10 * time.Minute

// gitHubRepository is an owner/name, each a GitHub name.
var gitHubRepository = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// runCIRunnerParity runs cfo ci-runner parity: it adds to the repository's
// parity record each job its desktop mirror ran since, beside the hosted
// job it copies, and says where each job stands.
func runCIRunnerParity(args []string, stdout, stderr io.Writer, commands execx.Runner, runtime commandRuntime) int {
	fs := flag.NewFlagSet("cfo ci-runner parity", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repository := fs.String("repo", "", "the private repository the desktop mirror runs for, as owner/name")
	workflow := fs.String("workflow", "desktop-mirror.yml", "the mirror's workflow file")
	runs := fs.Int("runs", 30, "how many of the mirror's newest finished runs to read, 1 to 100")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || !gitHubRepository.MatchString(*repository) || *runs < 1 || *runs > 100 || !strings.HasSuffix(*workflow, ".yml") || strings.ContainsAny(*workflow, `/\`) {
		fmt.Fprintln(stderr, "usage: cfo ci-runner parity --repo <owner/name> [--workflow <file>.yml] [--runs <1 to 100>]")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	folder := watch.CIRunnerFolder(h.State)
	path := ciparity.Path(folder, *repository)
	record, err := ciparity.Read(path, *repository)
	if err != nil {
		fmt.Fprintf(stderr, "cfo ci-runner parity: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), ciParityLimit)
	defer cancel()
	pairs, collectErr := ciparity.Collect(ctx, ghParity{commands: commands, repository: *repository, workflow: *workflow}, record, *runs, folder)
	record.Pairs = append(record.Pairs, pairs...)
	if len(pairs) > 0 {
		if err := ciparity.Write(path, record); err != nil {
			fmt.Fprintf(stderr, "cfo ci-runner parity: keep the record: %v\n", err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "cfo ci-runner parity: %s: %d new pair(s), %d in the record %s\n", *repository, len(pairs), len(record.Pairs), path)
	for _, standing := range ciparity.Standings(record) {
		line := fmt.Sprintf("%s: %d run(s) in a row matched, of %d pair(s)", standing.Job, standing.Streak, standing.Pairs)
		if standing.Lost > 0 {
			line += fmt.Sprintf(", %d desktop run(s) lost", standing.Lost)
		}
		if standing.Difference != "" {
			line += fmt.Sprintf("; newest difference, CI run %d: %s", standing.DifferedOn, standing.Difference)
		}
		if standing.IsReady() {
			line += "; ready: its hosted copy may retire, on his word"
		}
		fmt.Fprintln(stdout, line)
	}
	if collectErr != nil {
		fmt.Fprintf(stderr, "cfo ci-runner parity: %v; the pairs read before it are kept\n", collectErr)
		return 1
	}
	return 0
}

// ghParity reads the parity record's runs, jobs and logs through gh.
type ghParity struct {
	commands   execx.Runner
	repository string
	workflow   string
}

func (g ghParity) MirrorRuns(ctx context.Context, limit int) ([]ciparity.Run, error) {
	var answer struct {
		Runs []ciparity.Run `json:"workflow_runs"`
	}
	err := g.api(ctx, fmt.Sprintf("repos/%s/actions/workflows/%s/runs?status=completed&per_page=%d", g.repository, g.workflow, limit), &answer)
	return answer.Runs, err
}

func (g ghParity) Jobs(ctx context.Context, run int64) ([]ciparity.Job, error) {
	var answer struct {
		Jobs []ciparity.Job `json:"jobs"`
	}
	err := g.api(ctx, fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", g.repository, run), &answer)
	return answer.Jobs, err
}

func (g ghParity) Log(ctx context.Context, job int64) (string, error) {
	out, err := g.gh(ctx, fmt.Sprintf("repos/%s/actions/jobs/%d/logs", g.repository, job))
	return string(out), err
}

func (g ghParity) api(ctx context.Context, path string, into any) error {
	out, err := g.gh(ctx, path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, into); err != nil {
		return fmt.Errorf("gh answered %s in a shape that cannot be read: %w", path, err)
	}
	return nil
}

func (g ghParity) gh(ctx context.Context, path string) ([]byte, error) {
	result, err := g.commands.Run(ctx, execx.Request{Name: "gh", Args: []string{"api", path}})
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, errors.New("gh api " + path + ": " + strings.TrimSpace(string(result.Stderr)))
	}
	return result.Stdout, nil
}
