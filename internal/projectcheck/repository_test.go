package projectcheck

import (
	"context"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// countingRunner runs every process as the operating system would and keeps
// the arguments and the output of each.
type countingRunner struct {
	args   [][]string
	output []string
}

func (r *countingRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	result, err := execx.OSRunner{}.Run(ctx, request)
	r.args = append(r.args, request.Args)
	r.output = append(r.output, string(result.Stdout))
	return result, err
}

// A check read a tracked file with a git show of its own each time a part
// of it asked for the file, and named its commit twice, once short and once
// in full. A local run of the package's tests started 2,166 git processes
// for its 205 checks, 327 of them a file read again or the commit named
// again, and a check's time is almost all git processes (#1114). A check
// now starts one git process for each tracked file it reads and one to
// name its commit.
func TestACheckStartsOneGitProcessForEachFileItReadsAndOneToNameItsCommit(t *testing.T) {
	t.Parallel()
	// Arrange
	f := newFixture(t, map[string]string{
		".no-mistakes.yaml":        "commands:\n  test: \"npm test\"\n",
		"package.json":             `{"scripts":{"test":"node --test"},"dependencies":{"dotenv":"16.0.0"}}`,
		"AGENTS.md":                "Test with `npm test`.\n",
		".github/workflows/ci.yml": "on: pull_request\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npm test\n",
		".env.example":             "API_KEY=\n",
		"src/index.js":             "require('dotenv').config()\nconsole.log(process.env.API_KEY)\n",
	})
	f.origin()
	short, full := f.output("rev-parse", "--short", "origin/main"), f.output("rev-parse", "origin/main")
	runner := &countingRunner{}
	options := f.options("npm", "node")
	options.Runner = runner

	// Act
	f.run(options)

	// Assert
	shown := map[string]int{}
	var named []string
	for i, args := range runner.args {
		if args[0] == "show" {
			shown[strings.TrimPrefix(args[1], "origin/main:")]++
		}
		for _, line := range strings.Split(runner.output[i], "\n") {
			if line = strings.TrimSpace(line); line == short || line == full {
				named = append(named, strings.Join(args, " "))
				break
			}
		}
	}
	for _, name := range []string{".no-mistakes.yaml", "package.json", "AGENTS.md", ".github/workflows/ci.yml"} {
		if shown[name] == 0 {
			t.Errorf("the check never read %s, which this test counts the reads of", name)
		}
	}
	for _, name := range sortedKeys(shown) {
		if shown[name] > 1 {
			t.Errorf("the check started %d git processes to read %s, want 1", shown[name], name)
		}
	}
	if len(named) != 1 {
		t.Errorf("%d git processes named the commit read, want 1: %s", len(named), strings.Join(named, "; "))
	}
}
