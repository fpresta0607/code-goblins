package memory

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

func TestMigrationInventoryPreservesCatalogRangesAuthorityAndRouting(t *testing.T) {
	dir := t.TempDir()
	first := "Keep this preference exactly.\n"
	second := "An aging incident. <!--a:2026-10-30-->\n"
	content := []byte(first + second)
	writeMemoryFile(t, dir, "memory/preferences.md", content)
	catalog := Catalog{Version: 1, Sources: []Source{{Path: "memory/preferences.md", SHA256: Hash(content), Entries: []Entry{
		{ID: "preference", End: len(first), SHA256: Hash([]byte(first)), Authority: "ruling", Location: "warm", Projects: []string{"northwind"}, Tasks: []string{"task-7"}, Topics: []string{"preferences"}},
		{ID: "incident", Start: len(first), End: len(content), SHA256: Hash([]byte(second)), Authority: "history", Location: "cold", Trigger: "investigating the incident", Topics: []string{"incident"}},
	}}}}
	catalog.Layers = []Layer{{Name: "team", Classes: []string{"preferences"}, Owner: "team owner", Capture: "Report to the team owner.", Correction: "Retain the earlier source."}}
	writeCatalog(t, dir, catalog)

	plan, err := PlanMigration(dir)

	if err != nil || len(plan.Files) != 1 || len(plan.Files[0].Moves) != 2 {
		t.Fatalf("catalog inventory=%+v, %v", plan, err)
	}
	if len(plan.Layers) != 1 || plan.Layers[0].Owner != "team owner" || plan.Layers[0].Capture != "Report to the team owner." || plan.Layers[0].Correction != "Retain the earlier source." {
		t.Fatalf("inventory lost declared tier ownership: %+v", plan.Layers)
	}
	preference, incident := plan.Files[0].Moves[0], plan.Files[0].Moves[1]
	if preference.ID != "preference" || preference.End != len(first) || preference.Authority != "ruling" || preference.Tier != "pinned" || !preference.NeedsApproval {
		t.Fatalf("declared ruling lost its classification or range: %+v", preference)
	}
	for _, scope := range []string{"northwind", "task-7", "preferences"} {
		if !strings.Contains(preference.Scope, scope) {
			t.Fatalf("declared scope %q was lost: %+v", scope, preference)
		}
	}
	if incident.ID != "incident" || incident.Start != len(first) || incident.Tier != "aging" || incident.NeedsApproval || !strings.Contains(incident.Scope, "incident") {
		t.Fatalf("explicit aging history lost its classification: %+v", incident)
	}
	writeMemoryFile(t, dir, CatalogPath, []byte(`{"version":1,"sources":[]}`))
	if _, err := PlanMigration(dir); err == nil {
		t.Fatal("invalid declared authority silently selected legacy classification")
	}
}

func TestMigrationInventoryProtectsFactsPinnedByTheLegacyIndex(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "memory/MEMORY.md", []byte("# Index\n- [Preference](preferences.md) - owner choice <!--P-->\n- [Nested](nested/fact.md) - another pin <!--P-->\n"))
	for _, name := range []string{"memory/preferences.md", "memory/nested/fact.md"} {
		writeMemoryFile(t, dir, name, []byte("Unmarked fact bytes.\n"))
	}

	plan, err := PlanMigration(dir)

	if err != nil {
		t.Fatal(err)
	}
	for _, file := range plan.Files {
		for _, move := range file.Moves {
			if file.Path != "memory/MEMORY.md" && (!move.NeedsApproval || move.Tier != "pinned") {
				t.Fatalf("index pin did not protect its source: %+v", move)
			}
		}
	}
}

func TestMigrationInventoryRetainsUnsupportedMarkerBytesExplicitly(t *testing.T) {
	dir := t.TempDir()
	content := []byte("Original bytes <!--a:2026-10-30/100-->\r\n")
	writeMemoryFile(t, dir, "memory/fact.md", content)

	plan, err := PlanMigration(dir)

	if err != nil || plan.RetainedBytes != len(content) || plan.Files[0].RetainedSHA256 != Hash(content) {
		t.Fatalf("inventory did not retain unsupported marker bytes: %+v, %v", plan, err)
	}
	move := plan.Files[0].Moves[0]
	if move.ClassificationError == "" || !move.NeedsApproval {
		t.Fatalf("unclassified legacy memory was treated as disposable: %+v", move)
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
