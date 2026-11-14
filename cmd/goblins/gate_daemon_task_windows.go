package main

import (
	"context"
	"fmt"
	"html"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// gateDaemonTaskPrefix starts the name of each scheduled task no-mistakes
// registers to start a daemon at sign-in, one for each home, which it names
// after the home.
const gateDaemonTaskPrefix = `\no-mistakes-daemon-`

// gateDaemonTaskRoot finds the home a daemon task starts its daemon in: its
// action runs no-mistakes daemon run --root <home>.
var gateDaemonTaskRoot = regexp.MustCompile(`<Arguments>daemon run --root ([^<]+)</Arguments>`)

// forgetGateDaemonTask removes the scheduled task that starts the gate
// daemon of the home at root at sign-in. no-mistakes keeps it through daemon
// stop, and its daemon uninstall removes one on macOS alone, so a home that
// moved would get a daemon again at every sign-in, in a folder made again for
// it. Only a task whose action names root goes.
func forgetGateDaemonTask(ctx context.Context, commands execx.Runner, root string) error {
	listed, err := schtasks(ctx, commands, "/query", "/fo", "csv", "/nh")
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(listed, "\n") {
		name, _, _ := strings.Cut(strings.TrimSpace(line), ",")
		name = strings.Trim(name, `"`)
		if seen[name] || !strings.HasPrefix(strings.ToLower(name), gateDaemonTaskPrefix) {
			continue
		}
		seen[name] = true
		definition, err := schtasks(ctx, commands, "/query", "/tn", name, "/xml")
		if err != nil {
			return err
		}
		if !startsGateDaemonOf(definition, root) {
			continue
		}
		if _, err := schtasks(ctx, commands, "/delete", "/tn", name, "/f"); err != nil {
			return err
		}
	}
	return nil
}

// startsGateDaemonOf says the task with this definition starts the daemon of
// the home at root. no-mistakes quotes a home with a space in it as Go does.
func startsGateDaemonOf(definition, root string) bool {
	found := gateDaemonTaskRoot.FindStringSubmatch(definition)
	if found == nil {
		return false
	}
	named := html.UnescapeString(found[1])
	if unquoted, err := strconv.Unquote(named); err == nil {
		named = unquoted
	}
	return strings.EqualFold(filepath.Clean(named), filepath.Clean(root))
}

func schtasks(ctx context.Context, commands execx.Runner, args ...string) (string, error) {
	result, err := commands.Run(ctx, execx.Request{Name: "schtasks", Args: args})
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("schtasks %s exited %d: %s", strings.Join(args, " "), result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return string(result.Stdout), nil
}
