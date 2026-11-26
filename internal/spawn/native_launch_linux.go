package spawn

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// isProgram reports true: Linux starts any program as itself, and a script
// by its #! line.
func isProgram(string) bool {
	return true
}

// scriptProgram runs the script at path with args as itself, since Linux
// runs a script by its #! line with no shell between.
func scriptProgram(path string, args ...string) ([]string, error) {
	return append([]string{path}, args...), nil
}

// loginShellTimeout bounds how long the user's passwd entry and login shell
// may take to give the user's environment.
const loginShellTimeout = 15 * time.Second

// UserEnvironment is the environment a new login of this user gets, as
// Windows gives a new process of the user one: what the login shell the
// user's passwd entry names sets up from what a PAM login starts it with:
// /etc/environment, /etc/default/locale and the user manager's environment
// (loginEnvironment). Nothing this process inherited from whatever runs it
// reaches it.
func UserEnvironment() ([]string, error) {
	ctx, cancel := awake.WithTimeout(context.Background(), loginShellTimeout)
	defer cancel()
	uid := strconv.Itoa(os.Getuid())
	result, err := execx.OSRunner{}.Run(ctx, execx.Request{Name: "getent", Args: []string{"passwd", uid}})
	if err != nil {
		return nil, fmt.Errorf("spawn: read user %s's passwd entry with getent: %w", uid, err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("spawn: getent found no passwd entry for user %s (exit %d)", uid, result.ExitCode)
	}
	user, err := parsePasswd(string(result.Stdout))
	if err != nil {
		return nil, err
	}
	sources := loginSources{EnvironmentFile: "/etc/environment", LocaleFile: "/etc/default/locale", RuntimeDir: "/run/user/" + uid}
	return loginEnvironment(user, sources, loginShellTimeout)
}

// loginSources names where a PAM login of a user takes the environment it
// starts the login shell with: the files pam_env reads, and the user's
// runtime folder, where the user manager listens when it runs.
type loginSources struct {
	EnvironmentFile, LocaleFile, RuntimeDir string
}

// systemPath is the PATH a login starts with when nothing it reads sets one.
const systemPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// account is what a passwd entry says of a user.
type account struct {
	Name, Home, Shell string
}

// parsePasswd reads one passwd entry, name:password:uid:gid:gecos:home:shell,
// as getent prints it from whichever source the system names, not only
// /etc/passwd.
func parsePasswd(entry string) (account, error) {
	fields := strings.Split(strings.TrimSpace(entry), ":")
	if len(fields) != 7 || fields[0] == "" || !filepath.IsAbs(fields[5]) || !filepath.IsAbs(fields[6]) {
		return account{}, fmt.Errorf("spawn: the passwd entry %q names no user with a home folder and a login shell", strings.TrimSpace(entry))
	}
	return account{Name: fields[0], Home: fields[5], Shell: fields[6]}, nil
}

// shellState names the variables a shell sets for itself rather than for the
// programs a terminal runs, which a terminal's own shell sets again: its
// folders, its depth, the last command it ran.
var shellState = []string{"PWD", "OLDPWD", "SHLVL", "_", execx.TreeVariable}

// loginEnvironment is the environment user's login shell sets up, started as
// an interactive login shell, so it runs the files a terminal's shell runs,
// from only what a PAM login starts it with (seedEnvironment), in the user's
// home folder. The shell prints a marker and
// then the environment with each entry ended by a NUL, so nothing a file it
// runs prints can pass for an entry, and the marker is printed in two halves,
// so a file that echoes the command line cannot print it whole. It runs in a
// session of its own, with no terminal to take from whatever runs goblins,
// and what it leaves running is ended with it. A shell that does not print
// within timeout, or exits otherwise, fails with what to check, and no other
// environment stands in.
func loginEnvironment(user account, sources loginSources, timeout time.Duration) ([]string, error) {
	seed, err := seedEnvironment(user, sources, timeout)
	if err != nil {
		return nil, err
	}
	halves := make([]byte, 16)
	if _, err := rand.Read(halves); err != nil {
		return nil, err
	}
	first, second := hex.EncodeToString(halves[:8]), hex.EncodeToString(halves[8:])
	ctx, cancel := awake.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := execx.CommandContext(ctx, user.Shell, "-i", "-l", "-c", "printf '%s%s' "+first+" "+second+"; exec env -0")
	cmd.Dir = user.Home
	cmd.Env = seed
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.SysProcAttr.Setsid = true
	cmd.WaitDelay = time.Second
	tree, err := execx.StartTree(cmd)
	if err != nil {
		return nil, fmt.Errorf("spawn: start the login shell %s from user %s's passwd entry: %w", user.Shell, user.Name, err)
	}
	waitErr := cmd.Wait()
	_, endErr := tree.End()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("spawn: the login shell %s did not print user %s's environment within %s; check %s for a command that waits", user.Shell, user.Name, timeout, startupFiles(user.Shell))
	}
	// A process the shell's files left running that holds its output open
	// past WaitDelay leaves the environment printed whole: the shell exited.
	if waitErr != nil && !errors.Is(waitErr, exec.ErrWaitDelay) {
		return nil, fmt.Errorf("spawn: the login shell %s %s before it printed user %s's environment%s; check %s", user.Shell, exitOf(waitErr), user.Name, stderrTail(stderr.Bytes()), startupFiles(user.Shell))
	}
	if endErr != nil {
		return nil, fmt.Errorf("spawn: end what the login shell %s left running: %w", user.Shell, endErr)
	}
	_, printed, isPrinted := bytes.Cut(stdout.Bytes(), []byte(first+second))
	entries := bytes.Split(printed, []byte{0})
	var env []string
	// What follows the last NUL is no entry: env ends each with one.
	for _, entry := range entries[:len(entries)-1] {
		name, _, isEntry := strings.Cut(string(entry), "=")
		if isEntry && name != "" && !slices.Contains(shellState, name) {
			env = append(env, string(entry))
		}
	}
	if !isPrinted || len(env) == 0 {
		return nil, fmt.Errorf("spawn: the login shell %s printed no environment for user %s%s; check %s", user.Shell, user.Name, stderrTail(stderr.Bytes()), startupFiles(user.Shell))
	}
	return env, nil
}

