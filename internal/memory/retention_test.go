package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRetentionPlansVerifiedRecoveryWithoutRemovingProtectedWork(t *testing.T) {
	dir := t.TempDir()
	content := []byte("An exact completed task summary with its provenance.\n")
	writeMemoryFile(t, dir, "finished/summary.md", content)
	writeMemoryFile(t, dir, "archive/summary.md", content)
	base := RetentionProposal{Path: "finished/summary.md", Destination: "archive/summary.md", SHA256: Hash(content), Kind: "summary", TaskState: "completed", IsLanded: true, IsCaptureComplete: true}
	for _, test := range []struct {
		name          string
		change        func(*RetentionProposal)
		isRecoverable bool
		reason        string
	}{
		{"completed", func(*RetentionProposal) {}, true, "verified local recovery"},
		{"active", func(p *RetentionProposal) { p.TaskState = "active" }, false, "not completed"},
		{"held", func(p *RetentionProposal) { p.IsHeld = true }, false, "held"},
		{"unlanded", func(p *RetentionProposal) { p.IsLanded = false }, false, "unlanded"},
		{"capture incomplete", func(p *RetentionProposal) { p.IsCaptureComplete = false }, false, "capture"},
		{"pinned authority", func(p *RetentionProposal) { p.Kind = "authority" }, false, "authority"},
		{"missing copy", func(p *RetentionProposal) { p.Destination = "missing.md" }, false, "recovery unavailable"},
		{"stale source hash", func(p *RetentionProposal) { p.SHA256 = strings.Repeat("0", 64) }, false, "source hash changed"},
		{"self copy", func(p *RetentionProposal) { p.Destination = p.Path }, false, "independent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			proposal := base
			test.change(&proposal)
			plan, err := PlanRetention(dir, []RetentionProposal{proposal})
			if err != nil {
				t.Fatal(err)
			}
			if !plan.IsDryRun || plan.ReclaimedBytes != 0 || plan.IsRemoteVerified || len(plan.Items) != 1 {
				t.Fatalf("unsafe claims: %+v", plan)
			}
			item := plan.Items[0]
			if item.IsRecoverable != test.isRecoverable || !strings.Contains(item.Reason, test.reason) || item.Bytes != len(content) || item.SourceSHA256 != Hash(content) || item.SHA256 != proposal.SHA256 {
				t.Fatalf("recovery proof: %+v", item)
			}
			retained, err := ReadData(dir, base.Path)
			if err != nil || string(retained) != string(content) {
				t.Fatal("planner changed source")
			}
		})
	}
	first, err := PlanRetention(dir, []RetentionProposal{base})
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanRetention(dir, []RetentionProposal{base})
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("unchanged inventory is not repeatable")
	}
	encoded, _ := json.Marshal(first)
	if !strings.Contains(string(encoded), "proposal") {
		t.Fatal("unverified task-state declarations were treated as authority")
	}
}

