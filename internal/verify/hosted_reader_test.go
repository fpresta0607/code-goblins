package verify

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatetest"
)

type hostedFixtureCommands struct {
	identity                    hostedIdentity
	replies                     map[string][]byte
	environment                 []byte
	problem                     string
	headReads, mainReads, calls int
}

func (fixture *hostedFixtureCommands) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	fixture.calls++
	if err := ctx.Err(); err != nil {
		return execx.Result{}, err
	}
	if request.Name == "git" && slices.Contains(request.Args, "get-url") {
		if fixture.problem == "credential in origin" {
			origin := url.URL{Scheme: "https", Host: "github.com", Path: "/fixture/project.git", User: url.UserPassword("fixture", "fixture-private-value")}
			return execx.Result{Stdout: []byte(origin.String() + "\n")}, nil
		}
		return execx.Result{Stdout: []byte("https://github.com/fixture/project.git\n")}, nil
	}
	if request.OutputLimit <= 0 || !request.KillTree {
		return execx.Result{}, fmt.Errorf("proof process is not bounded: %+v", request)
	}
	for _, entry := range request.Env {
		name, _, _ := strings.Cut(entry, "=")
		if slices.Contains([]string{"CODEX_THREAD_ID", "CFO_HOME", "CFO_STATE_OVERRIDE", "NO_MISTAKES_GATE"}, strings.ToUpper(name)) {
			return execx.Result{}, fmt.Errorf("inherited %s reached proof process", name)
		}
	}
	var output []byte
	switch request.Name {
	case "git":
		switch request.Args[0] {
		case "status":
			if fixture.problem == "dirty" {
				output = []byte(" M package.go\n")
			}
		case "merge-tree":
			if !slices.Equal(request.Args, []string{"merge-tree", "--write-tree", "--no-messages", fixture.identity.Main, fixture.identity.Head}) {
				return execx.Result{}, fmt.Errorf("merge identity differs")
			}
			if fixture.problem == "merge conflict" {
				return execx.Result{ExitCode: 1}, nil
			}
			output = []byte(fixture.identity.Tree)
		case "rev-parse":
			if request.Args[1] == "HEAD" {
				fixture.headReads++
				output = []byte(fixture.identity.Head)
				if fixture.problem == "head changed" && fixture.headReads == 2 {
					output = []byte(strings.Repeat("9", 40))
				}
			} else {
				_, name, _ := strings.Cut(request.Args[1], ":")
				output = []byte(fixture.identity.Inputs[name])
				if fixture.problem == "producer differs from main" && request.Args[1] == fixture.identity.Main+":.github/go-evidence.ps1" {
					output = []byte(strings.Repeat("9", 40))
				}
			}
		default:
			return execx.Result{}, fmt.Errorf("unexpected Git read: %v", request.Args)
		}
	case "go":
		if request.Args[0] == "env" {
			output = fixture.environment
		} else if slices.Equal(request.Args, []string{"list", "./..."}) {
			output = []byte(strings.Join(fixture.identity.Packages, "\n"))
		} else {
			return execx.Result{}, fmt.Errorf("unexpected Go command: %v", request.Args)
		}
	case "gh":
		endpoint := request.Args[len(request.Args)-1]
		if request.Args[0] != "api" || request.Args[1] != "--method" || request.Args[2] != "GET" {
			return execx.Result{}, fmt.Errorf("not a read-only API request")
		}
		if fixture.problem == "API unreadable" {
			return execx.Result{ExitCode: 1}, nil
		}
		output = fixture.replies[endpoint]
		if strings.HasSuffix(endpoint, "/git/ref/heads/main") {
			fixture.mainReads++
			if fixture.problem == "main changed" && fixture.mainReads == 2 {
				output = []byte(`{"object":{"sha":"` + strings.Repeat("9", 40) + `"}}`)
			}
		}
		if fixture.problem == "metadata oversized" && endpoint == "repos/fixture/project" {
			output = []byte(strings.Repeat(" ", hostedManifestLimit+1))
		}
		if fixture.problem == "malformed metadata" && endpoint == "repos/fixture/project" {
			output = []byte("not JSON")
		}
		if output == nil {
			return execx.Result{}, fmt.Errorf("unexpected API endpoint: %s", endpoint)
		}
	default:
		return execx.Result{}, fmt.Errorf("unexpected process: %s", request.Name)
	}
	return execx.Result{Stdout: output}, nil
}

func TestHostedReaderNeverRecordsCredentialsFromAnUnsupportedOrigin(t *testing.T) {
	commands, plan := hostedReaderFixture(t)
	commands.problem = "credential in origin"
	receipt, err := (Hosted{Commands: commands}).Read(context.Background(), plan, 90*time.Minute)
	if err == nil || receipt != nil || strings.Contains(err.Error(), "fixture-private-value") {
		t.Fatalf("unsupported origin was recorded unsafely: receipt=%+v, error=%v", receipt, err)
	}
}

