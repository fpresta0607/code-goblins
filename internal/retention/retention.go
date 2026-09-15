// Package retention removes only expired browser data owned by a completed,
// merged and inactive task. Context, decisions, policy, tests and recaps stay.
package retention

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/taskcontext"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

type Entry struct {
	TaskID   string `json:"task_id"`
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	KeepDays int    `json:"keep_days"`
	Hold     string `json:"hold,omitempty"`
	Removed  bool   `json:"removed"`
}

type Service struct {
	Home      home.Home
	Commands  execx.Runner
	Prober    monitor.Prober
	Processes reap.ProcessLister
	Now       func() time.Time
}

func (s Service) Run(ctx context.Context, id string, apply bool) ([]Entry, error) {
	if err := state.ValidTaskID(id); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Spawn and switch cannot launch an owner while its data is being retired.
	for _, name := range []string{".spawn.lock", state.MetadataLockName(id), state.CleanupLockName(id), state.PipelineLockName(id)} {
		if _, err := lock.AcquireExclusiveNamed(s.Home.State, name); err != nil {
			return nil, err
		}
		defer lock.ReleaseExclusiveNamed(s.Home.State, name)
	}
	paths := taskcontext.PathsFor(s.Home, id)
	entries := []Entry{{TaskID: id, Path: paths.Browser.Profile, KeepDays: 30}, {TaskID: id, Path: paths.Browser.Evidence, KeepDays: 90}}
	hold := s.eligible(ctx, id, paths)
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now()
	}
	for i := range entries {
		e := &entries[i]
		e.Hold = hold
		info, err := os.Lstat(e.Path)
		if errors.Is(err, os.ErrNotExist) {
			e.Hold = "absent"
			continue
		}
		if err != nil {
			e.Hold = err.Error()
			continue
		}
		size, newest, err := inspectOwnedTree(s.Home.State, e.Path)
		e.Bytes = size
		if err != nil {
			e.Hold = err.Error()
			continue
		}
		if e.Hold != "" {
			continue
		}
		if retirement, err := taskcontext.ReadRetirement(s.Home, id); err == nil && retirement.RetiredAt.After(newest) {
			newest = retirement.RetiredAt
		}
		if now.Before(newest.Add(time.Duration(e.KeepDays) * 24 * time.Hour)) {
			e.Hold = "retention period has not elapsed"
			continue
		}
		if !apply {
			continue
		}
		again, err := os.Lstat(e.Path)
		if err != nil || !os.SameFile(info, again) {
			e.Hold = "directory identity changed during audit"
			continue
		}
		// No arbitrary paths, force flag, worktree removal or personal-profile scan.
		if err := os.RemoveAll(e.Path); err != nil {
			e.Hold = err.Error()
			continue
		}
		e.Removed = true
	}
	return entries, nil
}

