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
// it. Its caller names the project, the tier, its commands, its time limit
// and the cfo build that verifies, and ReadReuseKey reads the rest. A pass is
// taken again only for the very same key.
type ReuseKey struct {
	Project      string     `json:"project"`
	Scope        string     `json:"scope"`
	Commands     [][]string `json:"commands"`
	LimitSeconds int        `json:"limit_seconds"`
	// Executor is the cfo build that verifies, not a cfo a tier's command
	// starts from its PATH, which a record names among its tools.
	Executor string `json:"executor"`
	// Source is the complete source of the worktree, and Base where its
	// branch left the default branch, since a check can choose what to run
	// by what the branch changed.
	Source Source `json:"source"`
	Base   string `json:"base"`
	// Inputs is a digest of the ignored files the project declares its
	// checks read, Tools the output of each tool command it declares, and
	// Environment each variable it declares with a digest of its value, so
	// that no secret is kept.
	Inputs      string   `json:"inputs"`
	Tools       []string `json:"tools"`
	Environment []string `json:"environment"`
	// Platform is the operating system and architecture the checks ran on.
	Platform string `json:"platform"`
}

// ID names the key in the store.
func (key ReuseKey) ID() string {
	// A key holds only strings and numbers, which always marshal.
	data, _ := json.Marshal(key)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ReadReuseKey completes key with what the worktree dir holds now, as reuse
// declares it, within sourceReadLimit. A key that cannot all be read is no
// key: an unstamped cfo build, an incomplete source, no default branch to
// measure the branch against, an input that is a link, or a tool command
// that fails.
func ReadReuseKey(ctx context.Context, commands execx.Runner, dir string, key ReuseKey, reuse project.Reuse) (ReuseKey, error) {
	if key.Executor == "" || key.Executor == "dev" {
		return ReuseKey{}, errors.New("verify: an unstamped cfo build cannot say what ran a pass")
	}
	ctx, cancel := context.WithTimeout(ctx, sourceReadLimit)
	defer cancel()
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
	if key.Inputs, err = inputsDigest(ctx, dir, reuse.Inputs); err != nil {
		return ReuseKey{}, err
	}
	key.Tools = nil
	for _, tool := range reuse.Tools {
		result, err := commands.Run(ctx, execx.Request{Dir: dir, Name: tool[0], Args: tool[1:], KillTree: true})
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
		sum := sha256.Sum256([]byte(os.Getenv(name)))
		key.Environment = append(key.Environment, name+"=sha256:"+hex.EncodeToString(sum[:]))
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
// A link, a junction or any other entry that is neither a file nor a folder
// is refused, since what it points at could change with no change here.
func inputsDigest(ctx context.Context, dir string, inputs []string) (string, error) {
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
			if err := ctx.Err(); err != nil {
				return err
			}
			name, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			switch {
			case entry.IsDir():
			case entry.Type().IsRegular():
				sum, err := fileDigest(path)
				if err != nil {
					return err
				}
				fmt.Fprintf(digest, "%s\x00file\x00%s\n", filepath.ToSlash(name), sum)
			default:
				return fmt.Errorf("%s is a link or another kind of entry, whose target could change unseen", filepath.ToSlash(name))
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
	file, err := fsx.Open(path)
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

// passesDir is the folder passes are kept in.
func passesDir() (string, error) {
	store, err := StoreDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(store, "passes"), nil
}

// wholePass says why results are not a whole pass of key, or nothing: each
// of the key's commands passed, in order, with a record of its execution in
// the executions store, and tested the key's source.
func wholePass(key ReuseKey, results []Result) error {
	if len(key.Commands) == 0 {
		return errors.New("verify: a tier with no command is no pass")
	}
	if len(results) != len(key.Commands) {
		return fmt.Errorf("verify: %d result(s) of %d command(s) is no whole pass", len(results), len(key.Commands))
	}
	store, err := StoreDir()
	if err != nil {
		return err
	}
	executions := filepath.Join(store, "executions")
	for index, result := range results {
		command := strings.Join(result.Command, " ")
		tested, err := result.Source.Tested()
		switch {
		case !slices.Equal(result.Command, key.Commands[index]):
			return fmt.Errorf("verify: %s is not the tier's command %s", command, strings.Join(key.Commands[index], " "))
		case result.Status != "passed":
			return fmt.Errorf("verify: %s did not pass", command)
		case result.Execution == "" || !fsx.SamePath(filepath.Dir(result.Execution), executions):
			return fmt.Errorf("verify: %s kept no record of its execution in the store", command)
		case err != nil:
			return err
		case tested != key.Source:
			return fmt.Errorf("verify: %s tested tree %s, not the key's %s", command, tested.Tree, key.Source.Tree)
		}
	}
	return nil
}

// KeepPass keeps a tier's results as the pass of key, if they are a whole
// pass on the key's own source (wholePass).
func KeepPass(key ReuseKey, results []Result, now time.Time) error {
	if err := wholePass(key, results); err != nil {
		return err
	}
	folder, err := passesDir()
	if err != nil {
		return err
	}
	return Save(filepath.Join(folder, key.ID()+".json"), Pass{Key: key, Results: results, Kept: now.UTC()})
}

// PrunePasses removes the passes written more than a week before now.
func PrunePasses(now time.Time) error {
	folder, err := passesDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(folder)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
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

// FindPass is the pass kept for key, no older than maxAge, that is a whole
// pass of it (wholePass) whose evidence stands: each command's execution
// record is there, passed, and tested the key's source. Each result is
// marked as taken again at now for the task reusing it, and keeps the
// original run's own fields. Otherwise the error is ErrNoPass, with why.
func FindPass(key ReuseKey, now time.Time, maxAge time.Duration, task string) (Pass, error) {
	folder, err := passesDir()
	if err != nil {
		return Pass{}, err
	}
	path := filepath.Join(folder, key.ID()+".json")
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
	if err := wholePass(key, pass.Results); err != nil {
		return Pass{}, fmt.Errorf("%w: the pass at %s is not whole: %v", ErrNoPass, path, err)
	}
	for index, result := range pass.Results {
		record, err := LoadExecution(result.Execution)
		if err != nil {
			return Pass{}, fmt.Errorf("%w: the record of %s is gone: %v", ErrNoPass, strings.Join(result.Command, " "), err)
		}
		if tested, err := record.Source.Tested(); record.Status != "passed" || err != nil || tested != key.Source {
			return Pass{}, fmt.Errorf("%w: the record of %s no longer shows a pass on this source", ErrNoPass, strings.Join(result.Command, " "))
		}
		pass.Results[index].Reused, pass.Results[index].ReusedAt, pass.Results[index].ReusedFor = path, now.UTC(), task
	}
	return pass, nil
}
