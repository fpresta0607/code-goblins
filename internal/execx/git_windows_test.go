package execx

import (
	"bytes"
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// gitForWindows is the folder Git for Windows is installed in, with its
// launcher in cmd and the git it starts in mingw64\bin, or the test is
// skipped on a machine without it.
func gitForWindows(t *testing.T) string {
	t.Helper()
	folder := filepath.Join(os.Getenv("ProgramFiles"), "Git")
	for _, program := range []string{filepath.Join(folder, "cmd", "git.exe"), filepath.Join(folder, "mingw64", "bin", "git.exe")} {
		if _, err := os.Stat(program); err != nil {
			t.Skipf("Git for Windows is not installed at %s: %v", folder, err)
		}
	}
	return folder
}

// signInEnvironment is this process's environment as a program a sign-in
// starts has it, as the board has it: PATH holds Git for Windows' launcher
// folder and Windows' own, and nothing a Git Bash shell set.
func signInEnvironment(t *testing.T, git string) []string {
	t.Helper()
	path := strings.Join([]string{filepath.Join(git, "cmd"), filepath.Join(os.Getenv("SystemRoot"), "System32"), os.Getenv("SystemRoot")}, string(os.PathListSeparator))
	t.Setenv("PATH", path)
	shell := []string{"MSYSTEM", "HOME", "MINGW_PREFIX", "MINGW_CHOST", "MSYSTEM_PREFIX", "MSYSTEM_CHOST", "EXEPATH", "PLINK_PROTOCOL", "ORIGINAL_PATH", "SHELL", "TERM", "LC_CTYPE"}
	env := slices.DeleteFunc(os.Environ(), func(variable string) bool {
		name, _, _ := strings.Cut(variable, "=")
		return slices.ContainsFunc(shell, func(drop string) bool { return strings.EqualFold(name, drop) }) || strings.HasPrefix(strings.ToUpper(name), "GIT_")
	})
	return env
}

func runGit(t *testing.T, program, dir string, env []string, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(program, args...)
	cmd.Dir, cmd.Env, cmd.Stdin = dir, env, strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v: %s", program, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// Git for Windows' launcher, cmd\git.exe, which PATH names, only starts
// mingw64\bin\git.exe and waits for it, so every git command was two
// processes. A fleet program starts the git the launcher would start, one
// process for the command.
func TestGitStartsAsOneProcessWhereTheLauncherStartsTwo(t *testing.T) {
	// Arrange
	git := gitForWindows(t)
	env := signInEnvironment(t, git)
	count := func(cmd *exec.Cmd) uint32 {
		t.Helper()
		cmd.Env = env
		tree, err := StartTree(cmd)
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
		usage, err := tree.Usage()
		if err != nil {
			t.Fatal(err)
		}
		if err := tree.Release(); err != nil {
			t.Fatal(err)
		}
		return usage.TotalProcesses
	}
	// Named by its path, the launcher runs as it is.
	launched := count(Command(filepath.Join(git, "cmd", "git.exe"), "--version"))

	// Act
	cmd := Command("git", "--version")
	started := count(cmd)

	// Assert
	if launched != 2 {
		t.Fatalf("the launcher started %d processes, want 2: the premise is gone", launched)
	}
	if want := filepath.Join(git, "mingw64", "bin", "git.exe"); !strings.EqualFold(cmd.Path, want) || started != 1 {
		t.Fatalf("git ran as %s in %d processes, want %s in 1", cmd.Path, started, want)
	}
}

// The git the launcher starts finds what the launcher would have put on
// its PATH by itself: a repository's hook, a credential helper written as a
// shell function and Git Credential Manager each run as they do through the
// launcher, from the environment a sign-in gives the board.
func TestTheRealGitRunsHooksAndCredentialHelpersAsTheLauncherDoes(t *testing.T) {
	git := gitForWindows(t)
	launcher := filepath.Join(git, "cmd", "git.exe")
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string, env []string)
		stdin string
		args  []string
		want  func(t *testing.T, dir, out string) string
	}{
		{
			name: "pre-commit hook",
			setup: func(t *testing.T, dir string, env []string) {
				hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
				if err := os.WriteFile(hook, []byte("#!/bin/sh\necho \"hook ran with $(command -v sh)\" >> hook-marker\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			args: []string{"commit", "-q", "--allow-empty", "-m", "hooked"},
			want: func(t *testing.T, dir, _ string) string {
				marker, err := os.ReadFile(filepath.Join(dir, "hook-marker"))
				if err != nil {
					t.Fatal(err)
				}
				return string(marker)
			},
		},
		{
			name: "shell credential helper",
			setup: func(t *testing.T, dir string, env []string) {
				// The helper answers what the environment holds, so no value
				// shaped like a credential is in the source.
				runGit(t, launcher, dir, env, "", "config", "credential.helper", "!f() { echo username=$STAND_IN_ACCOUNT; echo password=$STAND_IN_ANSWER; }; f")
			},
			stdin: "protocol=https\nhost=example.invalid\n\n",
			args:  []string{"credential", "fill"},
			want:  func(_ *testing.T, _, out string) string { return out },
		},
		{
			name: "Git Credential Manager",
			setup: func(t *testing.T, _ string, _ []string) {
				if _, err := os.Stat(filepath.Join(git, "mingw64", "bin", "git-credential-manager.exe")); err != nil {
					t.Skipf("Git Credential Manager is not installed: %v", err)
				}
			},
			args: []string{"credential-manager", "--version"},
			want: func(_ *testing.T, _, out string) string { return out },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			t.Setenv("STAND_IN_ACCOUNT", "fleet")
			t.Setenv("STAND_IN_ANSWER", "not-a-secret")
			env := signInEnvironment(t, git)
			run := func(start func(dir string) *exec.Cmd) string {
				t.Helper()
				dir := t.TempDir()
				runGit(t, launcher, dir, env, "", "init", "-q")
				runGit(t, launcher, dir, env, "", "config", "user.name", "Test")
				runGit(t, launcher, dir, env, "", "config", "user.email", "test@example.invalid")
				test.setup(t, dir, env)
				cmd := start(dir)
				cmd.Dir, cmd.Env, cmd.Stdin = dir, env, strings.NewReader(test.stdin)
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				var out bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &out
				done := make(chan error, 1)
				go func() { done <- cmd.Run() }()
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("%s: %v: %s", strings.Join(cmd.Args, " "), err, out.String())
					}
				case <-ctx.Done():
					t.Fatalf("%s did not finish", strings.Join(cmd.Args, " "))
				}
				return test.want(t, dir, out.String())
			}
			throughLauncher := run(func(string) *exec.Cmd { return exec.Command(launcher, test.args...) })

			// Act
			throughExecx := run(func(string) *exec.Cmd { return Command("git", test.args...) })

			// Assert
			if throughExecx != throughLauncher || strings.TrimSpace(throughExecx) == "" {
				t.Fatalf("through execx git gave %q, through the launcher %q", throughExecx, throughLauncher)
			}
		})
	}
}

