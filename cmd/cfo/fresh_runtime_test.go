package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestExecutableFreshRuntimeIncludesInstructionBundle(t *testing.T) {
	repo := repoRootFromCmdCFO(t)
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	exe := buildCFOBinary(t, goBin, repo)
	runtimeRoot := t.TempDir()
	prepare := func() {
		t.Helper()
		h := home.Home{Root: runtimeRoot, State: filepath.Join(runtimeRoot, "state"), Data: filepath.Join(runtimeRoot, "data")}
		if err := prepareRuntimeHome(repo, h, exe); err != nil {
			t.Fatalf("prepare runtime: %v", err)
		}
	}
	prepare()
	for _, rel := range []string{"AGENTS.md", "CLAUDE.md", ".cfo-home", "cfo.exe", "config/pipeline.json", ".agents/skills/lavish/SKILL.md", ".agents/skills/no-mistakes/SKILL.md", ".agents/skills/chrome-devtools-axi/SKILL.md", "docs/pipeline.md"} {
		if info, err := os.Stat(filepath.Join(runtimeRoot, rel)); err != nil || info.Size() == 0 {
			t.Fatalf("fresh instruction pointer missing: %s %v", rel, err)
		}
	}
	memory := filepath.Join(runtimeRoot, "AGENTS.md")
	if err := os.WriteFile(memory, []byte("operator-owned memory"), 0600); err != nil {
		t.Fatal(err)
	}
	prepare()
	preserved, err := os.ReadFile(memory)
	if err != nil || string(preserved) != "operator-owned memory" {
		t.Fatal("operator memory overwritten")
	}
	cmd := exec.Command(filepath.Join(runtimeRoot, "cfo.exe"), "supervisor", "status")
	cmd.Env = cfoTestEnv(t, runtimeRoot, nil)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "supervisor_down") {
		t.Fatalf("fresh runtime falsely healthy: %s %v", out, err)
	}
}
