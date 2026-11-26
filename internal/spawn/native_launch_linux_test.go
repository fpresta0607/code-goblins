package spawn

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
)

// fakeShell writes a login shell named name whose body runs before the
// command it is handed, and the account whose passwd entry names it.
func fakeShell(t *testing.T, name, body string) account {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	shell := filepath.Join(dir, name)
	script := "#!/bin/sh\n" + body + "\nshift 3\nexec /bin/sh -c \"$1\"\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return account{Name: "harbor", Home: home, Shell: shell}
}

// noLoginSources is a login with no files to read and no user manager.
func noLoginSources(t *testing.T) loginSources {
	t.Helper()
	dir := t.TempDir()
	return loginSources{EnvironmentFile: filepath.Join(dir, "environment"), LocaleFile: filepath.Join(dir, "locale"), RuntimeDir: dir}
}

// writeFileWithFolders writes text to a file at path, created with its folders, with
// mode.
func writeFileWithFolders(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

// managerLogin is a login whose environment file sets PATH to a folder
// holding a systemctl that runs body, before the system's own folders, and
// whose runtime folder holds the user manager's socket.
func managerLogin(t *testing.T, body string) loginSources {
	t.Helper()
	sources := noLoginSources(t)
	bin := filepath.Join(sources.RuntimeDir, "bin")
	writeFileWithFolders(t, filepath.Join(bin, "systemctl"), "#!/bin/sh\n"+body+"\n", 0o755)
	writeFileWithFolders(t, filepath.Join(sources.RuntimeDir, "systemd", "private"), "", 0o644)
	writeFileWithFolders(t, sources.EnvironmentFile, "PATH="+bin+":/usr/bin:/bin\n", 0o644)
	return sources
}

// entryNames is the names env's entries set.
func entryNames(env []string) []string {
	names := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		names = append(names, name)
	}
	return names
}

func TestLoginEnvironmentKeepsOnlyWhatTheShellPrintsAfterTheMarker(t *testing.T) {
	// Arrange: a profile that prints NULs and entries before the shell runs
	// the command, writes to standard error, and sets a variable of its own.
	t.Setenv("GOBLINS_PARENT_ONLY", "inherited")
	user := fakeShell(t, "bash", `printf 'banner\0NOISE=1\0'
echo 'Welcome back'
echo 'rc warning' >&2
export FROM_PROFILE=yes
cd /`)

	// Act
	env, err := loginEnvironment(user, noLoginSources(t), 2*time.Minute)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"HOME=" + user.Home, "USER=harbor", "LOGNAME=harbor", "SHELL=" + user.Shell, "FROM_PROFILE=yes"} {
		if !slices.Contains(env, want) {
			t.Errorf("environment %q lacks %s", env, want)
		}
	}
	names := entryNames(env)
	for _, unwanted := range []string{"NOISE", "banner", "GOBLINS_PARENT_ONLY", "GOBLINS_TREE", "PWD", "OLDPWD", "SHLVL", "_"} {
		if slices.Contains(names, unwanted) {
			t.Errorf("environment %q holds %s", env, unwanted)
		}
	}
}