func (s Service) eligible(ctx context.Context, id string, paths taskcontext.Paths) string {
	data, err := os.ReadFile(paths.Manifest)
	if err != nil {
		return "task context ownership unavailable"
	}
	var m taskcontext.Manifest
	if json.Unmarshal(data, &m) != nil || m.Schema != "cfo-task-context.v1" || m.ID != id || m.Paths != paths {
		return "task context ownership mismatch"
	}
	meta, err := state.ReadTaskMeta(s.Home.State, id)
	retired := false
	if errors.Is(err, os.ErrNotExist) {
		receipt, e := taskcontext.ReadRetirement(s.Home, id)
		if e != nil || receipt.Meta.Project != m.Project || receipt.Meta.Worktree != m.Worktree {
			return "retirement ownership or merge proof unavailable"
		}
		meta = receipt.Meta
		retired = true
	} else if err != nil || meta.ID != id || !fsx.SamePath(meta.Project, m.Project) || !fsx.SamePath(meta.Worktree, m.Worktree) {
		return "task metadata or worktree ownership unavailable; preserve retained context"
	}
	root, err := fsx.Canonical(filepath.Dir(paths.Manifest))
	if err != nil {
		return "task storage identity unavailable"
	}
	for _, source := range []string{meta.Project, meta.Worktree} {
		if within(source, root) {
			return "runtime storage is inside source; explicitly migrate it before retention"
		}
	}
	lines, err := fsx.ReadLines(filepath.Join(s.Home.State, id+".status"))
	if err != nil && !(retired && errors.Is(err, os.ErrNotExist)) {
		return "task outcome history unavailable"
	}
	if len(crewstate.FoldOpenDecisions(lines)) != 0 || (retired && len(m.Decisions) > 0) {
		return "unresolved task decisions"
	}
	verb, _ := crewstate.LatestVerb(lines)
	if verb != "done" && !(retired && len(lines) == 0) {
		return "task is not finished"
	}
	if s.Commands == nil || s.Prober == nil || s.Processes == nil {
		return "inactivity probes unavailable"
	}
	if !retired {
		status, err := s.Commands.Run(ctx, execx.Request{Dir: meta.Worktree, Name: "git", Args: []string{"status", "--porcelain=v1", "--untracked-files=all"}})
		if err != nil || status.ExitCode != 0 || len(status.Stdout) != 0 {
			return "worktree is dirty or unreadable"
		}
		if meta.Mode == "local-only" {
			_, err = worktree.ProveLocalMerged(ctx, s.Commands, meta.Project, meta.Worktree)
		} else {
			err = worktree.RequireMerged(ctx, s.Commands, meta.Worktree)
		}
		if err != nil {
			return err.Error()
		}
	}
	sample, err := s.Prober.Inspect(ctx, meta)
	if err != nil || sample.Verdict == monitor.ProbeUnknown || (sample.Verdict != monitor.ProbeMissing && sample.Agent != herdr.AgentDead) {
		return "worker is attached or liveness is unknown"
	}
	if meta.Mode == "no-mistakes" && !retired {
		launch, err := pipeline.LoadLaunch(filepath.Join(filepath.Dir(paths.Manifest), "pipeline-launch.json"))
		if err != nil {
			return "native gate association unavailable"
		}
		nativeRoot, err := pipeline.DefaultRoot()
		if err != nil {
			return "native gate home unavailable"
		}
		run, err := (pipeline.Reader{Root: nativeRoot, Commands: s.Commands}).BoundRun(ctx, launch)
		if err != nil || run.Status != "completed" {
			return "native gate is unresolved or unreadable"
		}
	}
	processes, err := s.Processes.List(ctx)
	if err != nil {
		return "process inventory unavailable"
	}
	for _, process := range processes {
		command := strings.ToLower(strings.ReplaceAll(process.CommandLine, `\`, "/"))
		if (strings.EqualFold(process.Name, "chrome.exe") && command == "") || strings.Contains(command, strings.ToLower(filepath.ToSlash(paths.Browser.Profile))) || strings.Contains(command, strings.ToLower(paths.Browser.Session)) {
			return "browser process or session is active or unreadable"
		}
	}
	return ""
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func inspectOwnedTree(stateDir, path string) (int64, time.Time, error) {
	if !within(filepath.Join(stateDir, "tasks"), path) {
		return 0, time.Time{}, errors.New("path is outside task-owned storage")
	}
	// Refuse links at every ancestor below the selected runtime root, including
	// Windows junctions, which need Readlink rather than only ModeSymlink.
	for parent := path; !strings.EqualFold(filepath.Clean(parent), filepath.Clean(stateDir)); parent = filepath.Dir(parent) {
		if _, err := os.Readlink(parent); err == nil {
			return 0, time.Time{}, fmt.Errorf("linked task storage is retained: %s", parent)
		}
	}
	var size int64
	var newest time.Time
	err := filepath.WalkDir(path, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if _, err := os.Readlink(current); err == nil {
			return errors.New("linked browser data is retained")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("nonregular browser data is retained")
		}
		if !info.IsDir() {
			size += info.Size()
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return size, newest, err
}
