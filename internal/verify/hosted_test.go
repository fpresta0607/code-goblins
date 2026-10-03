package verify

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

func hostedFixture() (hostedIdentity, hostedBundle) {
	identity := hostedIdentity{
		Repository: "fixture/project", Module: "example.com/m", Budget: 90 * time.Minute, Head: strings.Repeat("a", 40), Main: strings.Repeat("b", 40),
		Tree: strings.Repeat("c", 40), Toolchain: "go1.26.5 windows/amd64", Environment: strings.Repeat("e", 64),
		Inputs: map[string]string{}, Packages: []string{"example.com/m/cmd/cfo", "example.com/m/internal/spawn", "example.com/m/internal/supervisor", "example.com/m/other"},
		Required: []string{"example.com/m/internal/spawn"},
	}
	for _, name := range hostedInputPaths {
		identity.Inputs[name] = strings.Repeat("f", 40)
	}
	bundle := hostedBundle{
		Run:      hostedRun{ID: 123, Attempt: 2, Head: identity.Head, Event: "pull_request", Path: ".github/workflows/go.yml", Status: "completed", Conclusion: "success", URL: "https://github.com/fixture/project/actions/runs/123"},
		Checkout: hostedCommit{SHA: strings.Repeat("d", 40)},
	}
	bundle.Checkout.Tree.SHA = identity.Tree
	bundle.Checkout.Parents = []hostedParent{{SHA: identity.Main}, {SHA: identity.Head}}
	for _, name := range []string{"frontend", "test"} {
		bundle.Jobs = append(bundle.Jobs, hostedJob{ID: int64(len(bundle.Jobs) + 1), RunID: 123, Attempt: 2, Name: name, Status: "completed", Conclusion: "success"})
	}
	for index, shard := range hostedShards {
		job := hostedJob{ID: int64(index + 3), RunID: 123, Attempt: 2, Name: "go (" + shard.Name + ")", Status: "completed", Conclusion: "success"}
		job.Steps = []hostedStep{{Name: hostedTestStep, Status: "completed", Conclusion: "success"}, {Name: hostedUploadStep, Status: "completed", Conclusion: "success"}}
		job.Steps[0].StartedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		job.Steps[0].CompletedAt = job.Steps[0].StartedAt.Add(time.Second)
		if shard.Name == "rest" {
			job.Steps = append(job.Steps, hostedStep{Name: "Run go vet ./...", Status: "completed", Conclusion: "success"})
		}
		bundle.Jobs = append(bundle.Jobs, job)
		manifest := hostedManifest{
			Version: 1, Repository: identity.Repository, RunID: 123, Attempt: 2, Shard: shard.Name,
			Head: identity.Head, Main: identity.Main, Checkout: bundle.Checkout.SHA, Tree: identity.Tree,
			Inputs: identity.Inputs, Toolchain: identity.Toolchain, Environment: identity.Environment, CGO: "0",
			RunTests: shard.Run, SkipTests: shard.Skip, Vet: shard.Name == "rest", AllPackages: identity.Packages,
		}
		for _, name := range identity.Packages {
			if shard.covers(name, identity.Module) {
				manifest.Results = append(manifest.Results, hostedPackage{Package: name, Status: "passed", Seconds: 1})
			}
		}
		artifact := hostedArtifact{ID: int64(index + 100), Name: "cfo-go-evidence-2-" + shard.Name, Digest: "sha256:" + strings.Repeat("1", 64), Size: 512}
		artifact.WorkflowRun.ID, artifact.WorkflowRun.Head = 123, identity.Head
		bundle.Artifacts = append(bundle.Artifacts, artifact)
		bundle.Manifests = append(bundle.Manifests, manifest)
	}
	return identity, bundle
}