func TestLoginEnvironmentFailsAShellThatDoesNotPrint(t *testing.T) {
	// Arrange
	user := fakeShell(t, "zsh", "exec sleep 600")
	start := time.Now()

	// Act
	_, err := loginEnvironment(user, noLoginSources(t), 500*time.Millisecond) // stall-accepted: the test means the shell's timeout to run out

	// Assert
	if err == nil {
		t.Fatal("a shell that never printed gave an environment")
	}
	for _, want := range []string{user.Shell, "within 500ms", "~/.zshrc"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
	if took := time.Since(start); took > time.Minute {
		t.Errorf("the hung shell held the spawn %s", took)
	}
}

func TestLoginEnvironmentFailsAShellThatExitsNonZero(t *testing.T) {
	// Arrange
	user := fakeShell(t, "bash", "echo 'bad line in profile' >&2\nexit 3")

	// Act
	_, err := loginEnvironment(user, noLoginSources(t), 2*time.Minute)

	// Assert
	if err == nil {
		t.Fatal("a shell that exited 3 gave an environment")
	}
	for _, want := range []string{user.Shell, "exited 3", "bad line in profile", "~/.bashrc"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestLoginEnvironmentFailsAShellThatPrintsNoEnvironment(t *testing.T) {
	// Arrange: the shell exits 0 before it runs the command.
	user := fakeShell(t, "fish", "printf 'A=1\\0'\nexit 0")

	// Act
	_, err := loginEnvironment(user, noLoginSources(t), 2*time.Minute)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "printed no environment") || !strings.Contains(err.Error(), "config.fish") {
		t.Fatalf("error = %v, want one saying the shell printed no environment and naming config.fish", err)
	}
}

func TestParseEnvironmentFileReadsLinesAsPamEnvDoes(t *testing.T) {
	// Oracle: pam_env.conf(5) on /etc/environment: KEY=VAL lines, export
	// allowed, comments skipped, no expansion.
	tests := []struct {
		line string
		want map[string]string
	}{
		{"LANG=en_US.UTF-8", map[string]string{"LANG": "en_US.UTF-8"}},
		{"   ", map[string]string{}},
		{"# LANG=C", map[string]string{}},
		{"  #LANG=C", map[string]string{}},
		{"export http_proxy=http://proxy.example:3128", map[string]string{"http_proxy": "http://proxy.example:3128"}},
		{"NO_EQUALS", map[string]string{}},
		{"=value", map[string]string{}},
		{"BAD-NAME=1", map[string]string{}},
		{`DOUBLE="two words"`, map[string]string{"DOUBLE": "two words"}},
		{"SINGLE='one'", map[string]string{"SINGLE": "one"}},
		{`UNMATCHED="open`, map[string]string{"UNMATCHED": `"open`}},
		{`MIXED="a'`, map[string]string{"MIXED": `"a'`}},
		{`INNER=a"b"`, map[string]string{"INNER": `a"b"`}},
		{"LITERAL=$HOME/bin", map[string]string{"LITERAL": "$HOME/bin"}},
		{"EMPTY=", map[string]string{"EMPTY": ""}},
	}
	for _, test := range tests {
		// Arrange
		got := map[string]string{}

		// Act
		parseEnvironmentFile(test.line+"\n", got)

		// Assert
		if !maps.Equal(got, test.want) {
			t.Errorf("parseEnvironmentFile(%q) = %q, want %q", test.line, got, test.want)
		}
	}
}

func TestParseManagerEnvironmentDecodesDollarQuotes(t *testing.T) {
	// Oracle: bash's $'...' quoting, which systemctl show-environment writes
	// for a value with special characters.
	tests := []struct {
		line, want string
	}{
		{"PLAIN=/usr/bin", "/usr/bin"},
		{`SPACED=$'two words'`, "two words"},
		{`NEWLINE=$'a\nb'`, "a\nb"},
		{`TAB=$'a\tb'`, "a\tb"},
		{`QUOTE=$'it\'s'`, "it's"},
		{`BACKSLASH=$'C:\\path'`, `C:\path`},
		{`OCTAL=$'caf\303\251'`, "café"},
		{`DOLLAR=$'$HOME'`, "$HOME"},
		{`UNKNOWN=$'a\qb'`, `a\qb`},
	}
	for _, test := range tests {
		// Arrange
		got := map[string]string{}
		name, _, _ := strings.Cut(test.line, "=")

		// Act
		parseManagerEnvironment(test.line+"\n", got)

		// Assert
		if got[name] != test.want || len(got) != 1 {
			t.Errorf("parseManagerEnvironment(%q) = %q, want %s=%q", test.line, got, name, test.want)
		}
	}
}

func TestSeedEnvironmentWithoutFilesOrManagerIsThePasswdEntryAndSystemPath(t *testing.T) {
	// Arrange
	user := account{Name: "harbor", Home: "/home/harbor", Shell: "/bin/bash"}

	// Act
	env, err := seedEnvironment(user, noLoginSources(t), 2*time.Minute)

	// Assert
	want := []string{"HOME=/home/harbor", "LOGNAME=harbor", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "SHELL=/bin/bash", "USER=harbor"}
	if err != nil || !slices.Equal(env, want) {
		t.Fatalf("seedEnvironment = %q, %v; want %q", env, err, want)
	}
}

func TestSeedEnvironmentFailsAFileItCannotRead(t *testing.T) {
	// Arrange: a folder where the environment file should be.
	sources := noLoginSources(t)
	if err := os.Mkdir(sources.EnvironmentFile, 0o755); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err := seedEnvironment(account{Name: "harbor", Home: "/home/harbor", Shell: "/bin/bash"}, sources, 2*time.Minute)

	// Assert
	if err == nil || !strings.Contains(err.Error(), sources.EnvironmentFile) {
		t.Fatalf("error = %v, want one naming %s", err, sources.EnvironmentFile)
	}
}

func TestLoginEnvironmentStartsFromTheLocaleAndEnvironmentFiles(t *testing.T) {
	// Arrange
	user := fakeShell(t, "bash", "")
	sources := noLoginSources(t)
	writeFileWithFolders(t, sources.EnvironmentFile, "# proxy for the office\nhttps_proxy=\"http://proxy.example:3128\"\n", 0o644)
	writeFileWithFolders(t, sources.LocaleFile, "LANG=en_US.UTF-8\n", 0o644)

	// Act
	env, err := loginEnvironment(user, sources, 2*time.Minute)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"LANG=en_US.UTF-8", "https_proxy=http://proxy.example:3128"} {
		if !slices.Contains(env, want) {
			t.Errorf("environment %q lacks %s", env, want)
		}
	}
}

func TestLoginEnvironmentTakesEachSourceOverTheOneBefore(t *testing.T) {
	// Arrange
	user := fakeShell(t, "bash", "export ORDER_SHELL=shell")
	sources := managerLogin(t, `[ "$1 $2" = "--user show-environment" ] || exit 9
printf 'ORDER_MANAGER=manager\nORDER_SHELL=manager\nHOME=/elsewhere\nMANAGER_RUNTIME=%s\n' "$XDG_RUNTIME_DIR"`)
	pathLine, err := os.ReadFile(sources.EnvironmentFile)
	if err != nil {
		t.Fatal(err)
	}
	writeFileWithFolders(t, sources.EnvironmentFile, string(pathLine)+"ORDER_FILE=environment\nORDER_LOCALE=environment\nORDER_MANAGER=environment\nORDER_SHELL=environment\n", 0o644)
	writeFileWithFolders(t, sources.LocaleFile, "ORDER_LOCALE=locale\nORDER_MANAGER=locale\nORDER_SHELL=locale\n", 0o644)

	// Act
	env, err := loginEnvironment(user, sources, 2*time.Minute)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ORDER_FILE=environment", "ORDER_LOCALE=locale", "ORDER_MANAGER=manager", "ORDER_SHELL=shell", "HOME=" + user.Home, "MANAGER_RUNTIME=" + sources.RuntimeDir, strings.TrimSpace(string(pathLine))} {
		if !slices.Contains(env, want) {
			t.Errorf("environment %q lacks %s", env, want)
		}
	}
}

