package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

type WorkspaceDetail struct {
	Project     string         `json:"project"`
	Repository  string         `json:"repository"`
	Root        string         `json:"root"`
	Branch      string         `json:"branch"`
	Harness     string         `json:"harness"`
	Model       string         `json:"model"`
	MCP         []ScopedDetail `json:"mcp"`
	Environment []ScopedDetail `json:"environment"`
	Notes       []string       `json:"notes"`
}
type ScopedDetail struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Source string `json:"source"`
}

// Only declarative metadata is inspected. No process environment, dotenv,
// auth script, credential resolver, MCP commands or header values are exposed.
func (s *Service) workspaceDetail(ctx context.Context, taskID, generation string) (WorkspaceDetail, error) {
	out := WorkspaceDetail{MCP: []ScopedDetail{}, Environment: []ScopedDetail{}, Notes: []string{}}
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
		out.Notes = append(out.Notes, "CFO connection and environment configuration is not reported by this registration.")
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
	manifest, err := auth.LoadManifest(s.Store.Home.Data, meta.Project)
	if err == nil {
		for _, name := range manifest.EnvNames() {
			out.Environment = append(out.Environment, ScopedDetail{name, "Declared", "Authentication manifest"})
		}
	} else {
		out.Notes = append(out.Notes, "Authentication variable declarations could not be read.")
	}
	provision, err := worktree.Resolve(s.Store.Home.Data, meta.Project)
	if err == nil {
		for name, value := range provision.Env {
			if !auth.ValidEnvName(name) {
				continue
			}
			status := "Configured"
			if value == "" {
				status = "Empty"
			}
			out.Environment = append(out.Environment, ScopedDetail{name, status, "Worktree provisioning manifest"})
		}
	} else {
		out.Notes = append(out.Notes, "Provisioning variable declarations could not be read.")
	}
	sort.Slice(out.Environment, func(i, j int) bool { return out.Environment[i].Name < out.Environment[j].Name })
	path, source := filepath.Join(meta.Worktree, ".mcp.json"), "Project MCP configuration"
	if meta.Harness == "claude" && meta.TaskTmp != "" {
		path, source = filepath.Join(meta.TaskTmp, "mcp.json"), "Provisioned MCP configuration"
	}
	file, err := os.Open(path)
	if err == nil {
		defer file.Close()
		var configured struct {
			Servers map[string]json.RawMessage `json:"mcpServers"`
		}
		if err := json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&configured); err == nil {
			for name := range configured.Servers {
				if len(name) <= 128 {
					out.MCP = append(out.MCP, ScopedDetail{name, "Configured", source})
				}
			}
			sort.Slice(out.MCP, func(i, j int) bool { return out.MCP[i].Name < out.MCP[j].Name })
		} else {
			out.Notes = append(out.Notes, "MCP configuration could not be read.")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		out.Notes = append(out.Notes, "MCP configuration is unavailable.")
	}
	if meta.Harness == "codex" {
		out.Notes = append(out.Notes, "Codex also uses native user configuration; its runtime MCP connections are not reported here.")
	}
	return out, nil
}
