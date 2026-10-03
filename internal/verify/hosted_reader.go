package verify

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatetest"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// Hosted reads independently successful GitHub jobs and their immutable proof.
// Unavailable or mismatched evidence is an error, leaving the local baseline.
type Hosted struct{ Commands execx.Runner }

func (hosted Hosted) Read(ctx context.Context, plan gatetest.Plan, budget time.Duration) (*HostedReceipt, error) {
	if plan.Level == gatetest.Fast || len(plan.Tests) == 0 || plan.Uncommitted != 0 {
		return nil, errors.New("hosted evidence: this plan is ineligible for reuse")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if hosted.Commands == nil {
		hosted.Commands = execx.OSRunner{}
	}
	if err := hosted.unchanged(ctx, plan); err != nil {
		return nil, err
	}
	repository, err := (tickets.GitHub{Commands: hosted.Commands}).RepositoryOf(ctx, plan.Root)
	if err != nil {
		return nil, errors.New("hosted evidence: a supported GitHub origin could not be established")
	}
	var metadata struct {
		Name    string `json:"full_name"`
		Default string `json:"default_branch"`
	}
	if err := hosted.api(ctx, plan.Root, "repos/"+repository, func(data []byte) error { return json.Unmarshal(data, &metadata) }); err != nil {
		return nil, err
	}
	if metadata.Name != repository || metadata.Default == "" {
		return nil, errors.New("hosted evidence: repository identity is unreadable")
	}
	var reference struct {
		Object hostedParent `json:"object"`
	}
	mainEndpoint := "repos/" + repository + "/git/ref/heads/" + url.PathEscape(metadata.Default)
	if err := hosted.api(ctx, plan.Root, mainEndpoint, func(data []byte) error { return json.Unmarshal(data, &reference) }); err != nil {
		return nil, err
	}
	main := reference.Object.SHA
	if !hostedHash(main, 40) {
		return nil, errors.New("hosted evidence: current main identity is unreadable")
	}
	tree, err := hosted.output(ctx, plan.Root, "git", "merge-tree", "--write-tree", "--no-messages", main, plan.Commit)
	if err != nil || !hostedHash(strings.TrimSpace(string(tree)), 40) {
		return nil, errors.New("hosted evidence: the current combined merge tree cannot be established")
	}
	want := hostedIdentity{Repository: repository, Module: plan.Module, Head: plan.Commit, Main: main, Tree: strings.TrimSpace(string(tree)), Toolchain: plan.Toolchain, Inputs: map[string]string{}, Required: plan.Tests, Budget: budget}
	for _, name := range hostedInputPaths {
		hash, err := hosted.output(ctx, plan.Root, "git", "rev-parse", want.Tree+":"+name)
		if err != nil {
			return nil, fmt.Errorf("hosted evidence: input %s is unreadable", name)
		}
		want.Inputs[name] = strings.TrimSpace(string(hash))
	}
	// Changes to the verifier cannot certify themselves, and a stale planning
	// policy cannot be substituted with a newer main's policy.
	for _, name := range hostedInputPaths[2:] {
		hash, err := hosted.output(ctx, plan.Root, "git", "rev-parse", main+":"+name)
		if err != nil || strings.TrimSpace(string(hash)) != want.Inputs[name] {
			return nil, fmt.Errorf("hosted evidence: %s differs from trusted main", name)
		}
	}
	for _, name := range hostedInputPaths[2:4] {
		hash, err := hosted.output(ctx, plan.Root, "git", "rev-parse", plan.Base+":"+name)
		if err != nil || strings.TrimSpace(string(hash)) != want.Inputs[name] {
			return nil, fmt.Errorf("hosted evidence: the planned policy %s differs from tested main", name)
		}
	}
	environment, err := hosted.output(ctx, plan.Root, "go", append([]string{"env", "-json"}, hostedEnvironmentNames...)...)
	if err != nil {
		return nil, err
	}
	want.Environment, err = hostedEnvironment(environment, plan.Toolchain)
	if err != nil {
		return nil, err
	}
	packages, err := hosted.output(ctx, plan.Root, "go", "list", "./...")
	if err != nil {
		return nil, err
	}
	want.Packages = strings.Fields(string(packages))
	slices.Sort(want.Packages)
	var runs struct {
		Runs []hostedRun `json:"workflow_runs"`
	}
	if err := hosted.api(ctx, plan.Root, "repos/"+repository+"/actions/workflows/go.yml/runs?event=pull_request&status=completed&head_sha="+plan.Commit+"&per_page=3", func(data []byte) error { return json.Unmarshal(data, &runs) }); err != nil {
		return nil, err
	}
	var last error = errors.New("hosted evidence: no successful workflow on this head")
	for _, run := range runs.Runs {
		if run.Status != "completed" || run.Conclusion != "success" || run.Head != want.Head || run.ID <= 0 {
			continue
		}
		bundle, err := hosted.bundle(ctx, plan.Root, repository, run)
		if err != nil {
			last = err
			continue
		}
		receipt, err := validateHosted(want, bundle)
		if err != nil {
			last = err
			continue
		}
		if err := hosted.unchanged(ctx, plan); err != nil {
			return nil, err
		}
		var fresh struct {
			Object hostedParent `json:"object"`
		}
		if err := hosted.api(ctx, plan.Root, mainEndpoint, func(data []byte) error { return json.Unmarshal(data, &fresh) }); err != nil {
			return nil, err
		}
		if fresh.Object.SHA != main {
			return nil, errors.New("hosted evidence: main changed while reading proof")
		}
		return receipt, nil
	}
	return nil, last
}

func (hosted Hosted) unchanged(ctx context.Context, plan gatetest.Plan) error {
	head, err := hosted.output(ctx, plan.Root, "git", "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(string(head)) != plan.Commit {
		return errors.New("hosted evidence: the planned head changed")
	}
	status, err := hosted.output(ctx, plan.Root, "git", "status", "--porcelain", "--untracked-files=all")
	if err != nil || len(bytes.TrimSpace(status)) != 0 {
		return errors.New("hosted evidence: the tested worktree is not clean")
	}
	return nil
}

func (hosted Hosted) output(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	environment := gatetest.Environment(os.Environ())
	environment = slices.DeleteFunc(environment, func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		return strings.EqualFold(key, "CODEX_THREAD_ID")
	})
	result, err := hosted.Commands.Run(ctx, execx.Request{Dir: dir, Name: name, Args: args, Env: environment, KillTree: true, OutputLimit: 4 * hostedManifestLimit})
	if err != nil || result.ExitCode != 0 {
		return nil, fmt.Errorf("hosted evidence: %s could not read proof (exit %d)", name, result.ExitCode)
	}
	if len(result.Stdout) > 4*hostedManifestLimit {
		return nil, errors.New("hosted evidence: output exceeds its read limit")
	}
	return result.Stdout, nil
}

