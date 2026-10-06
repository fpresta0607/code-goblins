package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/install"
)

// Install wires in the per-user home wherever it runs, a checkout included,
// unless the user's CFO_HOME names a home a fleet lives in, which it keeps;
// a CFO_HOME naming a folder with no fleet does not decide it.
func TestInstallTargetIsThePerUserHomeWhereverItRunsUnlessAHomeIsInUse(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	environment := filepath.Join(t.TempDir(), "user-env.json")
	t.Setenv(install.UserEnvFileVariable, environment)
	stale := t.TempDir()
	if err := os.WriteFile(environment, []byte(`{"CFO_HOME": `+strconv.Quote(stale)+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	checkout := fakeSourceCheckout(t)
	want := filepath.Join(local, "CodeGoblins")
	for name, dir := range map[string]string{"a checkout": checkout, "a folder inside a checkout": filepath.Join(checkout, "cmd"), "any other folder": t.TempDir()} {
		t.Chdir(dir)
		target, err := installTarget()
		if err != nil || !strings.EqualFold(target.Root, want) || target.Kept {
			t.Errorf("%s: installTarget = %+v, %v; want the per-user home %q", name, target, err, want)
		}
	}

	if err := os.MkdirAll(filepath.Join(checkout, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(environment, []byte(`{"CFO_HOME": `+strconv.Quote(checkout)+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if target, err := installTarget(); err != nil || !strings.EqualFold(target.Root, checkout) || !target.Kept || !target.Checkout {
		t.Errorf("with CFO_HOME at a checkout holding a fleet: installTarget = %+v, %v; want that checkout kept", target, err)
	}

	t.Setenv("LOCALAPPDATA", "")
	if target, err := installTarget(); err == nil {
		t.Errorf("installTarget without LOCALAPPDATA = %+v, want a refusal", target)
	}
}

// fakeSourceCheckout is a code-goblins checkout with everything committed.
func fakeSourceCheckout(t *testing.T) string {
	t.Helper()
	checkout := t.TempDir()
	for _, file := range []string{"AGENTS.md", filepath.Join("cmd", "cfo", "main.go"), ".gitignore"} {
		path := filepath.Join(checkout, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("source\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "source"}} {
		if out, err := exec.Command("git", append([]string{"-C", checkout}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return checkout
}

// installedTree runs cfo install from dir on a machine of its own, a fresh
// profile with a file standing in for the user environment, and returns
// every file the install wrote under the per-user home, by its path there,
// with its content hash, and the user environment it recorded with the
// machine's own folders written as placeholders.
func installedTree(t *testing.T, dir string) (map[string]string, map[string]string) {
	t.Helper()
	machine := t.TempDir()
	profile := filepath.Join(machine, "profile")
	local := filepath.Join(profile, "AppData", "Local")
	for _, folder := range []string{local, filepath.Join(profile, ".claude")} {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	envFile := filepath.Join(machine, "user-env.json")
	t.Setenv(install.UserEnvFileVariable, envFile)
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", filepath.Join(profile, "AppData", "Roaming"))
	t.Setenv("USERPROFILE", profile)
	t.Setenv("HOME", profile)
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "CFO_HOME", "CFO_STATE_OVERRIDE", "CFO_PROJECTS_ROOT"} {
		t.Setenv(name, "")
	}
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	if code := runInstall(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("cfo install from %s exited %d:\n%s%s", dir, code, stdout.String(), stderr.String())
	}

	root := filepath.Join(local, "CodeGoblins")
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if filepath.ToSlash(rel) == "state/harnesses.json" {
			// The map names this machine's own folders and when it was
			// written; its shape is what must match.
			data = []byte(strings.ReplaceAll(strings.ReplaceAll(string(data), strings.ReplaceAll(profile, `\`, `\\`), "<profile>"), "\r", ""))
			data = []byte(strings.Join(dropLines(strings.Split(string(data), "\n"), `"written"`), "\n"))
		}
		sum := sha256.Sum256(data)
		tree[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{"bin", "state", "data", "worktrees", "scratch", "caches"} {
		if info, err := os.Stat(filepath.Join(root, folder)); err != nil || !info.IsDir() {
			t.Errorf("the home has no %s folder: %v", folder, err)
		}
	}
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), strings.ReplaceAll(profile, `\`, `\\`), "<profile>"), "\n") {
		if name, value, ok := strings.Cut(strings.TrimSpace(line), ": "); ok {
			env[strings.Trim(name, `"`)] = strings.TrimSuffix(value, ",")
		}
	}
	return tree, env
}

func dropLines(lines []string, containing string) []string {
	kept := lines[:0]
	for _, line := range lines {
		if !strings.Contains(line, containing) {
			kept = append(kept, line)
		}
	}
	return kept
}

// The Overlord's desktop install and a cfo install run from a code-goblins
// checkout set up the same home, in the same place, and the checkout gains
// nothing: a source checkout is never a home.
func TestInstallFromACheckoutAndFromTheDesktopInstallerMakeTheSameHome(t *testing.T) {
	// Arrange
	checkout := fakeSourceCheckout(t)

	// Act: the desktop installer runs cfo install from a neutral folder.
	fromCheckout, checkoutEnv := installedTree(t, checkout)
	fromInstaller, installerEnv := installedTree(t, t.TempDir())

	// Assert
	if len(fromCheckout) == 0 {
		t.Fatal("the install wrote no files")
	}
	var differ []string
	for path, sum := range fromCheckout {
		if fromInstaller[path] != sum {
			differ = append(differ, path)
		}
	}
	for path := range fromInstaller {
		if _, ok := fromCheckout[path]; !ok {
			differ = append(differ, path)
		}
	}
	sort.Strings(differ)
	if len(differ) > 0 {
		t.Errorf("the two homes differ in %v", differ)
	}
	for _, want := range []string{"bin/cfo.exe", "bin/goblins.exe", "AGENTS.md", ".cfo-home", "state/harnesses.json", "config/pipeline.json", "data/routing.json"} {
		if _, ok := fromCheckout[want]; !ok {
			t.Errorf("the home has no %s", want)
		}
	}
	for path := range fromCheckout {
		if strings.HasPrefix(path, ".agents/") || strings.HasPrefix(path, ".claude/") || path == "cfo.exe" || path == "goblins.exe" {
			t.Errorf("the home holds %s, which belongs in bin or the shared skills folder", path)
		}
	}
	if checkoutEnv["Path"] != installerEnv["Path"] || checkoutEnv["CFO_HOME"] != installerEnv["CFO_HOME"] || !strings.HasSuffix(checkoutEnv["Path"], `CodeGoblins\\bin"`) {
		t.Errorf("user environment from a checkout %v, from the installer %v; want both the same with the home's bin on PATH", checkoutEnv, installerEnv)
	}
	status, err := exec.Command("git", "-C", checkout, "status", "--porcelain", "--untracked-files=all", "--ignored").CombinedOutput()
	if err != nil || len(bytes.TrimSpace(status)) != 0 {
		t.Errorf("the checkout's git status after install = %q, %v; want nothing new", status, err)
	}
}
