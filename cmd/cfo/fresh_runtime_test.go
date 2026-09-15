package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutableFreshRuntimeIncludesInstructionBundle(t *testing.T) {
	repo := repoRootFromCmdCFO(t)
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	exe := buildCFOBinary(t, goBin, repo)
	runtimeRoot := t.TempDir()
	runInstall := func() {
		t.Helper()
		cmd := exec.Command(exe, "install", "--prepare-only")
		cmd.Dir = repo
		cmd.Env = cfoTestEnv(t, runtimeRoot, nil)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("prepare runtime: %v %s", err, out)
		}
	}
	runInstall()
	for _, rel := range []string{"AGENTS.md", "CLAUDE.md", ".cfo-home", "cfo.exe", "config/pipeline.json", ".agents/skills/lavish/SKILL.md", ".agents/skills/no-mistakes/SKILL.md", ".agents/skills/chrome-devtools-axi/SKILL.md", "docs/pipeline.md"} {
		if info, err := os.Stat(filepath.Join(runtimeRoot, rel)); err != nil || info.Size() == 0 {
			t.Fatalf("fresh instruction pointer missing: %s %v", rel, err)
		}
	}
	memory := filepath.Join(runtimeRoot, "AGENTS.md")
	if err := os.WriteFile(memory, []byte("operator-owned memory"), 0600); err != nil {
		t.Fatal(err)
	}
	runInstall()
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