func TestSeedEnvironmentHasNoUserManagerWithoutSystemctlOrItsSocket(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(t *testing.T, sources loginSources)
	}{
		{"no systemctl on PATH", func(t *testing.T, sources loginSources) {
			writeFileWithFolders(t, sources.EnvironmentFile, "PATH="+t.TempDir()+"\n", 0o644)
		}},
		{"no manager socket", func(t *testing.T, sources loginSources) {
			if err := os.Remove(filepath.Join(sources.RuntimeDir, "systemd", "private")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			sources := managerLogin(t, "printf 'FROM_MANAGER=1\\n'")
			test.arrange(t, sources)

			// Act
			env, err := seedEnvironment(account{Name: "harbor", Home: "/home/harbor", Shell: "/bin/bash"}, sources, 2*time.Minute)

			// Assert
			if err != nil || slices.Contains(entryNames(env), "FROM_MANAGER") {
				t.Fatalf("seedEnvironment = %q, %v; want no error and nothing from the manager", env, err)
			}
		})
	}
}

func TestSeedEnvironmentFailsASystemctlThatDoesNotPrint(t *testing.T) {
	// Arrange
	sources := managerLogin(t, "exec sleep 600")
	start := time.Now()

	// Act
	_, err := seedEnvironment(account{Name: "harbor", Home: "/home/harbor", Shell: "/bin/bash"}, sources, 500*time.Millisecond) // stall-accepted: the test means systemctl's timeout to run out

	// Assert
	if err == nil {
		t.Fatal("a systemctl that never printed gave an environment")
	}
	for _, want := range []string{"systemctl --user show-environment", "within 500ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
	if took := time.Since(start); took > time.Minute {
		t.Errorf("the hung systemctl held the spawn %s", took)
	}
}

func TestSeedEnvironmentFailsASystemctlThatExitsNonZero(t *testing.T) {
	// Arrange
	sources := managerLogin(t, "echo 'Failed to connect to bus' >&2\nexit 1")

	// Act
	_, err := seedEnvironment(account{Name: "harbor", Home: "/home/harbor", Shell: "/bin/bash"}, sources, 2*time.Minute)

	// Assert
	if err == nil {
		t.Fatal("a systemctl that exited 1 gave an environment")
	}
	for _, want := range []string{"systemctl --user show-environment", "exited 1", "Failed to connect to bus"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestGoblinProgramStartsTheRealHarnessOnlyForTheUsersHome(t *testing.T) {
	// Arrange: a claude script first on PATH, outside the home.
	userHome := t.TempDir()
	root := filepath.Join(userHome, ".local", "share", "code-goblins")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	claude := filepath.Join(bin, "claude")
	writeFileWithFolders(t, claude, "#!/bin/sh\nexit 0\n", 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(RealHarnessVariable, "")
	service := Service{HomeRoot: root}
	launch := harness.Launch{Args: []string{"--resume", "abc"}}

	// Act
	program, err := service.goblinProgram(harness.Claude, launch, []string{"HOME=" + userHome})
	_, otherErr := service.goblinProgram(harness.Claude, launch, []string{"HOME=" + t.TempDir()})

	// Assert
	if want := []string{claude, "--resume", "abc"}; err != nil || !slices.Equal(program, want) {
		t.Errorf("goblinProgram for the user's home = %q, %v; want %q", program, err, want)
	}
	if otherErr == nil || !strings.Contains(otherErr.Error(), "not the home the user's environment names") {
		t.Errorf("goblinProgram for another home: error = %v, want a refusal", otherErr)
	}
}

func TestNativeProgramStartsAScriptAsItself(t *testing.T) {
	// Arrange
	bin := t.TempDir()
	tool := filepath.Join(bin, "harbor-tool")
	writeFileWithFolders(t, tool, "#!/bin/sh\nexit 0\n", 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Act
	program, err := NativeProgram("harbor-tool", "--version")

	// Assert
	if want := []string{tool, "--version"}; err != nil || !slices.Equal(program, want) {
		t.Fatalf("NativeProgram = %q, %v; want %q", program, err, want)
	}
}

func TestParsePasswdReadsHomeAndShell(t *testing.T) {
	tests := []struct {
		entry   string
		want    account
		isValid bool
	}{
		{"harbor:x:1000:1000:Harbor,,,:/home/harbor:/bin/zsh\n", account{Name: "harbor", Home: "/home/harbor", Shell: "/bin/zsh"}, true},
		{"harbor:x:1000:1000::/home/harbor:", account{}, false},
		{"harbor:x:1000:1000::relative:/bin/sh", account{}, false},
		{":x:1000:1000::/home/harbor:/bin/sh", account{}, false},
		{"", account{}, false},
	}
	for _, test := range tests {
		got, err := parsePasswd(test.entry)
		if (err == nil) != test.isValid || got != test.want {
			t.Errorf("parsePasswd(%q) = %+v, %v; want %+v, valid %t", test.entry, got, err, test.want, test.isValid)
		}
	}
}

func TestUsersHomeIsTheHomeTheUsersEnvironmentNames(t *testing.T) {
	tests := []struct {
		userEnv []string
		want    string
	}{
		{[]string{"GOBLINS_HOME=/srv/goblins", "HOME=/home/harbor"}, "/srv/goblins"},
		{[]string{"XDG_DATA_HOME=/data/harbor", "HOME=/home/harbor"}, "/data/harbor/code-goblins"},
		{[]string{"XDG_DATA_HOME=relative", "HOME=/home/harbor"}, "/home/harbor/.local/share/code-goblins"},
		{[]string{"PATH=/usr/bin"}, ""},
	}
	t.Setenv("XDG_DATA_HOME", "/from/this/process")
	for _, test := range tests {
		if got := usersHome(test.userEnv); got != test.want {
			t.Errorf("usersHome(%q) = %q, want %q", test.userEnv, got, test.want)
		}
	}
}

func TestUserEnvironmentReadsThisUsersLogin(t *testing.T) {
	// Act
	env, err := UserEnvironment()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	names := entryNames(env)
	if !slices.Contains(names, "PATH") || !slices.Contains(names, "HOME") || slices.Contains(names, "GOBLINS_TREE") {
		t.Errorf("environment names %q, want PATH and HOME and no tree mark", names)
	}
}
