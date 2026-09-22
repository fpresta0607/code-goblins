package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