func (hosted Hosted) api(ctx context.Context, dir, endpoint string, decode func([]byte) error) error {
	data, err := hosted.output(ctx, dir, "gh", "api", "--method", "GET", "--hostname", "github.com", "-H", "Accept: application/vnd.github+json", "-H", "X-GitHub-Api-Version: 2022-11-28", endpoint)
	if err != nil {
		return err
	}
	if len(data) > hostedManifestLimit {
		return errors.New("hosted evidence: API metadata exceeds its read limit")
	}
	if err := decode(data); err != nil {
		return errors.New("hosted evidence: API metadata is unreadable")
	}
	return nil
}

func (hosted Hosted) bundle(ctx context.Context, dir, repository string, run hostedRun) (hostedBundle, error) {
	bundle := hostedBundle{Run: run}
	prefix := "repos/" + repository + "/actions/runs/" + strconv.FormatInt(run.ID, 10)
	var jobs struct {
		Count int         `json:"total_count"`
		Jobs  []hostedJob `json:"jobs"`
	}
	if err := hosted.api(ctx, dir, prefix+"/jobs?filter=latest&per_page=100", func(data []byte) error { return json.Unmarshal(data, &jobs) }); err != nil {
		return bundle, err
	}
	if jobs.Count != len(jobs.Jobs) || jobs.Count != len(hostedShards)+2 {
		return bundle, errors.New("hosted evidence: the job set is incomplete or changed")
	}
	bundle.Jobs = jobs.Jobs
	var artifacts struct {
		Count     int              `json:"total_count"`
		Artifacts []hostedArtifact `json:"artifacts"`
	}
	if err := hosted.api(ctx, dir, prefix+"/artifacts?per_page=100", func(data []byte) error { return json.Unmarshal(data, &artifacts) }); err != nil {
		return bundle, err
	}
	if artifacts.Count != len(artifacts.Artifacts) {
		return bundle, errors.New("hosted evidence: the artifact list is incomplete")
	}
	for _, shard := range hostedShards {
		var matched []hostedArtifact
		var job hostedJob
		for _, candidate := range bundle.Jobs {
			if candidate.Name == "go ("+shard.Name+")" {
				job = candidate
			}
		}
		name := fmt.Sprintf("cfo-go-evidence-%d-%s", job.Attempt, shard.Name)
		for _, artifact := range artifacts.Artifacts {
			if artifact.Name == name {
				matched = append(matched, artifact)
			}
		}
		if len(matched) != 1 || matched[0].Expired || matched[0].Size <= 0 || matched[0].Size > hostedManifestLimit {
			return bundle, errors.New("hosted evidence: a shard artifact is missing, expired, ambiguous or oversized")
		}
		artifact := matched[0]
		data, err := hosted.output(ctx, dir, "gh", "api", "--method", "GET", "--hostname", "github.com", "repos/"+repository+"/actions/artifacts/"+strconv.FormatInt(artifact.ID, 10)+"/zip")
		if err != nil {
			return bundle, err
		}
		manifest, err := decodeHostedArtifact(data, artifact)
		if err != nil {
			return bundle, err
		}
		if manifest.Shard != shard.Name || manifest.Attempt != job.Attempt {
			return bundle, errors.New("hosted evidence: the artifact payload belongs to another shard or attempt")
		}
		bundle.Artifacts = append(bundle.Artifacts, artifact)
		bundle.Manifests = append(bundle.Manifests, manifest)
	}
	checkout := bundle.Manifests[0].Checkout
	if !hostedHash(checkout, 40) {
		return bundle, errors.New("hosted evidence: checkout identity is unreadable")
	}
	if err := hosted.api(ctx, dir, "repos/"+repository+"/git/commits/"+checkout, func(data []byte) error { return json.Unmarshal(data, &bundle.Checkout) }); err != nil {
		return bundle, err
	}
	return bundle, nil
}