// seedEnvironment is what a PAM login starts user's login shell with, each
// source over the one before: /etc/environment and /etc/default/locale as
// pam_env reads them, the user manager's environment when it runs, and then
// HOME, USER, LOGNAME and SHELL from the passwd entry, with the system's PATH
// when nothing before set one. A missing file sets nothing; one that cannot
// be read fails.
func seedEnvironment(user account, sources loginSources, timeout time.Duration) ([]string, error) {
	seed := map[string]string{}
	for _, file := range []string{sources.EnvironmentFile, sources.LocaleFile} {
		text, err := fsx.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("spawn: read %s, which a login of user %s starts from: %w", file, user.Name, err)
		}
		parseEnvironmentFile(string(text), seed)
	}
	path, isPathSet := seed["PATH"]
	if !isPathSet {
		path = systemPath
	}
	if err := readManagerEnvironment(path, sources.RuntimeDir, timeout, seed); err != nil {
		return nil, err
	}
	if _, isPathSet := seed["PATH"]; !isPathSet {
		seed["PATH"] = systemPath
	}
	seed["HOME"], seed["USER"], seed["LOGNAME"], seed["SHELL"] = user.Home, user.Name, user.Name, user.Shell
	env := make([]string, 0, len(seed))
	for name, value := range seed {
		env = append(env, name+"="+value)
	}
	slices.Sort(env)
	return env, nil
}

// parseEnvironmentFile adds to into the variables text, a file such as
// /etc/environment, sets as pam_env reads it: a NAME=value line, with or
// without export before it, its value as written but for one pair of
// matching quotes around it, with nothing in it expanded. Blank lines,
// comments and lines that set no variable by a valid name are skipped.
func parseEnvironmentFile(text string, into map[string]string) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, isEntry := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !isEntry || !isVariableName(name) {
			continue
		}
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		into[name] = value
	}
}

// isVariableName reports whether name is one pam_env sets: letters, digits
// and underscores.
func isVariableName(name string) bool {
	return name != "" && strings.IndexFunc(name, func(r rune) bool {
		return r != '_' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z')
	}) < 0
}

