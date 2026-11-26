package connections

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

// Repair is what a repair the board offers does: open a sign-in page, store a
// credential from the clipboard, or run a service's login command.
type Repair struct{ URL, Credential, Service string }

// repairCheckLimit bounds the check the repair command makes of its project
// before it plans.
const repairCheckLimit = 5 * time.Minute

// RepairPlan is the repair a project's item offers: only an action the
// snapshot's item lists, planned from the manifest, never from the snapshot's
// words.
func (e Environment) RepairPlan(project Project, connection, action string) (Repair, error) {
	if !slices.ContainsFunc(project.Items, func(item Item) bool { return item.ID == connection && slices.Contains(item.Actions, action) }) {
		return Repair{}, errors.New("Refresh this connection before fixing it.")
	}
	kind, name, ok := strings.Cut(connection, ":")
	if !ok || !validName(name) {
		return Repair{}, errors.New("Choose a connection from this project.")
	}
	manifest, err := auth.LoadManifest(e.DataDir, project.Name)
	if err != nil {
		return Repair{}, errors.New("Repository connections are unavailable.")
	}
	credential, isStore := strings.CutPrefix(action, "store:")
	if isStore && kind == "mcp" && slices.Contains(manifest.EnvNames(), credential) && !auth.IsHarnessBillingKey(credential) {
		return Repair{Credential: credential}, nil
	}
	if kind == "service" {
		for _, service := range manifest.Services {
			switch {
			case service.Name != name:
			case action == "login" && safeLoginURL(service.URL):
				return Repair{URL: service.URL}, nil
			case action == "cli" && len(service.Login) > 0:
				return Repair{Service: name}, nil
			case isStore && slices.Contains(service.Env, credential) && !auth.IsHarnessBillingKey(credential):
				return Repair{Credential: credential}, nil
			}
		}
	}
	return Repair{}, errors.New("This repair is no longer available. Refresh connections.")
}

func safeLoginURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

// RepairConnection runs a repair for the command that does it: it checks the
// project once, as that command's own process, so the repair is planned from
// what is true now, then runs the plan.
func (e Environment) RepairConnection(ctx context.Context, project Project, connection, action string) error {
	ctx, cancel := context.WithTimeout(ctx, repairCheckLimit)
	defer cancel()
	project.Level = e.CheckProject(ctx, project.Name, project.Path)
	plan, err := e.RepairPlan(project, connection, action)
	if err != nil {
		return err
	}
	if plan.Service == "" {
		return errors.New("Use this connection's sign-in or secure clipboard card.")
	}
	manifest, err := auth.LoadManifest(e.DataDir, project.Name)
	if err != nil {
		return errors.New("Repository connections are unavailable.")
	}
	manifest.Services = slices.DeleteFunc(manifest.Services, func(service auth.Service) bool { return service.Name != plan.Service })
	if len(manifest.Services) != 1 {
		return errors.New("Repository connection changed. Refresh connections.")
	}
	manifest.Services[0].Optional = false
	store, err := e.OpenStore()
	if err != nil {
		return errors.New("Credential store is unavailable.")
	}
	report, err := (auth.Checker{Store: store, Runner: workspaceRunner{runner: e.Runner, dir: project.Path}, Project: project.Name}).Fix(ctx, manifest)
	if err != nil || len(report.Statuses) != 1 || report.Statuses[0].State != auth.StateGreen {
		return errors.New("Sign-in did not complete. Recheck the connection.")
	}
	return nil
}
