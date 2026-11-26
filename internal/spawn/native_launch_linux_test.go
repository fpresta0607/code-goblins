package spawn

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	env, err := loginEnvironment(user, 2*time.Minute)

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
	_, err := loginEnvironment(user, 500*time.Millisecond) // stall-accepted: the test means the shell's timeout to run out

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
	_, err := loginEnvironment(user, 2*time.Minute)

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
	_, err := loginEnvironment(user, 2*time.Minute)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "printed no environment") || !strings.Contains(err.Error(), "config.fish") {
		t.Fatalf("error = %v, want one saying the shell printed no environment and naming config.fish", err)
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
