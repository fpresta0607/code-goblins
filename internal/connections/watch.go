package connections

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// fileSum is a file's content fingerprint as of the size and time it had, so a
// file that has not changed is not read again.
type fileSum struct {
	size    int64
	modTime time.Time
	sum     string
}

// Watch keeps the snapshots current until ctx ends, each tick doing only what
// is cheap: it reads again the goblins whose task record changed or whose
// session record grew, and queues a recheck of the projects whose manifest or
// .mcp.json changed or whose check is old, and of the machine when its check
// is old or was never made. tasks lists the goblins that run.
func (s *Snapshots) Watch(ctx context.Context, every time.Duration, tasks func() []state.TaskMeta) {
	s.mu.Lock()
	s.tasks = tasks
	s.mu.Unlock()
	s.tick(tasks())
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.tick(tasks())
		}
	}
}

func (s *Snapshots) tick(running []state.TaskMeta) {
	now := s.Now()
	ids := map[string]bool{}
	for _, meta := range running {
		ids[meta.ID] = true
	}
	s.mu.Lock()
	var gone []string
	for task := range s.goblins {
		if !ids[task] {
			gone = append(gone, task)
		}
	}
	s.mu.Unlock()
	for _, task := range gone {
		s.dropGoblin(task)
	}
	for _, meta := range running {
		s.readGoblin(meta, false)
	}
	s.listProjects(running)
	type listed struct {
		name, path, basis string
		checkedAt         time.Time
	}
	s.mu.Lock()
	machineChecked := s.machine.CheckedAt
	var projects []listed
	for name, project := range s.projects {
		projects = append(projects, listed{name: name, path: project.Path, basis: project.Basis, checkedAt: project.CheckedAt})
	}
	s.mu.Unlock()
	var due []job
	if machineChecked.IsZero() || now.Sub(machineChecked) >= machineLife {
		due = append(due, job{RefreshMachine, ""})
	}
	for _, project := range projects {
		if project.checkedAt.IsZero() || now.Sub(project.checkedAt) >= projectLife || project.basis != s.projectBasis(project.name, project.path) {
			due = append(due, job{RefreshProject, project.name})
		}
	}
	slices.SortFunc(due, func(a, b job) int { return strings.Compare(a.level+"\x00"+a.scope, b.level+"\x00"+b.scope) })
	for _, next := range due {
		s.enqueue(next)
	}
}

// listProjects lists every project with a manifest and every project a running
// goblin works in, each with its checkout: a running goblin's own, else its
// name under the projects root. A project no longer listed is let go.
func (s *Snapshots) listProjects(running []state.TaskMeta) {
	paths := map[string]string{}
	for _, meta := range running {
		if name := projectName(s.Env.DataDir, meta.Project); name != "" && meta.Project != "" && paths[name] == "" {
			paths[name] = meta.Project
		}
	}
	names := manifestProjects(s.Env.DataDir)
	for name := range paths {
		names = append(names, name)
	}
	slices.Sort(names)
	listed := map[string]bool{}
	s.mu.Lock()
	for _, name := range slices.Compact(names) {
		if !validFileName(name) {
			continue
		}
		listed[name] = true
		project := s.projects[name]
		project.Name, project.Path = name, s.checkout(name, paths[name])
		s.projects[name] = project
	}
	var gone []string
	for name := range s.projects {
		if !listed[name] {
			gone = append(gone, name)
			delete(s.projects, name)
		}
	}
	s.mu.Unlock()
	for _, name := range gone {
		s.remove(projectFile(name))
	}
}

// checkout is a project's folder: the one a goblin works from, else its name
// under the projects root, else none.
func (s *Snapshots) checkout(name, running string) string {
	if running != "" {
		return running
	}
	if s.Env.ProjectsRoot != nil {
		if root := s.Env.ProjectsRoot(); root != "" {
			return filepath.Join(root, name)
		}
	}
	return ""
}

