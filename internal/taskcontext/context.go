// Package taskcontext publishes bounded, credential-free pointers for a task.
// The manifest and browser evidence outlive its disposable code worktree.
package taskcontext

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type Browser struct {
	Session  string `json:"session"`
	Profile  string `json:"profile"`
	Evidence string `json:"evidence"`
}
type Paths struct {
	Manifest string  `json:"manifest"`
	Recap    string  `json:"recap"`
	Tests    string  `json:"tests"`
	Browser  Browser `json:"browser"`
}
type Manifest struct {
	Schema            string               `json:"schema"`
	ID                string               `json:"task_id"`
	Project           string               `json:"project"`
	Worktree          string               `json:"worktree"`
	Branch            string               `json:"branch,omitempty"`
	Head              string               `json:"head,omitempty"`
	Brief             string               `json:"brief"`
	BriefSource       string               `json:"brief_source"`
	BriefStatus       string               `json:"brief_status"`
	Harness           string               `json:"harness"`
	GatePolicy        string               `json:"gate_policy"`
	GateBudget        string               `json:"gate_budget"`
	Decisions         []crewstate.Decision `json:"open_decisions"`
	Observation       *monitor.Observation `json:"observation,omitempty"`
	UpdatedAt         time.Time            `json:"updated_at"`
	MetadataMissing   bool                 `json:"metadata_missing"`
	CheckedAt         time.Time            `json:"checked_at"`
	Pointers          map[string]string    `json:"pointers"`
	IdentityStatus    string               `json:"identity_status"`
	DecisionsStatus   string               `json:"decisions_status"`
	ObservationStatus string               `json:"observation_status"`
	Paths
}

func PathsFor(h home.Home, id string) Paths {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(h.State))))
	taskSum := sha256.Sum256([]byte(id))
	root := filepath.Join(h.State, "tasks", id)
	return Paths{Manifest: filepath.Join(root, "context.json"), Recap: filepath.Join(root, "recap.html"), Tests: filepath.Join(root, "tests.json"), Browser: Browser{Session: fmt.Sprintf("cfo-%x-%x", sum[:6], taskSum[:8]), Profile: filepath.Join(root, "browser", "profile"), Evidence: filepath.Join(root, "browser", "evidence")}}
}

func Refresh(ctx context.Context, h home.Home, id string, commands execx.Runner) (Manifest, error) {
	if err := state.ValidTaskID(id); err != nil {
		return Manifest{}, err
	}
	paths := PathsFor(h, id)
	meta, err := state.ReadTaskMeta(h.State, id)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Manifest{}, err
		}
		data, e := os.ReadFile(paths.Manifest)
		if e != nil {
			return Manifest{}, err
		}
		var saved Manifest
		if e = json.Unmarshal(data, &saved); e != nil {
			return Manifest{}, e
		}
		if saved.Schema != "cfo-task-context.v1" || saved.ID != id || saved.Paths != paths {
			return Manifest{}, errors.New("context: stored ownership differs")
		}
		saved.MetadataMissing = true
		saved.IdentityStatus = "stale: task metadata missing; branch and head are last known"
		saved.DecisionsStatus = "stale: retained unresolved decisions"
		return save(paths, saved)
	}
	m := Manifest{Schema: "cfo-task-context.v1", ID: id, Project: meta.Project, Worktree: meta.Worktree, Brief: meta.Brief, Harness: meta.Harness, GatePolicy: filepath.Join(meta.TaskTmp, "pipeline.json"), GateBudget: filepath.Join(pathsRoot(paths), "gate-budget.json"), UpdatedAt: time.Now().UTC(), Paths: paths, Decisions: []crewstate.Decision{}}
	if err := os.MkdirAll(pathsRoot(paths), 0700); err != nil {
		return Manifest{}, err
	}
	m.BriefSource = meta.Brief
	m.Brief = filepath.Join(pathsRoot(paths), "brief.md")
	m.BriefStatus = "current: saved task copy"
	if data, e := os.ReadFile(meta.Brief); e == nil {
		if e = fsx.AtomicWriteFile(m.Brief, data); e != nil {
			return Manifest{}, e
		}
	} else {
		m.BriefStatus = "source unavailable: retained copy may be stale; consult pointer status"
	}
	if data, e := os.ReadFile(m.GatePolicy); e == nil {
		m.GatePolicy = filepath.Join(pathsRoot(paths), "pipeline.json")
		if e = fsx.AtomicWriteFile(m.GatePolicy, data); e != nil {
			return Manifest{}, e
		}
	} else if _, e = os.Stat(filepath.Join(pathsRoot(paths), "pipeline.json")); e == nil {
		m.GatePolicy = filepath.Join(pathsRoot(paths), "pipeline.json")
	}
	lines, e := fsx.ReadLines(filepath.Join(h.State, id+".status"))
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return Manifest{}, e
	}
	m.Decisions = crewstate.FoldOpenDecisions(lines)
	m.DecisionsStatus = "current"
	if errors.Is(e, os.ErrNotExist) {
		m.DecisionsStatus = "missing: status history unavailable"
		if data, readErr := os.ReadFile(paths.Manifest); readErr == nil {
			var saved Manifest
			if json.Unmarshal(data, &saved) == nil && saved.ID == id && saved.Paths == paths {
				m.Decisions = saved.Decisions
				m.DecisionsStatus = "stale: retained unresolved decisions; status history missing"
			}
		}
	}
	m.ObservationStatus = "missing"
	if observation, e := monitor.ReadObservation(h.State, id); e == nil {
		m.Observation = &observation
		m.ObservationStatus = "available: consult last_observed timestamp"
	} else if !errors.Is(e, os.ErrNotExist) {
		m.ObservationStatus = "unreadable"
	}
	m.IdentityStatus = "unobserved"
	if commands != nil && meta.Worktree != "" {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		r, e := commands.Run(ctx, execx.Request{Dir: meta.Worktree, Name: "git", Args: []string{"status", "--porcelain=v2", "--branch", "--untracked-files=no"}})
		m.IdentityStatus = "unavailable: git identity probe failed"
		if e == nil && r.ExitCode == 0 {
			for _, line := range strings.Split(string(r.Stdout), "\n") {
				if value, ok := strings.CutPrefix(line, "# branch.oid "); ok {
					m.Head = strings.TrimSpace(value)
				}
				if value, ok := strings.CutPrefix(line, "# branch.head "); ok {
					m.Branch = strings.TrimSpace(value)
				}
			}
			if len(m.Head) == 40 && m.Branch != "" {
				m.IdentityStatus = "current"
			}
		}
	}
	return save(paths, m)
}

