package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/project"
)

// ErrNoPass says that no pass can be taken again for a key.
var ErrNoPass = errors.New("verify: no pass to reuse")

// keptPassAge is the longest a pass is kept, whatever age a project takes.
const keptPassAge = 7 * 24 * time.Hour

// ReuseKey is what a tier's pass depends on, as far as this machine can read
// it. Its caller names the project, the tier, its commands and the cfo build
// that runs them, and ReadReuseKey reads the rest. A pass is taken again only
// for the very same key.
type ReuseKey struct {
	Project  string     `json:"project"`
	Scope    string     `json:"scope"`
	Commands [][]string `json:"commands"`
	Executor string     `json:"executor"`
	// Source is the complete source of the worktree, and Base where its
	// branch left the default branch, since a check can choose what to run
	// by what the branch changed.
	Source Source `json:"source"`
	Base   string `json:"base"`
	// Inputs is a digest of the ignored files the project declares its
	// checks read, Tools the output of each tool command it declares, and
	// Environment each variable it declares with its value.
	Inputs      string   `json:"inputs"`
	Tools       []string `json:"tools"`
	Environment []string `json:"environment"`
	// Platform is the operating system and architecture the checks ran on.
	Platform string `json:"platform"`
}

// ID names the key in the store.
func (key ReuseKey) ID() string {
	// A key holds only strings, which always marshal.
	data, _ := json.Marshal(key)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ReadReuseKey completes key with what the worktree dir holds now, as reuse
// declares it. A key that cannot all be read is no key: an unstamped cfo
// build, an incomplete source, no default branch to measure the branch
// against, or a tool command that fails.
func ReadReuseKey(ctx context.Context, commands execx.Runner, dir string, key ReuseKey, reuse project.Reuse) (ReuseKey, error) {
	if key.Executor == "" || key.Executor == "dev" {
		return ReuseKey{}, errors.New("verify: an unstamped cfo build cannot say what ran a pass")
	}
	source, err := ReadSource(ctx, commands, dir)
	if err != nil {
		return ReuseKey{}, err
	}
	if !source.IsComplete {
		return ReuseKey{}, errors.New("verify: the tree does not hold all of what is on disk")
	}
	key.Source = source
	if key.Base, err = defaultBranchBase(ctx, commands, dir); err != nil {
		return ReuseKey{}, err
	}
	if key.Inputs, err = inputsDigest(dir, reuse.Inputs); err != nil {
		return ReuseKey{}, err
	}
	key.Tools = nil
	for _, tool := range reuse.Tools {
		result, err := commands.Run(ctx, execx.Request{Dir: dir, Name: tool[0], Args: tool[1:]})
		if err == nil && result.ExitCode != 0 {
			err = fmt.Errorf("exited %d", result.ExitCode)
		}
		if err != nil {
			return ReuseKey{}, fmt.Errorf("verify: read the tool %s: %w", strings.Join(tool, " "), err)
		}
		key.Tools = append(key.Tools, strings.TrimSpace(string(result.Stdout)+string(result.Stderr)))
	}
	key.Environment = nil
	for _, name := range reuse.Environment {
		key.Environment = append(key.Environment, name+"="+os.Getenv(name))
	}
	key.Platform = runtime.GOOS + "/" + runtime.GOARCH
	return key, nil
}

// defaultBranchBase is where the branch checked out in dir left the default
// branch, as origin/HEAD or origin/main names it.
func defaultBranchBase(ctx context.Context, commands execx.Runner, dir string) (string, error) {
	for _, ref := range []string{"origin/HEAD", "origin/main"} {
		result, err := commands.Run(ctx, execx.Request{Dir: dir, Env: repositoryFreeEnvironment(), Name: "git", Args: []string{"merge-base", "HEAD", ref}})
		if err == nil && result.ExitCode == 0 {
			return strings.TrimSpace(string(result.Stdout)), nil
		}
	}
	return "", errors.New("verify: no default branch, as origin/HEAD or origin/main, to measure the branch against")
}

// inputsDigest is a digest of each of inputs, paths under dir: a file's
// content, a folder's files with their paths, and a missing one as missing.
func inputsDigest(dir string, inputs []string) (string, error) {
	digest := sha256.New()
	for _, input := range inputs {
		root := filepath.Join(dir, filepath.FromSlash(input))
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) && path == root {
				fmt.Fprintf(digest, "%s\x00missing\n", input)
				return nil
			}
			if err != nil {
				return err
			}
			name, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			switch {
			case entry.IsDir():
				return nil
			case entry.Type().IsRegular():
				sum, err := fileDigest(path)
				if err != nil {
					return err
				}
				fmt.Fprintf(digest, "%s\x00file\x00%s\n", filepath.ToSlash(name), sum)
			default:
				fmt.Fprintf(digest, "%s\x00other\x00%s\n", filepath.ToSlash(name), entry.Type())
			}
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("verify: read the input %s: %w", input, err)
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// Pass is a tier's pass kept to be taken again: its key, its commands'
// results, and when it was kept.
type Pass struct {
	Key     ReuseKey  `json:"key"`
	Results []Result  `json:"results"`
	Kept    time.Time `json:"kept"`
}

// passPath is where the pass of a key is kept.
func passPath(key ReuseKey) (string, error) {
	store, err := StoreDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(store, "passes", key.ID()+".json"), nil
}

// KeepPass keeps a tier's results as the pass of key, and returns where. It
// keeps only a whole pass on the key's own source: each of the key's
// commands passed in its turn, with a record of its execution, and tested
// that source. It removes the passes older than a week.
func KeepPass(key ReuseKey, results []Result, now time.Time) (string, error) {
	if len(results) != len(key.Commands) {
		return "", fmt.Errorf("verify: %d result(s) of %d command(s) is no whole pass", len(results), len(key.Commands))
	}
	kept := make([]Result, 0, len(results))
	for index, result := range results {
		tested, err := result.Source.Tested()
		switch {
		case !slices.Equal(result.Command, key.Commands[index]):
			return "", fmt.Errorf("verify: %s is not the tier's command %s", strings.Join(result.Command, " "), strings.Join(key.Commands[index], " "))
		case result.Status != "passed":
			return "", fmt.Errorf("verify: %s did not pass", strings.Join(result.Command, " "))
		case result.Execution == "":
			return "", fmt.Errorf("verify: %s kept no record of its execution", strings.Join(result.Command, " "))
		case err != nil:
			return "", err
		case tested != key.Source:
			return "", fmt.Errorf("verify: %s tested tree %s, not the key's %s", strings.Join(result.Command, " "), tested.Tree, key.Source.Tree)
		}
		result.Reused = ""
		kept = append(kept, result)
	}
	path, err := passPath(key)
	if err != nil {
		return "", err
	}
	if err := Save(path, Pass{Key: key, Results: kept, Kept: now.UTC()}); err != nil {
		return "", err
	}
	return path, prunePasses(filepath.Dir(path), now)
}

// prunePasses removes the passes in folder written more than a week before
// now.
func prunePasses(folder string, now time.Time) error {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") || now.Sub(info.ModTime()) <= keptPassAge {
			continue
		}
		failures = append(failures, fsx.Remove(filepath.Join(folder, entry.Name())))
	}
	return errors.Join(failures...)
}

