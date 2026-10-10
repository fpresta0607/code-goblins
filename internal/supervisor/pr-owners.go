package supervisor

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// fleetOwners are the GitHub accounts whose repositories the fleet owns: the
// account gh works as, and the organizations config/fleet.json names. A pull
// request in another owner's repository is none of the fleet's business
// unless a goblin opened it: on 2026-10-07 a checkout of the Overlord's fork
// whose origin is the upstream raised 39 pr_health wakes for strangers' pull
// requests. The account is asked once a poll, and only when a listed pull
// request is no goblin's by what the goblin said or has checked out, or when
// a goblin's own is behind its base with its checks passed.
type fleetOwners struct {
	named  []string
	viewer string
	err    error
	isRead bool
}

// owns reports whether the fleet owns the repository pr is in. Its owner is
// the one GitHub gives in the pull request's own address, never one read
// from a remote of the checkout, whose name says nothing of whose it is.
func (o *fleetOwners) owns(ctx context.Context, runner execx.Runner, dir string, pr ghPullRequest) (bool, error) {
	owner, _ := pullRequestRepository(pr.URL)
	isOwner := func(name string) bool { return strings.EqualFold(name, owner) }
	if slices.ContainsFunc(o.named, isOwner) {
		return true, nil
	}
	viewer, err := o.account(ctx, runner, dir)
	return err == nil && isOwner(viewer), err
}

// account is the account gh works as, asked once a poll.
func (o *fleetOwners) account(ctx context.Context, runner execx.Runner, dir string) (string, error) {
	if !o.isRead {
		o.isRead = true
		o.viewer, o.err = runOutput(ctx, runner, dir, "gh", "api", "user", "--jq", ".login")
		if o.err == nil && o.viewer == "" {
			o.err = errors.New("gh named no account it works as")
		}
	}
	return o.viewer, o.err
}

// isAccount reports whether login is the account gh works as. While that
// account cannot be read it is nobody's.
func (o *fleetOwners) isAccount(ctx context.Context, runner execx.Runner, dir, login string) bool {
	viewer, err := o.account(ctx, runner, dir)
	return err == nil && strings.EqualFold(viewer, login)
}

// pullRequestRepository is the owner and name of the repository a pull
// request's address, as GitHub gives it, puts it in.
func pullRequestRepository(url string) (owner, name string) {
	owner, rest, _ := strings.Cut(strings.TrimPrefix(url, "https://github.com/"), "/")
	name, _, _ = strings.Cut(rest, "/")
	return owner, name
}