// readManagerEnvironment adds to into the environment the user manager gives
// what it starts, as systemctl, found on path, prints it. No systemctl on
// path, or no manager socket in runtimeDir, is no user manager, which adds
// nothing. systemctl that does not print within timeout, or exits otherwise,
// fails with what it wrote, and nothing stands in.
func readManagerEnvironment(path, runtimeDir string, timeout time.Duration, into map[string]string) error {
	systemctl := ""
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		if found, err := exec.LookPath(filepath.Join(dir, "systemctl")); err == nil {
			systemctl = found
			break
		}
	}
	if systemctl == "" {
		return nil
	}
	socket := filepath.Join(runtimeDir, "systemd", "private")
	if _, err := os.Stat(socket); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("spawn: look for the user manager's socket %s: %w", socket, err)
	}
	ctx, cancel := awake.WithTimeout(context.Background(), timeout)
	defer cancel()
	request := execx.Request{Name: systemctl, Args: []string{"--user", "show-environment"}, Env: []string{"PATH=" + path, "XDG_RUNTIME_DIR=" + runtimeDir}, KillTree: true}
	result, err := execx.OSRunner{}.Run(ctx, request)
	if ctx.Err() != nil {
		return fmt.Errorf("spawn: %s --user show-environment did not print the user manager's environment within %s%s", systemctl, timeout, stderrTail(result.Stderr))
	}
	if err != nil {
		return fmt.Errorf("spawn: run %s --user show-environment: %w", systemctl, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("spawn: %s --user show-environment exited %d%s", systemctl, result.ExitCode, stderrTail(result.Stderr))
	}
	parseManagerEnvironment(string(result.Stdout), into)
	return nil
}

// parseManagerEnvironment adds to into the variables text, what systemctl
// --user show-environment prints, sets: a NAME=value line each, where a value
// with characters a shell reads specially is quoted as $'...'.
func parseManagerEnvironment(text string, into map[string]string) {
	for _, line := range strings.Split(text, "\n") {
		name, value, isEntry := strings.Cut(line, "=")
		if !isEntry || !isVariableName(name) {
			continue
		}
		if len(value) >= 3 && strings.HasPrefix(value, "$'") && strings.HasSuffix(value, "'") {
			value = decodeDollarQuoted(value[2 : len(value)-1])
		}
		into[name] = value
	}
}

// dollarQuotedEscapes are the characters a backslash and a letter or mark
// stand for in a $'...' word, those systemd writes.
var dollarQuotedEscapes = map[byte]byte{'a': '\a', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v', '\\': '\\', '\'': '\'', '"': '"'}

// decodeDollarQuoted is the value quoted, the inside of a $'...' word, stands
// for: a backslash and a letter or mark (dollarQuotedEscapes), or a byte as
// up to three octal digits, decoded, and any other backslash kept, as a shell
// keeps it.
func decodeDollarQuoted(quoted string) string {
	var value strings.Builder
	for index := 0; index < len(quoted); index++ {
		if quoted[index] != '\\' || index+1 == len(quoted) {
			value.WriteByte(quoted[index])
			continue
		}
		if decoded, isEscape := dollarQuotedEscapes[quoted[index+1]]; isEscape {
			value.WriteByte(decoded)
			index++
			continue
		}
		code, digits := 0, 0
		for digits < 3 && index+1 < len(quoted) && quoted[index+1] >= '0' && quoted[index+1] <= '7' {
			code = code*8 + int(quoted[index+1]-'0')
			index++
			digits++
		}
		if digits == 0 {
			value.WriteByte('\\')
			continue
		}
		value.WriteByte(byte(code))
	}
	return value.String()
}

// exitOf says how a shell's run ended.
func exitOf(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "exited " + strconv.Itoa(exit.ExitCode())
	}
	return "failed: " + err.Error()
}

// stderrTail is the end of what a shell wrote on its standard error, on one
// line after a colon, or nothing when it wrote nothing.
func stderrTail(stderr []byte) string {
	text := strings.Join(strings.Fields(string(stderr)), " ")
	if text == "" {
		return ""
	}
	if len(text) > 400 {
		text = "..." + text[len(text)-400:]
	}
	return ": " + text
}

// startupFiles names the files a login shell of its kind runs, in which a
// command that fails or waits would sit.
func startupFiles(shell string) string {
	switch filepath.Base(shell) {
	case "bash":
		return "~/.bash_profile, ~/.bash_login or ~/.profile, and ~/.bashrc"
	case "zsh":
		return "~/.zshenv, ~/.zprofile, ~/.zshrc and ~/.zlogin"
	case "fish":
		return "~/.config/fish/config.fish"
	}
	return "~/.profile"
}
