package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

// systemdRun is the program that gives a host a scope of its own.
var systemdRun = "systemd-run"

// startHost starts the host detached from this process (startDetached), in a
// transient systemd user scope of its own: a unit that stops ends everything
// in its cgroup, and a session started in a cgroup stays in it, so without
// one the host would end with whatever launched it, the supervisor's unit on
// its update, crash or restart included. systemd-run makes the scope and then
// runs the host in its own process, so the host keeps the pid this launch
// started. Where no user manager gives a scope, the host starts in this
// process's cgroup instead and is contained.
func startHost(log *os.File, name string, args, env []string) (*exec.Cmd, bool, error) {
	env = withRuntimeFolder(env)
	if err := scopeAnswers(env); err != nil {
		fmt.Fprintf(log, "%s host: no systemd user manager gives this host a scope of its own (%v), so it runs in the cgroup of the process that launched it (pid %d) and ends when that cgroup's unit stops\n", time.Now().UTC().Format(time.RFC3339Nano), err, os.Getpid())
		cmd, err := startDetached(log, name, args, env)
		return cmd, true, err
	}
	cmd, err := startDetached(log, systemdRun, slices.Concat([]string{"--user", "--scope", "--collect", "--quiet", "--", name}, args), env)
	return cmd, false, err
}

// scopeAnswers starts true in a scope of its own, as startHost starts a
// host, and says why it could not: nothing but making one proves the user
// manager makes one. A failed start of the host itself would end it before it
// serves, with nothing to tell it from a host that failed.
func scopeAnswers(env []string) error {
	trying, cancel := awake.WithTimeout(context.Background(), LaunchTimeout)
	defer cancel()
	probe := execx.CommandContext(trying, systemdRun, "--user", "--scope", "--collect", "--quiet", "--", "true")
	probe.Env = env
	output, err := probe.CombinedOutput()
	if err != nil {
		if text := strings.TrimSpace(string(output)); text != "" {
			return fmt.Errorf("%s: %w: %s", systemdRun, err, text)
		}
		return fmt.Errorf("%s: %w", systemdRun, err)
	}
	return nil
}

// withRuntimeFolder is env with XDG_RUNTIME_DIR naming the user's own
// /run/user/<uid> where env names none and that folder is the user's own:
// systemd-run finds the user manager through it, and a command run from
// cron, su - or sudo -iu has no such variable, though the manager runs.
func withRuntimeFolder(env []string) []string {
	if slices.ContainsFunc(env, func(entry string) bool { return strings.HasPrefix(entry, "XDG_RUNTIME_DIR=") }) {
		return env
	}
	folder := filepath.Join("/run/user", strconv.Itoa(os.Getuid()))
	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() {
		return env
	}
	if stat, isStat := info.Sys().(*syscall.Stat_t); !isStat || int(stat.Uid) != os.Getuid() {
		return env
	}
	return append(slices.Clone(env), "XDG_RUNTIME_DIR="+folder)
}
