package gatehome

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// remoteName is the remote no-mistakes init adds to a gated checkout, which
// names the checkout's repository in the gate's home.
const remoteName = "no-mistakes"

// remote is a gated checkout's no-mistakes remote a move pointed elsewhere,
// with where it led before.
type remote struct {
	checkout string
	url      string
}

// repointRemotes points the no-mistakes remote of every checkout the gate's
// database at target names, and whose remote leads into old, at the same
// repository in target. A checkout that is gone or has no such remote is
// left alone. It returns the remotes it changed.
func repointRemotes(ctx context.Context, git execx.Runner, old, target string) ([]remote, error) {
	checkouts, err := gatedCheckouts(ctx, filepath.Join(target, "state.sqlite"))
	if err != nil {
		return nil, err
	}
	var changed []remote
	for _, checkout := range checkouts {
		if _, err := os.Stat(checkout); err != nil {
			continue
		}
		result, err := git.Run(ctx, execx.Request{Name: "git", Args: []string{"-C", checkout, "remote", "get-url", remoteName}, NoConsole: true})
		if err != nil {
			return changed, err
		}
		if result.ExitCode != 0 {
			continue
		}
		current := strings.TrimSpace(string(result.Stdout))
		relative, err := filepath.Rel(old, filepath.FromSlash(current))
		if err != nil || !filepath.IsLocal(relative) {
			continue
		}
		if err := setRemote(ctx, git, checkout, filepath.Join(target, relative)); err != nil {
			return changed, err
		}
		changed = append(changed, remote{checkout: checkout, url: current})
	}
	return changed, nil
}

// restoreRemotes points each changed remote back where it led.
func restoreRemotes(ctx context.Context, git execx.Runner, remotes []remote) error {
	var errs []error
	for _, remote := range remotes {
		errs = append(errs, setRemote(ctx, git, remote.checkout, remote.url))
	}
	return errors.Join(errs...)
}

func setRemote(ctx context.Context, git execx.Runner, checkout, url string) error {
	result, err := git.Run(ctx, execx.Request{Name: "git", Args: []string{"-C", checkout, "remote", "set-url", remoteName, url}, NoConsole: true})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("git remote set-url %s in %s: %s", remoteName, checkout, strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}

// gatedCheckouts are the checkouts the gate's database at path gates.
func gatedCheckouts(ctx context.Context, path string) ([]string, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	db, err := sql.Open("sqlite", databaseURL(path, "mode=ro"))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT working_path FROM repos")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var checkouts []string
	for rows.Next() {
		var checkout string
		if err := rows.Scan(&checkout); err != nil {
			return nil, err
		}
		checkouts = append(checkouts, checkout)
	}
	return checkouts, rows.Err()
}
