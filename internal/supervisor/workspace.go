package supervisor

import (
	"context"
	"errors"
	"path/filepath"

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
	return out, nil
}
