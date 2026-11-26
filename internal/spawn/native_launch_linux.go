package spawn

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/execx"
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
// user's passwd entry names sets up from a clean start (loginEnvironment).
// Nothing this process inherited from whatever runs it reaches it.
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
	return loginEnvironment(user, loginShellTimeout)
}

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
// from only what a login gives it: HOME, USER, LOGNAME, SHELL and the
// system's PATH, in the user's home folder. The shell prints a marker and
// then the environment with each entry ended by a NUL, so nothing a file it
// runs prints can pass for an entry, and the marker is printed in two halves,
// so a file that echoes the command line cannot print it whole. It runs in a
// session of its own, with no terminal to take from whatever runs goblins,
// and what it leaves running is ended with it. A shell that does not print
// within timeout, or exits otherwise, fails with what to check, and no other
// environment stands in.
func loginEnvironment(user account, timeout time.Duration) ([]string, error) {
	halves := make([]byte, 16)
	if _, err := rand.Read(halves); err != nil {
		return nil, err
	}
	first, second := hex.EncodeToString(halves[:8]), hex.EncodeToString(halves[8:])
	ctx, cancel := awake.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := execx.CommandContext(ctx, user.Shell, "-i", "-l", "-c", "printf '%s%s' "+first+" "+second+"; exec env -0")
	cmd.Dir = user.Home
	cmd.Env = []string{"HOME=" + user.Home, "USER=" + user.Name, "LOGNAME=" + user.Name, "SHELL=" + user.Shell, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
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