func TestHostedEvidenceRequiresExactIdentityAndCompleteSuccessfulCoverage(t *testing.T) {
	for _, test := range []struct {
		name    string
		change  func(*hostedIdentity, *hostedBundle)
		isValid bool
	}{
		{"complete", func(_ *hostedIdentity, _ *hostedBundle) {}, true},
		{"no tests", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[6].Results[0].Status = "no_tests" }, true},
		{"full", func(identity *hostedIdentity, _ *hostedBundle) { identity.Required = []string{"./..."} }, true},
		{"head changed", func(identity *hostedIdentity, _ *hostedBundle) { identity.Head = strings.Repeat("9", 40) }, false},
		{"main changed", func(identity *hostedIdentity, _ *hostedBundle) { identity.Main = strings.Repeat("9", 40) }, false},
		{"merge tree changed", func(identity *hostedIdentity, _ *hostedBundle) { identity.Tree = strings.Repeat("9", 40) }, false},
		{"toolchain changed", func(identity *hostedIdentity, _ *hostedBundle) { identity.Toolchain = "go1.26.6 windows/amd64" }, false},
		{"build flags changed", func(identity *hostedIdentity, _ *hostedBundle) { identity.Environment = strings.Repeat("9", 64) }, false},
		{"input changed", func(identity *hostedIdentity, _ *hostedBundle) {
			identity.Inputs = map[string]string{"go.mod": strings.Repeat("9", 40)}
		}, false},
		{"wrong repository", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[0].Repository = "other/project" }, false},
		{"manifest head changed", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[0].Head = strings.Repeat("9", 40) }, false},
		{"manifest main changed", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[0].Main = strings.Repeat("9", 40) }, false},
		{"manifest checkout changed", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[0].Checkout = strings.Repeat("9", 40) }, false},
		{"manifest tree changed", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[0].Tree = strings.Repeat("9", 40) }, false},
		{"manifest input changed", func(_ *hostedIdentity, bundle *hostedBundle) {
			bundle.Manifests[0].Inputs = maps.Clone(bundle.Manifests[0].Inputs)
			bundle.Manifests[0].Inputs["go.mod"] = strings.Repeat("9", 40)
		}, false},
		{"incomplete jobs", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Jobs = bundle.Jobs[:8] }, false},
		{"extra job", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Jobs = append(bundle.Jobs, bundle.Jobs[0]) }, false},
		{"failed frontend", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Jobs[0].Conclusion = "failure" }, false},
		{"skipped aggregate", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Jobs[1].Conclusion = "skipped" }, false},
		{"pending job", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Jobs[2].Status = "in_progress" }, false},
		{"missing upload step", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Jobs[2].Steps = bundle.Jobs[2].Steps[:1] }, false},
		{"failed test step", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Jobs[2].Steps[0].Conclusion = "failure" }, false},
		{"unreadable test timing", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Jobs[2].Steps[0].StartedAt = time.Time{} }, false},
		{"over test budget", func(identity *hostedIdentity, bundle *hostedBundle) {
			bundle.Jobs[2].Steps[0].CompletedAt = bundle.Jobs[2].Steps[0].StartedAt.Add(identity.Budget + time.Second)
		}, false},
		{"wrong job run", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Jobs[2].RunID++ }, false},
		{"missing shard", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests = bundle.Manifests[:6] }, false},
		{"wrong attempt", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[0].Attempt++ }, false},
		{"wrong pattern", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[0].RunTests = "OnlyOneTest" }, false},
		{"missing vet", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[6].Vet = false }, false},
		{"wrong package set", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[0].AllPackages = []string{"only/one"} }, false},
		{"missing result", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[0].Results = nil }, false},
		{"duplicate result", func(_ *hostedIdentity, bundle *hostedBundle) {
			bundle.Manifests[0].Results = append(bundle.Manifests[0].Results, bundle.Manifests[0].Results[0])
		}, false},
		{"failed package", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[0].Results[0].Status = "failed" }, false},
		{"unfinished package", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[0].Results[0].Status = "running" }, false},
		{"skipped test", func(_ *hostedIdentity, bundle *hostedBundle) {
			bundle.Manifests[0].Results[0].Skipped = []string{"TestOptional"}
		}, false},
		{"over timeout", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[0].Results[0].Seconds = 1201 }, false},
		{"negative time", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[0].Results[0].Seconds = -1 }, false},
		{"cgo compiler unproved", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Manifests[0].CGO = "1" }, false},
		{"expired artifact", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Artifacts[0].Expired = true }, false},
		{"wrong artifact run", func(_ *hostedIdentity, bundle *hostedBundle) { bundle.Artifacts[0].WorkflowRun.ID++ }, false},
		{"wrong checkout parents", func(_ *hostedIdentity, bundle *hostedBundle) {
			bundle.Checkout.Parents[0].SHA = strings.Repeat("9", 40)
		}, false},
		{"required package absent", func(identity *hostedIdentity, _ *hostedBundle) { identity.Required = []string{"example.com/m/missing"} }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity, bundle := hostedFixture()
			test.change(&identity, &bundle)
			receipt, err := validateHosted(identity, bundle)
			if (err == nil) != test.isValid {
				t.Fatalf("valid=%v, receipt=%+v, error=%v", test.isValid, receipt, err)
			}
			if !test.isValid {
				if receipt != nil {
					t.Fatal("declined evidence returned a reusable receipt")
				}
				return
			}
			if receipt.Head != identity.Head || receipt.Main != identity.Main || receipt.Tree != identity.Tree || receipt.RunID != 123 || len(receipt.Jobs) != 9 || len(receipt.Artifacts) != 7 || !slices.Equal(receipt.Packages, identity.Packages) {
				t.Fatalf("receipt lost its exact verification proof: %+v", receipt)
			}
		})
	}
}