// FindPass is the pass kept for key, no older than maxAge, whose evidence
// stands: each of its commands' execution records is there, passed, and
// tested the key's source. Each result names the pass it is taken from.
// Otherwise the error is ErrNoPass, with why.
func FindPass(key ReuseKey, now time.Time, maxAge time.Duration) (Pass, error) {
	path, err := passPath(key)
	if err != nil {
		return Pass{}, err
	}
	var pass Pass
	data, err := fsx.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Pass{}, fmt.Errorf("%w: none is kept for this key", ErrNoPass)
	}
	if err != nil {
		return Pass{}, err
	}
	if err := json.Unmarshal(data, &pass); err != nil || !reflect.DeepEqual(pass.Key, key) {
		return Pass{}, fmt.Errorf("%w: the pass kept at %s is not this key's", ErrNoPass, path)
	}
	if age := now.Sub(pass.Kept); age < -time.Minute || age > maxAge {
		return Pass{}, fmt.Errorf("%w: the pass at %s was kept %s ago, past %s", ErrNoPass, path, age.Round(time.Minute), maxAge)
	}
	for index, result := range pass.Results {
		record, err := LoadExecution(result.Execution)
		if err != nil {
			return Pass{}, fmt.Errorf("%w: the record of %s is gone: %v", ErrNoPass, strings.Join(result.Command, " "), err)
		}
		if tested, err := record.Source.Tested(); record.Status != "passed" || err != nil || tested != key.Source {
			return Pass{}, fmt.Errorf("%w: the record of %s no longer shows a pass on this source", ErrNoPass, strings.Join(result.Command, " "))
		}
		pass.Results[index].Reused = path
	}
	return pass, nil
}
