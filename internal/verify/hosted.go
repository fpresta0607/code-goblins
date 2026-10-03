package verify

import (
	"encoding/hex"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	hostedTestStep      = "Run Go tests with isolated CFO state"
	hostedUploadStep    = "Keep reusable Go evidence"
	hostedManifestLimit = 1 << 20
)

var hostedInputPaths = []string{"go.mod", "go.sum", "config/verify.json", ".no-mistakes.yaml", ".github/workflows/go.yml", ".github/go-evidence.ps1"}

type hostedShard struct{ Name, Package, Run, Skip string }

var hostedShards = []hostedShard{
	{Name: "cmd-cfo-1", Package: "cmd/cfo", Run: "Update|Recover"},
	{Name: "cmd-cfo-2", Package: "cmd/cfo", Skip: "Update|Recover"},
	{Name: "spawn-1", Package: "internal/spawn", Run: "Spawn"},
	{Name: "spawn-2", Package: "internal/spawn", Skip: "Spawn"},
	{Name: "supervisor-1", Package: "internal/supervisor", Run: "^Test[A-M]"},
	{Name: "supervisor-2", Package: "internal/supervisor", Skip: "^Test[A-M]"},
	{Name: "rest"},
}

func (shard hostedShard) covers(name, module string) bool {
	if shard.Package != "" {
		return name == module+"/"+shard.Package
	}
	return !slices.Contains([]string{module + "/cmd/cfo", module + "/internal/spawn", module + "/internal/supervisor"}, name)
}

// HostedReceipt identifies the external checks reused instead of local execution.
type HostedReceipt struct {
	URL         string                  `json:"url"`
	RunID       int64                   `json:"run_id"`
	Head        string                  `json:"head"`
	Main        string                  `json:"main"`
	Checkout    string                  `json:"checkout"`
	Tree        string                  `json:"tree"`
	Toolchain   string                  `json:"toolchain"`
	Environment string                  `json:"environment"`
	Inputs      map[string]string       `json:"inputs"`
	Packages    []string                `json:"packages"`
	Jobs        []HostedJobReceipt      `json:"jobs"`
	Artifacts   []HostedArtifactReceipt `json:"artifacts"`
}

type HostedJobReceipt struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Attempt     int     `json:"attempt"`
	TestSeconds float64 `json:"test_seconds,omitempty"`
}

type HostedArtifactReceipt struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type hostedIdentity struct {
	Repository, Module, Head, Main, Tree, Toolchain, Environment string
	Inputs                                                       map[string]string
	Packages, Required                                           []string
	Budget                                                       time.Duration
}

type hostedManifest struct {
	Version     int               `json:"version"`
	Repository  string            `json:"repository"`
	RunID       int64             `json:"run_id"`
	Attempt     int               `json:"attempt"`
	Shard       string            `json:"shard"`
	Head        string            `json:"head"`
	Main        string            `json:"main"`
	Checkout    string            `json:"checkout"`
	Tree        string            `json:"tree"`
	Inputs      map[string]string `json:"inputs"`
	Toolchain   string            `json:"toolchain"`
	Environment string            `json:"environment"`
	CGO         string            `json:"cgo"`
	RunTests    string            `json:"run_tests"`
	SkipTests   string            `json:"skip_tests"`
	Vet         bool              `json:"vet"`
	AllPackages []string          `json:"all_packages"`
	Results     []hostedPackage   `json:"results"`
}

type hostedPackage struct {
	Package string   `json:"package"`
	Status  string   `json:"status"`
	Seconds float64  `json:"seconds"`
	Skipped []string `json:"skipped"`
}

type hostedRun struct {
	ID         int64  `json:"id"`
	Attempt    int    `json:"run_attempt"`
	Head       string `json:"head_sha"`
	Event      string `json:"event"`
	Path       string `json:"path"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"html_url"`
}

type hostedJob struct {
	ID         int64        `json:"id"`
	RunID      int64        `json:"run_id"`
	Attempt    int          `json:"run_attempt"`
	Name       string       `json:"name"`
	Status     string       `json:"status"`
	Conclusion string       `json:"conclusion"`
	Steps      []hostedStep `json:"steps"`
}

type hostedStep struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

