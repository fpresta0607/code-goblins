package spawn

import (
	"fmt"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// spawnNeed is the services a new task's terminal is to carry: the ones its
// brief's Authentication section names. A helper carries what its parent
// carries, whatever its own brief says, since a goblin writes its helper's
// brief and no goblin gives itself a service.
func (s Service) spawnNeed(req Request) (auth.Need, error) {
	if req.Parent != "" {
		parent, err := state.ReadTaskMeta(s.StateDir, req.Parent)
		if err != nil {
			return auth.Need{}, fmt.Errorf("spawn: read the record of the helper's parent %s: %w", req.Parent, err)
		}
		return taskNeed(parent), nil
	}
	brief, err := fsx.ReadFile(req.BriefPath)
	if err != nil {
		return auth.Need{}, fmt.Errorf("spawn: read brief: %w", err)
	}
	need, err := auth.NeedFromBrief(string(brief))
	if err != nil {
		return auth.Need{}, fmt.Errorf("spawn: %w", err)
	}
	return need, nil
}

// taskNeed is the services a recorded task's next terminal and its credential
// script carry: the ones its record names. A task spawned by a build that
// gave every task all of its project's credentials has a record that names
// none, and is given what a brief that names none is given, the manifest's
// default services. Its brief is not read: it was written before a brief
// could name a service, and the goblin it briefs can write to it.
func taskNeed(meta state.TaskMeta) auth.Need {
	if !meta.HasCredentials {
		return auth.Need{IsUnstated: true}
	}
	return auth.Need{Services: meta.Credentials}
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
// terminal carries and how it comes by another: it says so in a blocked
// report, and the CFO grants it. It names services, never a value.
func credentialsInstruction(project string, grant auth.Grant) string {
	carried := "no service's credentials"
	if len(grant.Services) > 0 {
		carried = "the credentials of these services of " + auth.ProjectName(project) + ": " + strings.Join(grant.Services, ", ")
	}
	return " Your terminal carries " + carried + ", and of no other." +
		" When your task needs another service, say so in a blocked report that names the service and why, and never ask for a value: the CFO grants a service by name, and you are told to load it once he has."
}
