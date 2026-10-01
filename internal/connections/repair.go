package connections

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type Repair struct{ URL, Credential, Service, Server string }

func (i *Inspector) RepairPlan(meta state.TaskMeta, snapshot Snapshot, connection, action string) (Repair, error) {
	if !slices.ContainsFunc(snapshot.Entries, func(entry Entry) bool { return entry.ID == connection && slices.Contains(entry.Actions, action) }) {
		return Repair{}, errors.New("Refresh this connection before fixing it.")
	}
	kind, name, ok := strings.Cut(connection, ":")
	if !ok || !validName(name) {
		return Repair{}, errors.New("Choose a connection from this goblin.")
	}
	manifest, err := auth.LoadManifest(i.DataDir, meta.Project)
	if err != nil && kind == "service" {
		return Repair{}, errors.New("Repository connections are unavailable.")
	}
	if kind == "service" {
		for _, service := range manifest.Services {
			if service.Name != name {
				continue
			}
			if action == "login" && safeLoginURL(service.URL) {
				return Repair{URL: service.URL}, nil
			}
			if action == "cli" && len(service.Login) > 0 {
				return Repair{Service: name}, nil
			}
			if credential, ok := strings.CutPrefix(action, "store:"); ok && slices.Contains(service.Env, credential) && !auth.IsHarnessBillingKey(credential) {
				return Repair{Credential: credential}, nil
			}
		}
	}
	if kind == "credential" && action == "store:"+name && slices.Contains(manifest.EnvNames(), name) && !auth.IsHarnessBillingKey(name) {
		return Repair{Credential: name}, nil
	}
	if kind == "mcp" && action == "login" && meta.Harness == "codex" {
		return Repair{Server: name}, nil
	}
	if kind == "mcp" && meta.Harness == "claude" && strings.HasPrefix(action, "store:") && slices.Contains(manifest.EnvNames(), strings.TrimPrefix(action, "store:")) {
		for _, entry := range configEntries(filepath.Join(meta.TaskTmp, "mcp.json")) {
			if entry.ID == connection && slices.Contains(entry.Actions, action) {
				return Repair{Credential: strings.TrimPrefix(action, "store:")}, nil
			}
		}
	}
	if kind == "withheld" && strings.HasPrefix(action, "store:") {
		for _, entry := range withheldEntries(meta, nil) {
			if entry.ID == connection && slices.Contains(entry.Actions, action) && slices.Contains(manifest.EnvNames(), strings.TrimPrefix(action, "store:")) {
				return Repair{Credential: strings.TrimPrefix(action, "store:")}, nil
			}
		}
	}
	return Repair{}, errors.New("This repair is no longer available. Refresh connections.")
}

func safeLoginURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func (i *Inspector) RepairConnection(ctx context.Context, meta state.TaskMeta, connection, action string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	checkCtx, checkCancel := context.WithTimeout(ctx, 45*time.Second)
	snapshot := i.Check(checkCtx, meta)
	checkCancel()
	plan, err := i.RepairPlan(meta, snapshot, connection, action)
	if err != nil {
		return err
	}
	if plan.Server != "" {
		program, err := spawn.NativeProgram("codex", "mcp", "login", plan.Server)
		if err != nil {
			return errors.New("Codex login is unavailable.")
		}
		env, _, err := i.Runtime(ctx, i.StateDir, meta)
		if err != nil {
			return err
		}
		result, err := i.Runner.Run(ctx, execx.Request{Name: program[0], Args: program[1:], Dir: meta.Worktree, Env: env, KillTree: true})
		if err != nil || result.ExitCode != 0 {
			return errors.New("MCP sign-in did not complete.")
		}
		return nil
	}
	if plan.Service != "" {
		manifest, err := auth.LoadManifest(i.DataDir, meta.Project)
		if err != nil {
			return errors.New("Repository connections are unavailable.")
		}
		manifest.Services = slices.DeleteFunc(manifest.Services, func(service auth.Service) bool { return service.Name != plan.Service })
		if len(manifest.Services) != 1 {
			return errors.New("Repository connection changed. Refresh connections.")
		}
		manifest.Services[0].Optional = false
		store, err := i.OpenStore()
		if err != nil {
			return errors.New("Credential store is unavailable.")
		}
		report, err := (auth.Checker{Store: store, Runner: workspaceRunner{Runner: i.Runner, dir: meta.Worktree}, Project: auth.ProjectName(meta.Project)}).Fix(ctx, manifest)
		if err != nil || len(report.Statuses) != 1 || report.Statuses[0].State != auth.StateGreen {
			return errors.New("Sign-in did not complete. Recheck the connection.")
		}
		return nil
	}
	return errors.New("Use this connection's sign-in or secure clipboard card.")
}