type hostedArtifact struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Digest      string `json:"digest"`
	Size        int64  `json:"size_in_bytes"`
	Expired     bool   `json:"expired"`
	WorkflowRun struct {
		ID   int64  `json:"id"`
		Head string `json:"head_sha"`
	} `json:"workflow_run"`
}

type hostedParent struct {
	SHA string `json:"sha"`
}
type hostedCommit struct {
	SHA     string         `json:"sha"`
	Tree    hostedParent   `json:"tree"`
	Parents []hostedParent `json:"parents"`
}

type hostedBundle struct {
	Run       hostedRun
	Checkout  hostedCommit
	Jobs      []hostedJob
	Artifacts []hostedArtifact
	Manifests []hostedManifest
}

func validateHosted(want hostedIdentity, bundle hostedBundle) (*HostedReceipt, error) {
	decline := func(reason string) (*HostedReceipt, error) { return nil, fmt.Errorf("hosted evidence: %s", reason) }
	if !hostedHash(want.Head, 40) || !hostedHash(want.Main, 40) || !hostedHash(want.Tree, 40) || !hostedHash(want.Environment, 64) || len(want.Inputs) != len(hostedInputPaths) || want.Budget <= 0 {
		return decline("local input identity is incomplete")
	}
	for _, name := range hostedInputPaths {
		if !hostedHash(want.Inputs[name], 40) {
			return decline("an input fingerprint is missing")
		}
	}
	if len(want.Packages) == 0 || !slices.IsSorted(want.Packages) || len(slices.Compact(slices.Clone(want.Packages))) != len(want.Packages) {
		return decline("local package scope is ambiguous")
	}
	for _, name := range want.Required {
		if name != "./..." && !slices.Contains(want.Packages, name) {
			return decline("a required package has no hosted coverage")
		}
	}
	run := bundle.Run
	if run.ID <= 0 || run.Attempt <= 0 || run.Head != want.Head || run.Event != "pull_request" || run.Path != ".github/workflows/go.yml" || run.Status != "completed" || run.Conclusion != "success" || run.URL != "https://github.com/"+want.Repository+"/actions/runs/"+strconv.FormatInt(run.ID, 10) {
		return decline("the workflow run does not prove a successful matching head")
	}
	checkout := bundle.Checkout
	if !hostedHash(checkout.SHA, 40) || checkout.Tree.SHA != want.Tree || len(checkout.Parents) != 2 || checkout.Parents[0].SHA != want.Main || checkout.Parents[1].SHA != want.Head {
		return decline("the actual tested merge differs from current main and head")
	}
	if len(bundle.Jobs) != len(hostedShards)+2 || len(bundle.Manifests) != len(hostedShards) || len(bundle.Artifacts) != len(hostedShards) {
		return decline("the required job or artifact set is incomplete")
	}
	jobs := map[string]hostedJob{}
	jobIDs := map[int64]bool{}
	for _, job := range bundle.Jobs {
		if job.ID <= 0 || jobIDs[job.ID] || jobs[job.Name].ID != 0 || job.RunID != run.ID || job.Attempt <= 0 || job.Attempt > run.Attempt || job.Status != "completed" || job.Conclusion != "success" {
			return decline("a job is missing, repeated, unfinished or unsuccessful")
		}
		jobs[job.Name], jobIDs[job.ID] = job, true
	}
	if jobs["frontend"].ID == 0 || jobs["test"].ID == 0 {
		return decline("frontend or the required aggregate did not pass")
	}
	artifacts := map[string]hostedArtifact{}
	artifactIDs := map[int64]bool{}
	for _, artifact := range bundle.Artifacts {
		if artifact.ID <= 0 || artifactIDs[artifact.ID] || artifacts[artifact.Name].ID != 0 || artifact.Expired || artifact.Size <= 0 || artifact.Size > hostedManifestLimit || artifact.WorkflowRun.ID != run.ID || artifact.WorkflowRun.Head != want.Head || !strings.HasPrefix(artifact.Digest, "sha256:") || !hostedHash(strings.TrimPrefix(artifact.Digest, "sha256:"), 64) {
			return decline("an artifact is expired, ambiguous or belongs to another run")
		}
		artifacts[artifact.Name], artifactIDs[artifact.ID] = artifact, true
	}
	manifests := map[string]hostedManifest{}
	for _, manifest := range bundle.Manifests {
		if _, exists := manifests[manifest.Shard]; exists {
			return decline("a shard proof is repeated")
		}
		manifests[manifest.Shard] = manifest
	}
	receipt := &HostedReceipt{URL: run.URL, RunID: run.ID, Head: want.Head, Main: want.Main, Checkout: checkout.SHA, Tree: want.Tree, Toolchain: want.Toolchain, Environment: want.Environment, Inputs: maps.Clone(want.Inputs), Packages: slices.Clone(want.Packages)}
	for _, job := range bundle.Jobs {
		receipt.Jobs = append(receipt.Jobs, HostedJobReceipt{ID: job.ID, Name: job.Name, Attempt: job.Attempt})
	}
	for _, shard := range hostedShards {
		job, manifest := jobs["go ("+shard.Name+")"], manifests[shard.Name]
		artifact := artifacts[fmt.Sprintf("cfo-go-evidence-%d-%s", job.Attempt, shard.Name)]
		if job.ID == 0 || artifact.ID == 0 {
			return decline("a required shard has no matching job and artifact")
		}
		if manifest.Version != 1 || manifest.Repository != want.Repository || manifest.RunID != run.ID || manifest.Attempt != job.Attempt || manifest.Head != want.Head || manifest.Main != want.Main || manifest.Checkout != checkout.SHA || manifest.Tree != want.Tree || manifest.Toolchain != want.Toolchain || manifest.Environment != want.Environment || manifest.CGO != "0" || !maps.Equal(manifest.Inputs, want.Inputs) || !slices.Equal(manifest.AllPackages, want.Packages) || manifest.RunTests != shard.Run || manifest.SkipTests != shard.Skip || manifest.Vet != (shard.Name == "rest") {
			return decline("a shard's tested identity, inputs, environment or filters differ")
		}
		for _, name := range []string{hostedTestStep, hostedUploadStep} {
			if _, err := hostedPassedStep(job, name); err != nil {
				return nil, err
			}
		}
		testStep, _ := hostedPassedStep(job, hostedTestStep)
		elapsed := testStep.CompletedAt.Sub(testStep.StartedAt)
		if testStep.StartedAt.IsZero() || testStep.CompletedAt.IsZero() || elapsed < 0 || elapsed > want.Budget {
			return decline("a hosted test step has unreadable timing or exceeded the local budget")
		}
		for index := range receipt.Jobs {
			if receipt.Jobs[index].ID == job.ID {
				receipt.Jobs[index].TestSeconds = elapsed.Seconds()
			}
		}
		if shard.Name == "rest" {
			if _, err := hostedPassedStep(job, "Run go vet ./..."); err != nil {
				return nil, err
			}
		}
		expected := map[string]bool{}
		for _, name := range want.Packages {
			if shard.covers(name, want.Module) {
				expected[name] = true
			}
		}
		if len(manifest.Results) != len(expected) {
			return decline("a shard did not finish every selected package")
		}
		for _, result := range manifest.Results {
			if !expected[result.Package] || (result.Status != "passed" && result.Status != "no_tests") || len(result.Skipped) != 0 || math.IsNaN(result.Seconds) || math.IsInf(result.Seconds, 0) || result.Seconds < 0 || result.Seconds > (20*time.Minute).Seconds() {
				return decline("a package is missing, repeated, skipped, failed or over its timeout")
			}
			delete(expected, result.Package)
		}
		if len(expected) != 0 {
			return decline("a shard's package coverage is incomplete")
		}
		receipt.Artifacts = append(receipt.Artifacts, HostedArtifactReceipt{ID: artifact.ID, Name: artifact.Name, Digest: artifact.Digest})
	}
	return receipt, nil
}

func hostedPassedStep(job hostedJob, name string) (hostedStep, error) {
	var found []hostedStep
	for _, step := range job.Steps {
		if step.Name == name {
			found = append(found, step)
		}
	}
	if len(found) != 1 || found[0].Status != "completed" || found[0].Conclusion != "success" {
		return hostedStep{}, fmt.Errorf("hosted evidence: job %s has no unique successful %s step", job.Name, name)
	}
	return found[0], nil
}

func hostedHash(value string, size int) bool {
	if len(value) != size || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