func hostedReaderFixture(t *testing.T) (*hostedFixtureCommands, gatetest.Plan) {
	t.Helper()
	identity, bundle := hostedFixture()
	settings := map[string]string{"CGO_ENABLED": "0", "GOAMD64": "v1", "GOARCH": "amd64", "GOARM64": "", "GOEXPERIMENT": "", "GOFLAGS": "", "GOOS": "windows", "GOVERSION": "go1.26.5", "GOWORK": ""}
	environment, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	identity.Environment, err = hostedEnvironment(environment, identity.Toolchain)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &hostedFixtureCommands{identity: identity, replies: map[string][]byte{}, environment: environment}
	fixture.replies["repos/fixture/project"] = []byte(`{"full_name":"fixture/project","default_branch":"main"}`)
	fixture.replies["repos/fixture/project/git/ref/heads/main"] = []byte(`{"object":{"sha":"` + identity.Main + `"}}`)
	for index := range bundle.Manifests {
		bundle.Manifests[index].Environment = identity.Environment
		manifest, err := json.Marshal(bundle.Manifests[index])
		if err != nil {
			t.Fatal(err)
		}
		archive := hostedZIP(t, map[string][]byte{"manifest.json": manifest})
		bundle.Artifacts[index].Digest = fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
		bundle.Artifacts[index].Size = int64(len(archive))
		fixture.replies[fmt.Sprintf("repos/fixture/project/actions/artifacts/%d/zip", bundle.Artifacts[index].ID)] = archive
	}
	runs, err := json.Marshal([]hostedRun{bundle.Run})
	if err != nil {
		t.Fatal(err)
	}
	fixture.replies["repos/fixture/project/actions/workflows/go.yml/runs?event=pull_request&status=completed&head_sha="+identity.Head+"&per_page=3"] = append(append([]byte(`{"workflow_runs":`), runs...), '}')
	jobs, err := json.Marshal(bundle.Jobs)
	if err != nil {
		t.Fatal(err)
	}
	fixture.replies["repos/fixture/project/actions/runs/123/jobs?filter=latest&per_page=100"] = append(append([]byte(`{"total_count":9,"jobs":`), jobs...), '}')
	artifacts, err := json.Marshal(bundle.Artifacts)
	if err != nil {
		t.Fatal(err)
	}
	fixture.replies["repos/fixture/project/actions/runs/123/artifacts?per_page=100"] = append(append([]byte(`{"total_count":7,"artifacts":`), artifacts...), '}')
	checkout, err := json.Marshal(bundle.Checkout)
	if err != nil {
		t.Fatal(err)
	}
	fixture.replies["repos/fixture/project/git/commits/"+bundle.Checkout.SHA] = checkout
	plan := gatetest.Plan{Root: t.TempDir(), Commit: identity.Head, Base: identity.Main, Module: identity.Module, Toolchain: identity.Toolchain, Level: gatetest.Affected, Tests: identity.Required, Vet: identity.Required}
	return fixture, plan
}

func TestHostedReaderRechecksCurrentHeadAndMainAndDeclinesUnreadableProof(t *testing.T) {
	for _, problem := range []string{"", "dirty", "merge conflict", "producer differs from main", "API unreadable", "metadata oversized", "malformed metadata", "head changed", "main changed"} {
		t.Run(problem, func(t *testing.T) {
			for _, name := range []string{"CODEX_THREAD_ID", "CFO_HOME", "CFO_STATE_OVERRIDE", "NO_MISTAKES_GATE"} {
				t.Setenv(name, "fixture-inherited")
			}
			commands, plan := hostedReaderFixture(t)
			commands.problem = problem
			receipt, err := (Hosted{Commands: commands}).Read(context.Background(), plan, 90*time.Minute)
			if (err == nil) != (problem == "") {
				t.Fatalf("problem=%q, receipt=%+v, error=%v", problem, receipt, err)
			}
			if problem == "" && (receipt == nil || len(receipt.Artifacts) != 7 || commands.headReads != 2 || commands.mainReads != 2) {
				t.Fatalf("proof was not freshly checked: %+v, %+v", receipt, commands)
			}
		})
	}
}

func TestHostedReaderNeverQueriesForFastDirtyOrEmptyPlans(t *testing.T) {
	for _, scenario := range []string{"fast", "dirty", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			commands, plan := hostedReaderFixture(t)
			switch scenario {
			case "fast":
				plan.Level = gatetest.Fast
			case "dirty":
				plan.Uncommitted = 1
			case "empty":
				plan.Tests = nil
			}
			if receipt, err := (Hosted{Commands: commands}).Read(context.Background(), plan, 90*time.Minute); err == nil || receipt != nil || commands.calls != 0 {
				t.Fatalf("ineligible proof read: %+v, %v, calls=%d", receipt, err, commands.calls)
			}
		})
	}
}

func TestHostedReaderRejectsManifestPayloadsSwappedBetweenNamedArtifacts(t *testing.T) {
	commands, plan := hostedReaderFixture(t)
	first := "repos/fixture/project/actions/artifacts/100/zip"
	second := "repos/fixture/project/actions/artifacts/101/zip"
	commands.replies[first], commands.replies[second] = commands.replies[second], commands.replies[first]
	endpoint := "repos/fixture/project/actions/runs/123/artifacts?per_page=100"
	var artifacts struct {
		Count     int              `json:"total_count"`
		Artifacts []hostedArtifact `json:"artifacts"`
	}
	if err := json.Unmarshal(commands.replies[endpoint], &artifacts); err != nil {
		t.Fatal(err)
	}
	for index := range artifacts.Artifacts[:2] {
		data := commands.replies[fmt.Sprintf("repos/fixture/project/actions/artifacts/%d/zip", artifacts.Artifacts[index].ID)]
		artifacts.Artifacts[index].Digest = fmt.Sprintf("sha256:%x", sha256.Sum256(data))
		artifacts.Artifacts[index].Size = int64(len(data))
	}
	data, err := json.Marshal(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	commands.replies[endpoint] = data
	if receipt, err := (Hosted{Commands: commands}).Read(context.Background(), plan, 90*time.Minute); err == nil || receipt != nil {
		t.Fatalf("artifact names did not bind their manifest payloads: %+v, %v", receipt, err)
	}
}