var hostedEnvironmentNames = []string{"CGO_ENABLED", "GOAMD64", "GOARCH", "GOARM64", "GOEXPERIMENT", "GOFLAGS", "GOOS", "GOVERSION", "GOWORK"}

func hostedEnvironment(data []byte, toolchain string) (string, error) {
	var environment map[string]string
	if err := json.Unmarshal(data, &environment); err != nil || len(environment) != len(hostedEnvironmentNames) {
		return "", errors.New("hosted evidence: the Go environment is unreadable")
	}
	if environment["CGO_ENABLED"] != "0" || environment["GOOS"] != "windows" || toolchain != environment["GOVERSION"]+" "+environment["GOOS"]+"/"+environment["GOARCH"] {
		return "", errors.New("hosted evidence: the exact toolchain or CGO compiler cannot be proved")
	}
	var fingerprint strings.Builder
	for _, name := range hostedEnvironmentNames {
		value, exists := environment[name]
		if !exists {
			return "", errors.New("hosted evidence: a Go build setting is missing")
		}
		fingerprint.WriteString(name + "\x00" + value + "\x00")
	}
	digest := sha256.Sum256([]byte(fingerprint.String()))
	return hex.EncodeToString(digest[:]), nil
}

func decodeHostedArtifact(data []byte, artifact hostedArtifact) (hostedManifest, error) {
	var manifest hostedManifest
	if len(data) > 4*hostedManifestLimit || fmt.Sprintf("sha256:%x", sha256.Sum256(data)) != artifact.Digest {
		return manifest, errors.New("hosted evidence: the artifact digest differs")
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(archive.File) != 1 || archive.File[0].Name != "manifest.json" || archive.File[0].UncompressedSize64 > hostedManifestLimit {
		return manifest, errors.New("hosted evidence: the artifact is not one bounded manifest")
	}
	file, err := archive.File[0].Open()
	if err != nil {
		return manifest, errors.New("hosted evidence: the artifact cannot be opened")
	}
	content, readErr := io.ReadAll(io.LimitReader(file, hostedManifestLimit+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(content) > hostedManifestLimit || !utf8.Valid(content) {
		return manifest, errors.New("hosted evidence: the manifest is corrupt or oversized")
	}
	duplicates := json.NewDecoder(bytes.NewReader(content))
	duplicates.UseNumber()
	if err := uniqueHostedJSON(duplicates, 0); err != nil {
		return manifest, err
	}
	if _, err := duplicates.Token(); err != io.EOF {
		return manifest, errors.New("hosted evidence: trailing JSON is ambiguous")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, errors.New("hosted evidence: the manifest schema is unreadable")
	}
	return manifest, nil
}

func uniqueHostedJSON(decoder *json.Decoder, depth int) error {
	if depth > 16 {
		return errors.New("hosted evidence: JSON nesting exceeds its limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return errors.New("hosted evidence: manifest JSON is unreadable")
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, isString := key.(string)
			if !isString || name != strings.ToLower(name) || seen[name] {
				return errors.New("hosted evidence: repeated or noncanonical JSON keys are ambiguous")
			}
			seen[name] = true
		}
		if err := uniqueHostedJSON(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
