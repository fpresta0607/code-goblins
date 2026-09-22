package supervisor

import (
	"context"
	"errors"
	"fmt"
	"github.com/fpresta0607/code-goblins/internal/state"
	"regexp"
	"strings"
)

var githubPR = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/[1-9][0-9]*$`)

func (g Git) VerifyDelivery(ctx context.Context, meta state.TaskMeta, base, head, pr string) (string, error) {
	match := githubPR.FindStringSubmatch(pr)
	if len(match) != 2 {
		return "", errors.New("delivery PR identity is unavailable")
	}
	origin, err := g.run(ctx, meta.Project, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	origin = strings.TrimSpace(origin)
	repo := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(origin, "https://github.com/"), "git@github.com:"), ".git")
	if !strings.EqualFold(repo, match[1]) {
		return "", errors.New("PR does not match the project's origin")
	}
	mainHead, mainRef, err := g.remoteMain(ctx, meta.Project)
	if err != nil {
		return "", err
	}
	local, err := g.run(ctx, meta.Project, "rev-parse", "--verify", mainRef)
	if err != nil || strings.TrimSpace(local) != mainHead {
		return "", errors.New("main checkout needs the current remote main before its content can be verified")
	}
	if err := g.verifyLandedContent(ctx, meta.Project, meta.Worktree, base, head, mainHead); err != nil {
		return "", err
	}
	after, ref, err := g.remoteMain(ctx, meta.Project)
	if err != nil || after != mainHead || ref != mainRef {
		return "", errors.New("remote main changed during content verification")
	}
	return mainHead, nil
}

func (g Git) remoteMain(ctx context.Context, project string) (string, string, error) {
	output, err := g.run(ctx, project, "ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return "", "", errors.New("current remote main could not be read")
	}
	head, ref := "", ""
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD" && strings.HasPrefix(fields[1], "refs/heads/") {
			ref = fields[1]
		}
		if len(fields) == 2 && fields[1] == "HEAD" && commitID.MatchString(fields[0]) {
			head = fields[0]
		}
	}
	if head == "" || ref == "" {
		return "", "", errors.New("remote default branch evidence is unavailable")
	}
	return head, ref, nil
}

// Read the actual changed file contents from main. A merge status, commit
// ancestry, or terminal_head_verified_at timestamp cannot satisfy this proof.
// The baseline was retained before delivery, so later origin/HEAD movement
// cannot erase earlier commits from the set being verified.
func (g Git) verifyLandedContent(ctx context.Context, project, worktree, base, head, mainHead string) error {
	if !commitID.MatchString(base) || !commitID.MatchString(head) || !commitID.MatchString(mainHead) {
		return errors.New("retained task baseline is unavailable for landed-content verification")
	}
	output, err := g.run(ctx, worktree, "diff", "--name-status", "--no-renames", "-z", base, head, "--")
	if err != nil {
		return err
	}
	parts := strings.Split(strings.TrimSuffix(output, "\x00"), "\x00")
	if output == "" || len(parts)%2 != 0 || len(parts) > 1024 {
		return errors.New("landed-content change set is empty, invalid or exceeds 512 files")
	}
	for i := 0; i < len(parts); i += 2 {
		status, path := parts[i], parts[i+1]
		if status == "D" {
			present, err := g.run(ctx, project, "ls-tree", "-z", mainHead, "--", path)
			if err != nil || present != "" {
				return fmt.Errorf("deletion is not verified on main: %s", path)
			}
			continue
		}
		want, err := g.run(ctx, worktree, "show", head+":"+path)
		if err != nil {
			return errors.New("task content could not be read within verification bounds")
		}
		landed, err := g.run(ctx, project, "show", mainHead+":"+path)
		if err != nil {
			return fmt.Errorf("main content could not be read: %s", path)
		}
		if want != landed {
			return fmt.Errorf("main content differs for %s", path)
		}
	}
	return nil
}
