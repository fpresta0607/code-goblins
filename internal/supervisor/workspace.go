package supervisor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/services"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type WorkspaceDetail struct {
	Project    string   `json:"project"`
	Repository string   `json:"repository"`
	Root       string   `json:"root"`
	Branch     string   `json:"branch"`
	Harness    string   `json:"harness"`
	Model      string   `json:"model"`
	Notes      []string `json:"notes"`
}

func (s *Service) workspaceDetail(ctx context.Context, taskID, generation string) (WorkspaceDetail, error) {
	out := WorkspaceDetail{}
	var meta state.TaskMeta
	if taskID == "" {
		out.Root = s.Store.Home.Root
		out.Project, out.Repository = filepath.Base(out.Root), filepath.Base(out.Root)
		file, err := openPrimary(filepath.Join(s.Store.Home.State, "primary.json"))
		if err == nil {
			p, _, e := decodePrimary(file)
			_ = file.Close()
			if e == nil {
				out.Harness = p.Agent
			}
		}
		out.Notes = s.servicesNotes("")
		return out, nil
	}
	var err error
	meta, err = state.ReadTaskMeta(s.Store.Home.State, taskID)
	if err != nil || meta.SpawnGen != generation {
		return out, errors.New("Task restarted or was replaced. Refresh workspace details.")
	}
	out.Root, out.Project, out.Repository, out.Harness, out.Model = meta.Worktree, filepath.Base(meta.Project), filepath.Base(meta.Project), meta.Harness, meta.Model
	if out.Model != "" {
		out.Model = "Configured: " + out.Model
	}
	db := s.Store.Snapshot()
	if session, ok := db.Sessions[db.TaskSessions[meta.ID]]; ok && session.Generation == meta.SpawnGen && session.Model != "" {
		out.Model = "Reported: " + session.Model
	}
	out.Branch, err = s.Git.Branch(ctx, meta.Worktree)
	if err != nil {
		out.Notes = append(out.Notes, "Branch information is unavailable.")
	}
	out.Notes = append(out.Notes, credentialsNote(meta))
	out.Notes = append(out.Notes, s.servicesNotes(meta.ID)...)
	return out, nil
}

// credentialsNote says which services' credentials a task's terminal carries,
// by name and never by value: the ones its record names, none, or, for a task
// an older build started, everything stored for its project until its next
// terminal.
func credentialsNote(meta state.TaskMeta) string {
	project := filepath.Base(meta.Project)
	switch {
	case !meta.HasCredentials:
		return "Carries every credential stored for " + project + ": a build before credentials went by need started it. Its next terminal carries only the services " + project + "'s manifest marks default."
	case len(meta.Credentials) == 0:
		return "Carries no service's credentials."
	}
	return "Carries the credentials of " + andList(meta.Credentials) + ", and of no other service."
}

// servicesNotes says which local services stacks a task holds, with whom
// and what they take, or for the CFO, every stack held and whether cfo
// started the engine under them.
func (s *Service) servicesNotes(task string) []string {
	record, err := services.ReadRecord(s.Store.Home.State)
	if err != nil {
		return []string{"The local services cfo holds could not be read."}
	}
	var notes []string
	for _, stack := range record.Sorted() {
		if !stack.IsUp() || task != "" && !stack.Holds(task) {
			continue
		}
		memory := ", whose memory is not measured yet."
		if stack.Cost.Bytes > 0 {
			memory = fmt.Sprintf(", which take %.1f GB of memory.", float64(stack.Cost.Bytes)/(1<<30))
		}
		if task == "" {
			notes = append(notes, stack.Project+"'s local services are up for "+andList(stack.HolderIDs())+memory)
			continue
		}
		note := "Holds " + stack.Project + "'s local services"
		if others := slices.DeleteFunc(stack.HolderIDs(), func(id string) bool { return id == task }); len(others) > 0 {
			note += " with " + andList(others)
		}
		note += memory
		notes = append(notes, note)
	}
	if task == "" && len(notes) > 0 && record.Engine.StartedByCFO {
		notes = append(notes, "cfo started the Docker engine for them and stops it once none of them runs.")
	}
	return notes
}

// andList joins names as a sentence names them: a, b and c.
func andList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
