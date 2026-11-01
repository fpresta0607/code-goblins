package spawn

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// spawnNeed is the services and MCP servers a new task's terminal is to
// carry: the ones its brief's Authentication section names. A helper narrows
// what its parent carries and never widens it, so each line its brief states
// must name no more than its parent has, and a line its brief leaves out is
// its parent's.
func (s Service) spawnNeed(req Request, brief []byte) (auth.Need, error) {
	need, err := auth.NeedFromBrief(string(brief))
	if err != nil {
		return auth.Need{}, fmt.Errorf("spawn: %w", err)
	}
	if req.Parent == "" {
		return need, nil
	}
	parent, err := state.ReadTaskMeta(s.StateDir, req.Parent)
	if err != nil {
		return auth.Need{}, fmt.Errorf("spawn: read the record of the helper's parent %s: %w", req.Parent, err)
	}
	carried, err := s.parentNeed(parent)
	if err != nil {
		return auth.Need{}, err
	}
	return helperNeed(req.Parent, carried, need)
}

// parentNeed is what a helper's parent carries: the services and MCP servers
// its record names. A parent an older build spawned names no service, and its
// terminals carry the manifest's default services, which is what its helper
// may narrow.
func (s Service) parentNeed(parent state.TaskMeta) (auth.Need, error) {
	need := taskNeed(parent)
	if !need.IsUnstated {
		return need, nil
	}
	manifest, err := auth.LoadManifest(s.Worktrees.DataDir, parent.Project)
	if errors.Is(err, fs.ErrNotExist) {
		return auth.Need{MCPServers: need.MCPServers}, nil
	}
	if err != nil {
		return auth.Need{}, fmt.Errorf("spawn: %w", err)
	}
	return auth.Need{Services: manifest.Grant(need).Services, MCPServers: need.MCPServers}, nil
}

// helperNeed is the need a helper starts with: its own brief's Authentication
// section, which may select fewer services and MCP servers than its parent
// carries but never more, and its parent's for a line its brief leaves out.
// A name its parent does not carry is refused rather than silently dropped,
// since a helper that starts without what its brief says it needs fails at
// the work instead of at the request.
func helperNeed(parentID string, parent, brief auth.Need) (auth.Need, error) {
	need := parent
	if !brief.IsUnstated {
		if refusal := helperRefusal(parentID, "credentials", "service", parent.Services, brief.Services); refusal != "" {
			return auth.Need{}, errors.New(refusal)
		}
		need.Services = brief.Services
	}
	if !brief.IsMCPUnstated {
		if refusal := helperRefusal(parentID, "mcp", "MCP server", parent.MCPServers, brief.MCPServers); refusal != "" {
			return auth.Need{}, errors.New(refusal)
		}
		need.MCPServers = brief.MCPServers
	}
	return need, nil
}

// helperRefusal says which names a helper's brief asks for that its parent
// does not carry, and the line that would name some of what the parent does.
// It names services and servers, never a value.
func helperRefusal(parentID, key, subject string, carried, named []string) string {
	var added []string
	for _, name := range named {
		if !slices.Contains(carried, name) {
			added = append(added, name)
		}
	}
	if len(added) == 0 {
		return ""
	}
	continues := "which is"
	if len(added) > 1 {
		subject += "s"
		continues = "which are"
	}
	return fmt.Sprintf("spawn: the helper's brief names the %s %s, %s not among what its parent %s carries. A helper narrows what its parent carries and never widens it: its %s line names some of %s, or %s: %s, and a line it leaves out is its parent's",
		subject, strings.Join(added, ", "), continues, parentID, key, namesOr(carried, auth.NoServices), key, auth.NoServices)
}

// taskNeed is the services a recorded task's next terminal and its credential
// script carry: the ones its record names. A task spawned by a build that
// gave every task all of its project's credentials has a record that names
// none, and is given what a brief that names none is given, the manifest's
// default services. Its brief is not read: it was written before a brief
// could name a service, and the goblin it briefs can write to it. The MCP
// servers are the ones the record names, which is none for such a task.
func taskNeed(meta state.TaskMeta) auth.Need {
	if !meta.HasCredentials {
		return auth.Need{IsUnstated: true, MCPServers: meta.MCPServers}
	}
	return auth.Need{Services: meta.Credentials, MCPServers: meta.MCPServers}
}

