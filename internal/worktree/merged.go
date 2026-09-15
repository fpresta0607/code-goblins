package worktree

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// RequireMerged is deliberately conservative. It accepts ancestry or an exact
// content match (including a squash merge) against the recorded origin default
// branch. Missing or older remote evidence holds cleanup; it never fetches or
// changes refs as a side effect of a retention audit.
type MergeProof struct {
	Head        string    `json:"head"`
	DefaultHead string    `json:"default_head"`
	DefaultRef  string    `json:"default_ref"`
	Method      string    `json:"method"`
	VerifiedAt  time.Time `json:"verified_at"`
}

func RequireMerged(ctx context.Context, commands execx.Runner, dir string) error {
	_, err := ProveMerged(ctx, commands, dir)
	return err
}

func ProveMerged(ctx context.Context, commands execx.Runner, dir string) (MergeProof, error) {
	var proof MergeProof
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := commands.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: []string{"symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"}})
	target := strings.TrimSpace(string(result.Stdout))
	if err != nil || result.ExitCode != 0 || !strings.HasPrefix(target, "refs/remotes/origin/") || target == "refs/remotes/origin/HEAD" {
		return proof, fmt.Errorf("cleanup: origin default branch is unavailable; refresh remote evidence and preserve the worktree")
	}
	return proveMergedRef(ctx, commands, dir, target)
}

// Local-only delivery lands on the primary's checked-out main branch and does
// not invent an origin remote. Only explicit main/master primary branches
// qualify; a primary left on a feature branch is not landing evidence.
func ProveLocalMerged(ctx context.Context, commands execx.Runner, project, dir string) (MergeProof, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := commands.Run(ctx, execx.Request{Dir: project, Name: "git", Args: []string{"symbolic-ref", "--quiet", "HEAD"}})
	target := strings.TrimSpace(string(result.Stdout))
	if err != nil || result.ExitCode != 0 || (target != "refs/heads/main" && target != "refs/heads/master") {
		return MergeProof{}, fmt.Errorf("cleanup: local-only primary must be on its main branch to prove landing")
	}
	return proveMergedRef(ctx, commands, dir, target)
}

func proveMergedRef(ctx context.Context, commands execx.Runner, dir, target string) (MergeProof, error) {
	var proof MergeProof
	result, err := commands.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: []string{"rev-parse", "HEAD", target}})
	shas := strings.Fields(string(result.Stdout))
	if err != nil || result.ExitCode != 0 || len(shas) != 2 {
		return proof, fmt.Errorf("cleanup: merge commit identities unavailable")
	}
	for _, sha := range shas {
		if data, e := hex.DecodeString(sha); e != nil || len(data) != 20 {
			return proof, fmt.Errorf("cleanup: invalid commit identity")
		}
	}
	proof = MergeProof{Head: shas[0], DefaultHead: shas[1], DefaultRef: target, VerifiedAt: time.Now().UTC(), Method: "ancestor"}
	result, err = commands.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: []string{"merge-base", "--is-ancestor", proof.Head, proof.DefaultHead}})
	if err == nil && result.ExitCode == 0 {
		return proof, nil
	}
	if err != nil || result.ExitCode != 1 {
		return proof, fmt.Errorf("cleanup: merged ancestry could not be verified")
	}
	result, err = commands.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: []string{"diff", "--quiet", proof.Head, proof.DefaultHead, "--"}})
	if err == nil && result.ExitCode == 0 {
		proof.Method = "content-equal"
		return proof, nil
	}
	return proof, fmt.Errorf("cleanup: work is not proven merged into %s; retain branch, worktree and evidence", target)
}