// What the real git hands what it starts is what the launcher hands it:
// the same variables, the same values, and the same folders on PATH, the
// launcher's only difference being where it puts the user's own bin.
func TestTheRealGitGivesWhatItStartsTheLaunchersEnvironment(t *testing.T) {
	// Arrange
	git := gitForWindows(t)
	env := signInEnvironment(t, git)
	dir := t.TempDir()
	variables := func(cmd *exec.Cmd) (map[string]string, []string) {
		t.Helper()
		cmd.Dir, cmd.Env = dir, env
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]string{}
		for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
			if name, value, ok := strings.Cut(line, "="); ok {
				values[name] = value
			}
		}
		folders := strings.Split(values["PATH"], ":")
		delete(values, "PATH")
		// The two differ only in where the user's own bin folder sits.
		home := strings.TrimSuffix(values["HOME"], "/") + "/bin"
		return values, slices.DeleteFunc(folders, func(folder string) bool { return folder == home })
	}
	launched, launchedPath := variables(exec.Command(filepath.Join(git, "cmd", "git.exe"), "-c", "alias.env=!env", "env"))

	// Act
	started, startedPath := variables(Command("git", "-c", "alias.env=!env", "env"))

	// Assert
	delete(launched, "_")
	delete(started, "_")
	for _, name := range slices.Sorted(maps.Keys(maps.Collect(func(yield func(string, bool) bool) {
		for name := range launched {
			yield(name, true)
		}
		for name := range started {
			yield(name, true)
		}
	}))) {
		if started[name] != launched[name] {
			t.Errorf("%s = %q through execx, %q through the launcher", name, started[name], launched[name])
		}
	}
	if !slices.Equal(startedPath, launchedPath) {
		t.Errorf("PATH holds %q through execx, %q through the launcher", startedPath, launchedPath)
	}
}
