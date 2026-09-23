package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLandedContentReadsEveryTaskChangeAndRejectsMissingContent(t *testing.T) {
	dir := gitFixture(t)
	g := Git{}
	ctx := context.Background()
	base, err := g.base(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	head, err := g.Head(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.verifyLandedContent(ctx, dir, dir, base, head, base); err == nil {
		t.Fatal("PR/branch state without actual content was accepted")
	}
	if err := g.verifyLandedContent(ctx, dir, dir, base, head, head); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "second.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "second.go"}, {"commit", "-qm", "Another task commit"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
	}
	second, err := g.Head(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.verifyLandedContent(ctx, dir, dir, base, second, head); err == nil {
		t.Fatal("earlier landed commit hid a missing later file")
	}
	if err := g.verifyLandedContent(ctx, dir, dir, base, second, second); err != nil {
		t.Fatal(err)
	}
}

// A mode-only or type-only change keeps the blob bytes identical, so landed
// content must compare whole tree entries rather than file contents.
func TestLandedContentComparesExactTreeEntries(t *testing.T) {
	dir := gitFixture(t)
	git := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	// Trees authored on other platforms can hold paths Windows cannot check out.
	git("", "config", "core.protectNTFS", "false")
	blob := func(content string) string { return git(content, "hash-object", "-w", "--stdin") }
	// commit applies "mode,object,path" or "-path" entries to parent's tree
	// without touching the working tree.
	commit := func(parent string, entries ...string) string {
		git("", "read-tree", parent)
		for _, entry := range entries {
			if path, removed := strings.CutPrefix(entry, "-"); removed {
				git("", "update-index", "--force-remove", "--", path)
			} else {
				git("", "update-index", "--add", "--cacheinfo", entry)
			}
		}
		return git("", "commit-tree", git("", "write-tree"), "-p", parent, "-m", "entries")
	}
	script, target, notes := blob("echo landed\n"), blob("target.txt"), blob("literal\n")
	base := commit(git("", "rev-parse", "HEAD"), "100644,"+script+",run.sh", "100644,"+target+",link", "100644,"+blob("bye\n")+",gone.txt", "100644,"+notes+",notes")
	head := commit(base, "100755,"+script+",run.sh", "120000,"+target+",link", "-gone.txt", "100644,"+notes+",:notes")
	g, ctx := Git{}, context.Background()
	for _, c := range []struct {
		name, main string
		landed     bool
	}{
		{"nothing landed", base, false},
		{"executable bit missing", commit(head, "100644,"+script+",run.sh"), false},
		{"symlink landed as a regular file", commit(head, "100644,"+target+",link"), false},
		{"deletion missing", commit(head, "100644,"+blob("bye\n")+",gone.txt"), false},
		{"literal path missing beside a same-content lookalike", commit(head, "-:notes"), false},
		{"squash with identical entries", git("", "commit-tree", head+"^{tree}", "-p", base, "-m", "squash"), true},
		{"task head", head, true},
	} {
		if err := g.verifyLandedContent(ctx, dir, dir, base, head, c.main); (err == nil) != c.landed {
			t.Errorf("%s: want landed=%v, verification error %v", c.name, c.landed, err)
		}
	}
}