func save(paths Paths, m Manifest) (Manifest, error) {
	m.CheckedAt = time.Now().UTC()
	m.Pointers = map[string]string{}
	for name, path := range map[string]string{"worktree": m.Worktree, "brief": m.Brief, "gate_policy": m.GatePolicy, "gate_budget": m.GateBudget, "tests": m.Tests, "recap": m.Recap, "browser_profile": m.Browser.Profile, "browser_evidence": m.Browser.Evidence} {
		status := "missing"
		if path != "" {
			if _, err := os.Stat(path); err == nil {
				status = "present"
			} else if !errors.Is(err, os.ErrNotExist) {
				status = "unreadable"
			}
		}
		m.Pointers[name] = status
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err = os.MkdirAll(pathsRoot(paths), 0700); err != nil {
		return Manifest{}, err
	}
	return m, fsx.AtomicWriteFile(paths.Manifest, append(data, '\n'))
}
func pathsRoot(p Paths) string { return filepath.Dir(p.Manifest) }

func Instruction(id string) string {
	return " Run cfo context " + id + " and read its saved brief and evidence pointers to reconstruct task identity, open decisions and gate budget after any restart or harness switch. Before reporting done, save a Lavish HTML recap with cfo recap " + id + " --file <html>; include changes, reasons, test evidence, UI screenshots when applicable, limitations and the accurate PR/merge/deployment state. Browser feedback is optional."
}

func BrowserEnv(h home.Home, id string) map[string]string {
	b := PathsFor(h, id).Browser
	backend := os.Getenv("CFO_CHROME_MCP_PATH")
	if backend == "" {
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			backend = filepath.Join(appdata, "npm", "node_modules", "chrome-devtools-mcp", "build", "src", "bin", "chrome-devtools-mcp.js")
		} else {
			backend = filepath.Join(h.State, "browser-backend-not-configured.js")
		}
	}
	return map[string]string{"CHROME_DEVTOOLS_AXI_SESSION": b.Session, "CHROME_DEVTOOLS_AXI_USER_DATA_DIR": b.Profile, "CHROME_DEVTOOLS_AXI_AUTO_CONNECT": "0", "CHROME_DEVTOOLS_AXI_BROWSER_URL": "", "CHROME_DEVTOOLS_AXI_PORT": "", "CHROME_DEVTOOLS_AXI_HEADED": "0", "CHROME_DEVTOOLS_AXI_MCP_PATH": backend, "CHROME_DEVTOOLS_AXI_CHROME_ARGS": "", "CHROME_DEVTOOLS_AXI_WS_HEADERS": "", "CHROME_DEVTOOLS_AXI_CHANNEL": "stable"}
}
