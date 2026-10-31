package digest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestComposeUsesVerifiedWarmMemoryInsteadOfHistoricalRulings(t *testing.T) {
	// Arrange: the approved cold range stays byte-identical in its original file.
	h := newDigestHome(t)
	active := "Never merge without an explicit instruction.\n"
	history := strings.Repeat("A completed incident with no current action.\n", 500)
	content := active + history
	if err := os.MkdirAll(filepath.Join(h.Data, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "overlord.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := map[string]any{"version": 1, "sources": []any{map[string]any{
		"path": "overlord.md", "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(content))),
		"entries": []any{
			map[string]any{"id": "merge-authority", "start": 0, "end": len(active), "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(active))), "authority": "ruling", "location": "warm"},
			map[string]any{"id": "completed-incident", "start": len(active), "end": len(content), "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(history))), "authority": "history", "location": "cold", "trigger": "incident investigation", "topics": []string{"incident"}, "approval": map[string]string{"by": "Overlord", "quote": "Move completed-incident to cold retrieval.", "date": "2026-10-30", "destination": "overlord.md#completed-incident"}},
		},
	}}}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	var output bytes.Buffer
	if err := Compose(h, os.Getpid(), "warm", &output); err != nil {
		t.Fatal(err)
	}

	// Assert: this is the emitted startup interface, not a source-text assertion.
	if !strings.Contains(output.String(), active) || strings.Contains(output.String(), history) {
		t.Fatalf("startup did not select current authority independently of cold history: %s", output.String())
	}
	if !strings.Contains(output.String(), "cfo memory retrieve") || !strings.Contains(output.String(), "completed-incident") {
		t.Fatalf("startup omitted the cold retrieval route: %s", output.String())
	}
	if output.Len() > 18000 {
		t.Fatalf("startup unexpectedly grew with cold history: %d bytes", output.Len())
	}
	preserved, err := os.ReadFile(filepath.Join(h.Data, "overlord.md"))
	if err != nil || string(preserved) != content {
		t.Fatal("composition changed the original ruling bytes")
	}
}

func TestWarmStartupBoundsCanonicalStateWithoutAcknowledgingOmittedWakes(t *testing.T) {
	// Arrange: cold history and current state are separate bounded inputs.
	h := bigHome(t, 200, 200)
	for _, name := range []string{"projects.md", "overlord.md", filepath.Join("memory", "MEMORY.md")} {
		if err := os.Remove(filepath.Join(h.Data, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), []byte(`{"version":1,"sources":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	var output bytes.Buffer
	if err := Compose(h, os.Getpid(), "bounded", &output); err != nil {
		t.Fatal(err)
	}
	remaining, err := wake.Pending(h.State)

	// Assert: the remainder is retrievable and is never marked handled.
	if err != nil || len(remaining) != 200 || output.Len() > 18000 {
		t.Fatalf("state output=%d bytes, pending=%d, error=%v", output.Len(), len(remaining), err)
	}
	if !strings.Contains(output.String(), "cfo fleet-view") || !strings.Contains(output.String(), "cfo drain") {
		t.Fatal("bounded state omitted its canonical retrieval commands")
	}
}

func TestResumedWarmStartupRequiresChangedAuthorityToBeRead(t *testing.T) {
	h := newDigestHome(t)
	if err := os.MkdirAll(filepath.Join(h.Data, "memory"), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(words string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(h.Data, "overlord.md"), []byte(words), 0o600); err != nil {
			t.Fatal(err)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(words)))
		catalog := fmt.Sprintf(`{"version":1,"sources":[{"path":"overlord.md","sha256":%q,"entries":[{"id":"rule","start":0,"end":%d,"sha256":%q,"authority":"ruling","location":"warm"}]}]}`, hash, len(words), hash)
		if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), []byte(catalog), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("The first ruling.\n")
	composeBrief(t, h)
	write("The corrected ruling.\n")
	var output bytes.Buffer
	if err := ComposeBrief(h, os.Getpid(), "s1", false, true, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "do not read it again") || !strings.Contains(output.String(), "Read all of it") {
		t.Fatal("resume claimed this conversation already holds changed authority")
	}
}

func TestWarmStartupKeepsBudgetAndSourceFailuresExplicit(t *testing.T) {
	for _, scenario := range []string{"over budget", "corrupt source", "missing source", "absent sources"} {
		t.Run(scenario, func(t *testing.T) {
			h := newDigestHome(t)
			if err := os.MkdirAll(filepath.Join(h.Data, "memory"), 0o700); err != nil {
				t.Fatal(err)
			}
			words := "Preserve this exact pinned authority even when the allowance is small.\n"
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(words)))
			catalog := fmt.Sprintf(`{"version":1,"sources":[{"path":"overlord.md","sha256":%q,"entries":[{"id":"rule","start":0,"end":%d,"sha256":%q,"authority":"ruling","location":"warm"}]}]}`, hash, len(words), hash)
			if err := os.WriteFile(filepath.Join(h.Data, "overlord.md"), []byte(words), 0o600); err != nil {
				t.Fatal(err)
			}
			if scenario == "corrupt source" {
				if err := os.WriteFile(filepath.Join(h.Data, "overlord.md"), []byte("changed authority"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing source" || scenario == "absent sources" {
				if err := os.Remove(filepath.Join(h.Data, "overlord.md")); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "absent sources" {
				catalog = `{"version":1,"sources":[]}`
			}
			if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), []byte(catalog), 0o600); err != nil {
				t.Fatal(err)
			}
			if scenario == "over budget" {
				if err := os.MkdirAll(filepath.Join(h.Root, "config"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(h.Root, "config", "startup-memory-budget"), []byte("1"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			if err := Compose(h, os.Getpid(), scenario, &output); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "over budget":
				if !strings.Contains(output.String(), words) || !strings.Contains(output.String(), "MEMORY OVER BUDGET") {
					t.Fatal("a small budget silently dropped pinned authority")
				}
			case "absent sources":
				if !strings.Contains(output.String(), "MEMORY SOURCES ABSENT") {
					t.Fatal("an empty catalog concealed absent authority")
				}
			default:
				if !strings.Contains(output.String(), "MEMORY UNREADABLE") || strings.Contains(output.String(), words) {
					t.Fatal("invalid authority selected a silent legacy fallback")
				}
			}
		})
	}
}

func TestReadOnlyWarmPreviewUsesSelectiveRetrieval(t *testing.T) {
	h := newDigestHome(t)
	if err := os.MkdirAll(filepath.Join(h.Data, "memory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), []byte(`{"version":1,"sources":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := ComposeBrief(h, 0, "read-only", false, false, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "cfo memory retrieve") || strings.Contains(output.String(), "read "+filepath.Join(h.Data, "overlord.md")) {
		t.Fatal("read-only startup selected the full history instead of selective retrieval")
	}
	if _, err := os.Stat(filepath.Join(h.State, FullDigestFile)); !os.IsNotExist(err) {
		t.Fatal("read-only startup wrote a shared digest")
	}
}