// goblinHasVariable reports whether a goblin's terminal, built from the
// user's environment and the task's credentials, sets a variable. It decides
// whether an MCP server that authenticates by that variable is handed to the
// goblin. No harness billing key ever reaches a goblin, whatever its source.
func (s Service) goblinHasVariable(userEnv []string, credentials auth.Result) func(name string) bool {
	return func(name string) bool {
		return !auth.IsHarnessBillingKey(name) && hasNativeVariable(s.nativeHostEnvironment(userEnv, harness.Launch{}, credentials), name)
	}
}

// undefinedServersLine says which of the named MCP servers the project's
// .mcp.json does not define, and which it does, or nothing when it defines
// them all. A name shaped like a credential value is counted and never
// repeated: a brief names a server, never a value.
func undefinedServersLine(project string, named []string) (string, error) {
	defined, err := worktree.MCPServerNames(project)
	if err != nil {
		return "", err
	}
	var names []string
	valueShaped := 0
	for _, name := range named {
		switch {
		case slices.Contains(defined, name):
		case auth.SecretShape(name) != "":
			valueShaped++
		default:
			names = append(names, name)
		}
	}
	if valueShaped > 0 {
		names = append(names, fmt.Sprintf("%d shaped like a credential value, which a brief never holds", valueShaped))
	}
	if len(names) == 0 {
		return "", nil
	}
	line := auth.ProjectName(project) + "'s .mcp.json defines no server named " + strings.Join(names, ", ")
	if len(defined) == 0 {
		return line + ". It defines none, so there is none to name", nil
	}
	return line + ". It defines " + strings.Join(defined, ", "), nil
}

// mcpHeldLine says in one line which MCP servers a task's terminal was not
// given because their entry in the project's .mcp.json holds a value, and the
// one command that grants one. It names servers, never a value.
func mcpHeldLine(task string, held []string) string {
	servers := "servers"
	if len(held) == 1 {
		servers = "server"
	}
	return fmt.Sprintf("mcp: withheld %d %s whose entry holds a value (%s), grant one with `cfo auth grant %s --mcp <server>`", len(held), servers, strings.Join(held, ", "), task)
}

// mcpTakenLine says what a relaunch took from a task: MCP servers its last
// terminal was given, whose entry holds a value and which its record does not
// name. A task spawned before such servers were withheld loses them so.
func mcpTakenLine(id string, taken []string) string {
	return fmt.Sprintf("%s was given the MCP servers %s by its last terminal's configuration. Their entry in the project's .mcp.json holds a value and its record does not name them, so they are withheld from this relaunch on", id, strings.Join(taken, ", "))
}

// mcpInstruction tells a goblin which MCP servers it was not given and how it
// comes by one, or nothing when none was withheld for holding a value.
func mcpInstruction(held []string) string {
	if len(held) == 0 {
		return ""
	}
	return " These MCP servers of the project were withheld from you, since their entry holds a value: " + strings.Join(held, ", ") + "." +
		" When your task needs one, say so in a blocked report that names the server and why: the CFO grants a server by name, and it reaches you at your next restart."
}

// recordedServices is what a task's record names after a preflight: the
// services it carries and any the manifest does not declare just now, sorted.
// A name the manifest dropped stays in the record, so a service taken out of
// a manifest and put back is carried again without a second grant.
func recordedServices(grant auth.Grant) []string {
	services := append(slices.Clone(grant.Services), grant.Unknown...)
	slices.Sort(services)
	return services
}

// narrowedLine says what became of a task an older build spawned at its first
// relaunch: the build that started it gave its terminal everything stored for
// its project, and its terminals now carry only what the line names.
func narrowedLine(id, project string, grant auth.Grant) string {
	carried := "no service's credentials"
	if len(grant.Services) > 0 {
		carried = "only " + strings.Join(grant.Services, ", ")
	}
	return fmt.Sprintf("%s was started by a build that gave it every credential stored for %s. Its terminals carry %s from this relaunch on", id, auth.ProjectName(project), carried)
}

// credentialsInstruction tells a goblin which services' credentials its
// terminal carries and how it requests another on a card, by name only.
func credentialsInstruction(project string, grant auth.Grant) string {
	carried := "no service's credentials"
	if len(grant.Services) > 0 {
		carried = "the credentials of these services of " + auth.ProjectName(project) + ": " + strings.Join(grant.Services, ", ")
	}
	return " Your terminal carries " + carried + ", and of no other." +
		" When your task needs another declared service, file cfo auth request --project <project> --task <your-task-id> --grant <service> --why \"<why you need it>\" --wait: the Overlord grants or denies it on its Command Center card. Never grant it yourself, and never ask for a value. Once granted, re-source your auth.ps1."
}
