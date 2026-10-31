package memory

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationInventoryRetainsEverySourceAndKeepsApprovalItemSpecific(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"overlord.md":        []byte("# Rulings\r\n## Approval\r\nKeep the exact words: caf\u00e9.\r\n"),
		"projects.md":        []byte("# Projects\n- project-a: a project map\n"),
		"memory/MEMORY.md":   []byte("# Index\n- [One](one.md) - a fact\n"),
		"memory/one.md":      []byte("A pinned project fact. <!--P-->\n"),
		"memory/two.md":      []byte("A pinned project fact. <!--P-->\n"),
		"memory/cold/old.md": []byte("An archived fact and its source.\n"),
		"memory-archive.md":  []byte("# Archive\n## 2026-10-30\nOriginal provenance.\n"),
		"memory/empty.md":    {},
	}
	for name, content := range files {
		writeMemoryFile(t, dir, name, content)
	}
	plan, err := PlanMigration(dir)
	if err != nil || !plan.IsDryRun || len(plan.Files) != len(files) {
		t.Fatalf("inventory=%+v, %v", plan, err)
	}
	ids := make(map[string]bool)
	for _, file := range plan.Files {
		original := files[file.Path]
		var retained bytes.Buffer
		for _, move := range file.Moves {
			if ids[move.ID] {
				t.Fatalf("equal content at different owners collided: %s", move.ID)
			}
			ids[move.ID] = true
			retained.Write(original[move.Start:move.End])
			if move.SHA256 != Hash(original[move.Start:move.End]) || move.Scope == "" || move.Destination == "" {
				t.Fatalf("inventory lost exact scope or bytes: %+v", move)
			}
			if file.Path == "overlord.md" && (!move.NeedsApproval || move.Authority != "ruling") {
				t.Fatal("legacy chronology was silently treated as disposable authority")
			}
			if (file.Path == "memory/one.md" || file.Path == "memory/two.md") && !move.NeedsApproval {
				t.Fatal("an explicit pin lost its per-item approval boundary")
			}
			if file.Path == "memory-archive.md" && move.Destination != file.Path {
				t.Fatal("existing archive was proposed for another automatic relocation")
			}
		}
		if !bytes.Equal(original, retained.Bytes()) || file.RetainedSHA256 != Hash(original) || file.SHA256 != Hash(original) {
			t.Fatalf("inventory failed lossless byte reconstruction for %s", file.Path)
		}
		current, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file.Path)))
		if err != nil || !bytes.Equal(original, current) {
			t.Fatal("inventory changed its source")
		}
	}
	if plan.SourceBytes != plan.RetainedBytes || len(plan.PlanSHA256) != 64 {
		t.Fatal("inventory lacks complete retained-byte proof")
	}
}

func TestMemoryReadsRejectEscapingPathsAndUnreadableSources(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../outside.md", "/outside.md", "C:/outside.md", "memory\\outside.md"} {
		if _, err := ReadData(dir, name); err == nil {
			t.Fatalf("escaped the private source owner through %q", name)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "overlord.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanMigration(dir); err == nil {
		t.Fatal("unreadable authority was counted as empty retained memory")
	}
}
