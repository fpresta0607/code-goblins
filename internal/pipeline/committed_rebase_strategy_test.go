package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Issue 1949: a goblin keeps its published pull request current by merging
// main in, since published commits are never rebased or force-pushed. The
// gate's Rebase step integrates a moved main the way this repository's
// trusted .no-mistakes.yaml names in rebase.strategy. Replaying the branch
// drops the resolution its merge of main carried, so it conflicts on a file
// main changed after that merge even though the branch head merges cleanly,
// and only a force-push gets past it. Merging main in keeps the published head
// as the first parent, so the run goes on, and still stops on a real conflict.
func TestCommittedRepoConfigIntegratesAMovedBaseWithoutReplayingPublishedCommits(t *testing.T) {
	requireFixtureTools(t, "git")
	data, err := os.ReadFile(filepath.Join("..", "..", ".no-mistakes.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Rebase struct {
			Strategy string `yaml:"strategy"`
		} `yaml:"rebase"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name          string
		laterMainLine string
		isConflict    bool
	}{
		{name: "main later changes a line the branch never touched", laterMainLine: "g", isConflict: false},
		{name: "main later changes the line the branch's merge resolved", laterMainLine: "b", isConflict: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: the branch published a change to README.md, main changed
			// the same line, and the goblin merged main in with a resolution.
			work := t.TempDir()
			runPipelineGit(t, work, "init", "--quiet", "--initial-branch=main")
			for _, setting := range [][2]string{{"user.name", "Fixture"}, {"user.email", "fixture@example.invalid"}, {"core.autocrlf", "false"}, {"commit.gpgsign", "false"}} {
				runPipelineGit(t, work, "config", setting[0], setting[1])
			}
			readme := map[string]string{"a": "a", "b": "b", "c": "c", "d": "d", "e": "e", "f": "f", "g": "g"}
			commitReadme := func(line, text string, extraArgs ...string) {
				readme[line] = text
				var content strings.Builder
				for _, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
					content.WriteString(readme[name] + "\n")
				}
				writeFixtureFile(t, filepath.Join(work, "README.md"), content.String())
				runPipelineGit(t, work, "add", "README.md")
				runPipelineGit(t, work, append([]string{"commit", "--quiet", "-m", line + " " + text}, extraArgs...)...)
			}
			commitReadme("a", "a")
			runPipelineGit(t, work, "switch", "--quiet", "-c", "fix/published")
			commitReadme("b", "b-branch")
			runPipelineGit(t, work, "switch", "--quiet", "main")
			commitReadme("b", "b-main")
			mainReadme := map[string]string{}
			for name, text := range readme {
				mainReadme[name] = text
			}
			runPipelineGit(t, work, "switch", "--quiet", "fix/published")
			if err := exec.Command("git", "-C", work, "merge", "--quiet", "--no-edit", "main").Run(); err == nil {
				t.Fatal("fixture merge of main did not conflict")
			}
			commitReadme("b", "b-both", "--no-edit")
			published := runPipelineGit(t, work, "rev-parse", "HEAD")
			runPipelineGit(t, work, "switch", "--quiet", "main")
			readme = mainReadme
			commitReadme(scenario.laterMainLine, readme[scenario.laterMainLine]+"-later")
			runPipelineGit(t, work, "switch", "--quiet", "fix/published")

			// Act: integrate main the way the committed strategy says the
			// gate's Rebase step does.
			integration := []string{"rebase", "--quiet", "main"}
			abort := []string{"rebase", "--abort"}
			if config.Rebase.Strategy == "merge" {
				integration = []string{"merge", "--quiet", "--no-ff", "--no-edit", "main"}
				abort = []string{"merge", "--abort"}
			}
			output, err := exec.Command("git", append([]string{"-C", work}, integration...)...).CombinedOutput()
			isConflict := err != nil
			if isConflict {
				runPipelineGit(t, work, abort...)
			}

			// Assert
			if isConflict != scenario.isConflict {
				t.Fatalf("rebase.strategy %q: conflict = %t, want %t: %s", config.Rebase.Strategy, isConflict, scenario.isConflict, output)
			}
			if isConflict {
				return
			}
			for _, ancestor := range []string{published, "main"} {
				if err := exec.Command("git", "-C", work, "merge-base", "--is-ancestor", ancestor, "HEAD").Run(); err != nil {
					t.Fatalf("rebase.strategy %q: integrated head does not keep %s, so publishing it needs a force-push: %v", config.Rebase.Strategy, ancestor, err)
				}
			}
		})
	}
}
