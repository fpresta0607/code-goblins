package install

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/harnessmap"
)

func TestRoutedContractReachesEveryHarnessOnInstallAndUpdate(t *testing.T) {
	// Arrange
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	skills, err := fs.Sub(codegoblins.Skills, ".agents/skills")
	if err != nil {
		t.Fatal(err)
	}
	f.service.Skills = skills
	updated := fstest.MapFS{}
	if err := fs.WalkDir(codegoblins.Contract, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(codegoblins.Contract, name)
		if err == nil {
			updated[name] = &fstest.MapFile{Data: append(data, []byte("\nUpdated contract fixture.\n")...)}
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	updatedSkills := fstest.MapFS{}
	if err := fs.WalkDir(skills, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(skills, name)
		if err == nil {
			updatedSkills[name] = &fstest.MapFile{Data: append(data, []byte("\nUpdated skill fixture.\n")...)}
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}

	for _, phase := range []string{"install", "update"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "update" {
				f.service.Contract = updated
				f.service.Skills = updatedSkills
				writeFile(t, filepath.Join(f.root, "docs", "contract", "delivery.md"), "stale procedure")
				writeFile(t, filepath.Join(f.service.Harnesses.SharedSkills, "cfo-delivery", "SKILL.md"), "stale skill")
			}

			// Act
			f.install()

			// Assert
			if err := fs.WalkDir(f.service.Contract, ".", func(name string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				want, err := fs.ReadFile(f.service.Contract, name)
				if err == nil && readFile(t, filepath.Join(f.root, filepath.FromSlash(name))) != string(want) {
					t.Errorf("%s did not receive %s", phase, name)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			mapping, err := harnessmap.Read(filepath.Join(f.root, "state"))
			if err != nil {
				t.Fatal(err)
			}
			entries, err := fs.ReadDir(skills, ".")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "cfo-") && entry.Name() != "stow" {
					continue
				}
				if !slices.Contains(mapping.Installed, entry.Name()) {
					t.Errorf("%s is not discoverable in the recorded harness map", entry.Name())
				}
				want, err := fs.ReadFile(f.service.Skills, entry.Name()+"/SKILL.md")
				if err != nil {
					t.Fatal(err)
				}
				shared := filepath.Join(mapping.SharedSkills, entry.Name(), "SKILL.md")
				for _, harness := range mapping.Roots {
					target := shared
					if harness.Harness == "claude" {
						target = filepath.Join(harness.Skills, entry.Name(), "SKILL.md")
					}
					if readFile(t, target) != string(want) {
						t.Errorf("%s did not receive the shared %s entry point", harness.Harness, entry.Name())
					}
					sharedInfo, err := os.Stat(shared)
					if err != nil {
						t.Fatal(err)
					}
					targetInfo, err := os.Stat(target)
					if err != nil || !os.SameFile(sharedInfo, targetInfo) {
						t.Errorf("%s reads a different policy file: %v", harness.Harness, err)
					}
				}
			}
		})
	}
}

func TestLegacyTrackedHomeKeepsItsLoadedContractAndDirtyPolicy(t *testing.T) {
	// Arrange
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	f.service.Checkout = true
	f.env.values["CFO_HOME"] = f.root
	tracked := map[string]string{
		"AGENTS.md":            "# Stale tracked contract\n",
		"CLAUDE.md":            "Read [AGENTS.md](AGENTS.md).\n",
		"config/pipeline.json": "original pipeline\n",
		"data/routing.json":    "original routing\n",
	}
	for name, data := range tracked {
		writeFile(t, filepath.Join(f.root, filepath.FromSlash(name)), data)
	}
	gitIn(t, f.root, "init", "-q")
	gitIn(t, f.root, "config", "core.autocrlf", "false")
	gitIn(t, f.root, "add", "AGENTS.md", "CLAUDE.md", "config/pipeline.json", "data/routing.json")
	gitIn(t, f.root, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "fixture")
	for _, name := range []string{"config/pipeline.json", "data/routing.json"} {
		writeFile(t, filepath.Join(f.root, filepath.FromSlash(name)), "dirty local policy\n")
	}
	diff := func() string {
		t.Helper()
		data, err := exec.Command("git", "-C", f.root, "diff", "--binary", "--", "AGENTS.md", "CLAUDE.md", "config/pipeline.json", "data/routing.json").CombinedOutput()
		if err != nil {
			t.Fatalf("read fixture diff: %v: %s", err, data)
		}
		return string(data)
	}
	before := diff()
	skills, err := fs.Sub(codegoblins.Skills, ".agents/skills")
	if err != nil {
		t.Fatal(err)
	}
	f.service.Skills = skills

	// Act: both an install and a later update must preserve the tracked home.
	f.install()
	f.install()
	worker := filepath.Join(t.TempDir(), "worker")
	gitIn(t, f.root, "worktree", "add", "-q", "--detach", worker, "HEAD")

	// Assert
	if got := diff(); got != before || before == "" {
		t.Fatalf("the dirty tracked policy changed: before %q, after %q", before, got)
	}
	for _, directory := range []string{f.root, worker} {
		for _, harness := range []string{"claude", "codex", "pi"} {
			if harness == "claude" && readFile(t, filepath.Join(directory, "CLAUDE.md")) != tracked["CLAUDE.md"] {
				t.Error("the Claude entry point no longer routes to the tracked core")
			}
			if got := readFile(t, filepath.Join(directory, "AGENTS.md")); got != tracked["AGENTS.md"] {
				t.Errorf("%s in %s would load %q instead of its tracked contract", harness, directory, got)
			}
		}
	}
	if readFile(t, filepath.Join(worker, "config", "pipeline.json")) != tracked["config/pipeline.json"] {
		t.Error("the worker inherited dirty primary-checkout policy")
	}
	if _, err := os.Stat(filepath.Join(f.root, "docs", "contract", "dispatch.md")); !os.IsNotExist(err) {
		t.Errorf("installation activated tracked contract references: %v", err)
	}
	entries, err := fs.ReadDir(skills, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "cfo-") {
			continue
		}
		want, err := fs.ReadFile(skills, entry.Name()+"/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, filepath.Join(f.service.Harnesses.SharedSkills, entry.Name(), "SKILL.md")); got != string(want) {
			t.Errorf("the tracked home did not receive the shipped %s skill bytes", entry.Name())
		}
	}
}
