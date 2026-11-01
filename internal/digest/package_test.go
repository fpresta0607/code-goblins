package digest

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/memory"
)

func TestCompleteStartupPackagesMeasureBeforeAndAfterRetrievedComposition(t *testing.T) {
	// Actual composer and shipped core; global rules, discovery entries and task
	// brief are synthetic captures, not claims about provider-injected prompts.
	h := bigHome(t, 10, 20)
	var before bytes.Buffer
	if err := Compose(h, os.Getpid(), "package", &before); err != nil {
		t.Fatal(err)
	}
	catalog := memory.Catalog{Version: 1}
	var activeAuthority [][]byte
	for _, name := range []string{"projects.md", "overlord.md", "memory/MEMORY.md"} {
		content, err := os.ReadFile(filepath.Join(h.Data, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		boundary := bytes.IndexByte(content, '\n') + 1
		activeAuthority = append(activeAuthority, content[:boundary])
		stem := strings.TrimSuffix(filepath.Base(name), ".md")
		entries := []memory.Entry{{ID: stem + "-active", Start: 0, End: boundary, SHA256: memory.Hash(content[:boundary]), Authority: "ruling", Location: "warm"}}
		id := stem + "-historical"
		// Synthetic explicit per-item approval is the independent oracle. No
		// original source bytes are changed to produce the shorter startup.
		entries = append(entries, memory.Entry{ID: id, Start: boundary, End: len(content), SHA256: memory.Hash(content[boundary:]), Authority: "history", Location: "cold", Trigger: "historical investigation", Topics: []string{"history"}, Approval: &memory.Approval{By: "Overlord", Quote: "Offload " + id + " to its original named source owner.", Date: "2026-10-30", Destination: name + "#" + id}})
		catalog.Sources = append(catalog.Sources, memory.Source{Path: name, SHA256: memory.Hash(content), Entries: entries})
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	var after bytes.Buffer
	if err := Compose(h, os.Getpid(), "package", &after); err != nil {
		t.Fatal(err)
	}
	for _, content := range activeAuthority {
		if !bytes.Contains(after.Bytes(), content) {
			t.Fatalf("package omitted delivered warm authority: %q", content)
		}
	}
	core, err := fs.ReadFile(codegoblins.Contract, "AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, capture := range []struct {
		name   string
		digest []byte
	}{{"before", before.Bytes()}, {"after", after.Bytes()}} {
		root := t.TempDir()
		inputs := []memory.Input{}
		for _, input := range []struct{ kind, text string }{
			{"core", string(core)},
			{"global", "Synthetic global instructions: preserve user authority.\n"},
			{"memory", ""},
			{"skills-index", "stow: before reset, curate memory and report pending capture.\n"},
			{"brief", "Synthetic task: retrieve relevant authority before changing the project.\n"},
			{"digest", string(capture.digest)},
		} {
			if err := os.WriteFile(filepath.Join(root, input.kind), []byte(input.text), 0o600); err != nil {
				t.Fatal(err)
			}
			inputs = append(inputs, memory.Input{Kind: input.kind, Path: input.kind, Reason: "memory source text is already included in the delivered digest"})
		}
		packages := []memory.Package{{Harness: "codex", Inputs: inputs}, {Harness: "claude", Inputs: inputs}, {Harness: "pi", Inputs: inputs}}
		reports, err := memory.MeasurePackages(packages, root, 20000)
		if err != nil {
			t.Fatal(err)
		}
		for _, report := range reports {
			t.Logf("synthetic %s %s full package: bytes=%d estimated_tokens=%d; digest=%d; estimate is not billing or RAM", capture.name, report.Harness, report.Bytes, report.EstimatedTokens, len(capture.digest))
			if capture.name == "after" && (report.IsOverBudget || report.Bytes >= before.Len()) {
				t.Fatalf("retrieved full startup is not bounded: %+v", report)
			}
		}
	}
}