func TestRetentionRejectsUnsafePathsAndMismatchedRecovery(t *testing.T) {
	dir := t.TempDir()
	writeMemoryFile(t, dir, "finished.md", []byte("complete"))
	writeMemoryFile(t, dir, "copy.md", []byte("incomplete"))
	base := RetentionProposal{Path: "finished.md", Destination: "copy.md", SHA256: Hash([]byte("complete")), Kind: "artifact", TaskState: "completed", IsLanded: true, IsCaptureComplete: true}
	plan, err := PlanRetention(dir, []RetentionProposal{base})
	if err != nil || plan.Items[0].IsRecoverable || !strings.Contains(plan.Items[0].Reason, "differ") {
		t.Fatalf("bad recovery: %+v %v", plan, err)
	}
	for _, path := range []string{"../private.md", "C:/outside.md", "missing.md"} {
		proposal := base
		proposal.Path = path
		if _, err := PlanRetention(dir, []RetentionProposal{proposal}); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	if _, err := PlanRetention(dir, []RetentionProposal{base, base}); err == nil {
		t.Fatal("accepted duplicate proposal")
	}
}

func TestRetentionProtectsGovernedAliasesAndRejectsHardlinkRecovery(t *testing.T) {
	dir, _ := catalogFixture(t)
	content, err := ReadData(dir, "overlord.md")
	if err != nil {
		t.Fatal(err)
	}
	writeMemoryFile(t, dir, "recovery.md", content)
	proposal := RetentionProposal{Path: "./overlord.md", Destination: "recovery.md", SHA256: Hash(content), Kind: "summary", TaskState: "completed", IsLanded: true, IsCaptureComplete: true}
	plan, err := PlanRetention(dir, []RetentionProposal{proposal})
	if err != nil || plan.Items[0].IsRecoverable || !strings.Contains(plan.Items[0].Reason, "authority") {
		t.Fatalf("authority alias unprotected: %+v %v", plan, err)
	}
	writeMemoryFile(t, dir, "finished.md", []byte("completed fact"))
	if err := os.Link(filepath.Join(dir, "finished.md"), filepath.Join(dir, "linked.md")); err != nil {
		t.Fatal(err)
	}
	proposal.Path, proposal.Destination, proposal.SHA256 = "finished.md", "linked.md", Hash([]byte("completed fact"))
	plan, err = PlanRetention(dir, []RetentionProposal{proposal})
	if err != nil || plan.Items[0].IsRecoverable || !strings.Contains(plan.Items[0].Reason, "independent") {
		t.Fatalf("hardlink recovery accepted: %+v %v", plan, err)
	}
}

func TestRetentionUsesFileIdentityForAuthorityDuplicatesAndRecovery(t *testing.T) {
	for _, test := range []struct {
		path       string
		isGoverned bool
	}{
		{"overlord.md", true},
		{"projects.md", true},
		{"memory-archive.md", true},
		{"memory/fact.md", true},
		{"finished/original.md", false},
	} {
		for _, aliasKind := range []string{"symlink", "hardlink", "case"} {
			t.Run(test.path+"/"+aliasKind, func(t *testing.T) {
				dir := t.TempDir()
				content := []byte("Exact source bytes with provenance.\n")
				writeMemoryFile(t, dir, test.path, content)
				writeMemoryFile(t, dir, "archive/recovery.md", content)
				alias := "finished/summary.md"
				if err := os.MkdirAll(filepath.Join(dir, "finished"), 0o700); err != nil {
					t.Fatal(err)
				}
				sourcePath := filepath.Join(dir, filepath.FromSlash(test.path))
				aliasPath := filepath.Join(dir, filepath.FromSlash(alias))
				switch aliasKind {
				case "symlink":
					if err := os.Symlink(sourcePath, aliasPath); err != nil {
						t.Skipf("symlink unavailable: %v", err)
					}
				case "hardlink":
					if err := os.Link(sourcePath, aliasPath); err != nil {
						t.Fatal(err)
					}
				case "case":
					alias = strings.ToUpper(test.path)
					if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(alias))); os.IsNotExist(err) {
						t.Skip("filesystem is case sensitive")
					} else if err != nil {
						t.Fatal(err)
					}
				}
				proposal := RetentionProposal{Path: alias, Destination: "archive/recovery.md", SHA256: Hash(content), Kind: "summary", TaskState: "completed", IsLanded: true, IsCaptureComplete: true}

				plan, err := PlanRetention(dir, []RetentionProposal{proposal})

				if err != nil {
					t.Fatal(err)
				}
				item := plan.Items[0]
				if test.isGoverned {
					if item.IsRecoverable || item.Reason != "retain authority; use migration-plan and individually named approval" {
						t.Fatalf("governed file alias lost authority protection: %+v", item)
					}
				} else if !item.IsRecoverable {
					t.Fatalf("independent recovery rejected: %+v", item)
				}
				original := proposal
				original.Path = test.path
				for _, proposals := range [][]RetentionProposal{{original, proposal}, {proposal, original}} {
					if _, err := PlanRetention(dir, proposals); err == nil || !strings.Contains(err.Error(), "duplicate retention proposal") {
						t.Fatalf("duplicate file identities accepted: %v", err)
					}
				}
				if !test.isGoverned {
					original.Destination = alias
					plan, err := PlanRetention(dir, []RetentionProposal{original})
					if err != nil || plan.Items[0].IsRecoverable || !strings.Contains(plan.Items[0].Reason, "independent") {
						t.Fatalf("alias accepted as independent recovery: %+v %v", plan, err)
					}
				}
			})
		}
	}
}

func TestRetentionAcceptsIndependentSourcesWithIdenticalBytes(t *testing.T) {
	dir := t.TempDir()
	content := []byte("Completed task facts.\n")
	writeMemoryFile(t, dir, "finished/first.md", content)
	writeMemoryFile(t, dir, "finished/second.md", content)
	proposals := []RetentionProposal{
		{Path: "finished/first.md", Destination: "finished/second.md", SHA256: Hash(content), Kind: "summary", TaskState: "completed", IsLanded: true, IsCaptureComplete: true},
		{Path: "finished/second.md", Destination: "finished/first.md", SHA256: Hash(content), Kind: "summary", TaskState: "completed", IsLanded: true, IsCaptureComplete: true},
	}

	plan, err := PlanRetention(dir, proposals)

	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 2 || !plan.Items[0].IsRecoverable || !plan.Items[1].IsRecoverable {
		t.Fatalf("independent files treated as aliases: %+v", plan)
	}
}