// hasProject says whether a project is listed, listing it when its manifest
// exists and nothing has listed it yet.
func (s *Snapshots) hasProject(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, isListed := s.projects[name]; isListed {
		return true
	}
	if !validFileName(name) || !slices.Contains(manifestProjects(s.Env.DataDir), name) {
		return false
	}
	s.projects[name] = Project{Name: name, Path: s.checkout(name, "")}
	return true
}

// readGoblin reads one goblin's records again when its task record changed or
// its session record grew, or whenever force says so, carrying forward what the
// lines already read said.
func (s *Snapshots) readGoblin(meta state.TaskMeta, force bool) {
	s.reading.Lock()
	defer s.reading.Unlock()
	transcript := s.Transcript(meta)
	record := recordFingerprint(meta)
	s.mu.Lock()
	kept, isKept := s.goblins[meta.ID]
	s.mu.Unlock()
	continues := isKept && !kept.CheckedAt.IsZero() && kept.Transcript == transcript && kept.Generation == meta.SpawnGen && kept.Harness == meta.Harness
	if !force && continues && kept.Basis == record && sizeOf(transcript) == kept.Offset {
		return
	}
	prior, from := Goblin{}, int64(0)
	if continues {
		prior, from = kept.Goblin, kept.Offset
	}
	goblin, offset := s.ReadGoblin(meta, transcript, from, prior)
	now := s.Now().UTC()
	goblin.CheckedAt, goblin.Items, goblin.Basis = now, stamped(goblin.Items, now), record
	next := goblinFile{Goblin: goblin, Offset: offset, Transcript: transcript}
	s.mu.Lock()
	next.Checking = s.isQueued(job{RefreshGoblin, meta.ID})
	s.goblins[meta.ID] = next
	s.mu.Unlock()
	next.Checking = false
	s.save(goblinPath(meta.ID), next)
}

// dropGoblin lets go of a goblin that no longer runs.
func (s *Snapshots) dropGoblin(task string) {
	s.mu.Lock()
	_, isKept := s.goblins[task]
	delete(s.goblins, task)
	s.mu.Unlock()
	if isKept {
		s.remove(goblinPath(task))
	}
}

// sessionTranscript is the Claude session record of a goblin: the session its
// own running harness proved for this generation, found in the folder Claude
// keeps its conversations. A goblin of another harness has none.
func (s *Snapshots) sessionTranscript(meta state.TaskMeta) string {
	if meta.Harness != "claude" {
		return ""
	}
	session, err := fleettree.KeptSession(s.Env.StateDir, meta)
	if err != nil || session == "" {
		return ""
	}
	return fleettree.HeldTranscript(s.Env.UserHome, meta.Harness, meta.Worktree, session)
}

// sizeOf is the length of the file at path, 0 for none.
func sizeOf(path string) int64 {
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// recordFingerprint names a task's record as a goblin's read of it saw it, so a
// grant or revoke made with goblins auth reads the goblin again.
func recordFingerprint(meta state.TaskMeta) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%#v", meta)))
	return hex.EncodeToString(sum[:])
}

// projectBasis names what a project's check depends on: its checkout and the
// content of its manifest and .mcp.json.
func (s *Snapshots) projectBasis(name, path string) string {
	parts := []string{path, s.fingerprint(auth.ManifestPath(s.Env.DataDir, name)), s.fingerprint(filepath.Join(path, ".mcp.json"))}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// fingerprint is a file's size, time and content hash, "absent" for none. A
// file whose size and time have not changed is not read again.
func (s *Snapshots) fingerprint(path string) string {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "absent"
	}
	s.mu.Lock()
	known, isKnown := s.sums[path]
	s.mu.Unlock()
	if !isKnown || known.size != info.Size() || !known.modTime.Equal(info.ModTime()) {
		data, err := fsx.ReadFile(path)
		if err != nil {
			return "unreadable"
		}
		sum := sha256.Sum256(data)
		known = fileSum{size: info.Size(), modTime: info.ModTime(), sum: hex.EncodeToString(sum[:])}
		s.mu.Lock()
		s.sums[path] = known
		s.mu.Unlock()
	}
	return strconv.FormatInt(known.size, 10) + "-" + known.sum
}
