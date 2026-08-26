package auth

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/harness"
)

// WriteTable prints one honest line per service: its state, the reason behind
// it, and under it the resolution order for every variable the service
// declares - every place that was looked, in order, and which one answered.
// Values are never printed, only names, scopes, and provenance.
func WriteTable(w io.Writer, report Report) error {
	fmt.Fprintf(w, "project %s\n", report.Project)
	if report.Manifest != "" {
		fmt.Fprintf(w, "manifest %s\n", report.Manifest)
	}
	if report.Store != "" {
		fmt.Fprintf(w, "store %s\n", report.Store)
	}
	fmt.Fprintf(w, "resolution order env -> store/%s -> store/shared (shared only where the manifest declares it)\n", report.Project)
	if len(report.Statuses) == 0 {
		fmt.Fprintln(w, "no services declared")
		return nil
	}

	width := len("SERVICE")
	methodWidth := len("METHOD")
	for _, status := range report.Statuses {
		width = max(width, len(status.Service))
		methodWidth = max(methodWidth, len(status.Method))
	}
	refused := RefusedNames(report)
	fmt.Fprintf(w, "\n%-*s  %-*s  %-12s  %s\n", width, "SERVICE", methodWidth, "METHOD", "STATE", "DETAIL")
	for _, status := range report.Statuses {
		detail := status.Detail
		if status.Fixed != "" {
			detail = status.Fixed + " -> " + detail
		}
		fmt.Fprintf(w, "%-*s  %-*s  %-12s  %s\n", width, status.Service, methodWidth, status.Method, status.State, detail)
		for _, line := range resolutionLines(status) {
			fmt.Fprintf(w, "%-*s  %s\n", width, "", line)
		}
		// A green service whose variable silently never reaches the pane is
		// the report that sends a reader after the wrong fault.
		for _, name := range status.Declared {
			if prover, isRefused := refused[name]; isRefused && prover != status.Service {
				fmt.Fprintf(w, "%-*s    %s withheld: %s proved it names another instance\n", width, "", name, prover)
			}
		}
	}

	fmt.Fprintf(w, "\n%d of %d services green", countState(report, StateGreen), len(report.Statuses))
	if unverified := countState(report, StateUnverified); unverified > 0 {
		fmt.Fprintf(w, ", %d present but unverifiable", unverified)
	}
	if skipped := countState(report, StateSkipped); skipped > 0 {
		fmt.Fprintf(w, ", %d optional skipped", skipped)
	}
	if blocking := report.Blocking(); len(blocking) > 0 {
		fmt.Fprintf(w, ", %d blocking", len(blocking))
	}
	fmt.Fprintln(w)
	return nil
}

// resolutionLines renders the ordered candidates behind each declared
// variable. This is what makes "which DATABASE_URL did I get" answerable
// without reading a secret or guessing.
func resolutionLines(status Status) []string {
	names := make([]string, 0, len(status.Resolution))
	for name := range status.Resolution {
		names = append(names, name)
	}
	sort.Strings(names)

	nameWidth := 0
	for _, name := range names {
		nameWidth = max(nameWidth, len(name))
	}
	lines := make([]string, 0, len(names))
	for _, name := range names {
		parts := make([]string, 0, len(status.Resolution[name]))
		for _, candidate := range status.Resolution[name] {
			part := candidate.Source
			if candidate.Name != name {
				part += "(" + candidate.Name + ")"
			}
			switch {
			case candidate.Hit:
				part += " HIT"
			case candidate.Note != "":
				part += " [" + candidate.Note + "]"
			}
			parts = append(parts, part)
		}
		if len(parts) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("  %-*s  %s", nameWidth, name, strings.Join(parts, " -> ")))
	}
	return lines
}

func countState(report Report, state State) int {
	count := 0
	for _, status := range report.Statuses {
		if status.State == state {
			count++
		}
	}
	return count
}

// StoreCommand is the exact command that fixes one credential. It is printed
// rather than described so the Overlord can paste it.
func StoreCommand(project, name string) string {
	if project == "" {
		return "cfo auth store " + name
	}
	return "cfo auth store --project " + quoteScope(project) + " " + name
}

// quoteScope keeps a printed command pasteable. A checkout directory may
// legitimately contain a space, a parenthesis or an accent, and an unquoted
// scope would reach the flag parser as something other than one argument - a
// command that names the wrong credential, or none.
func quoteScope(scope string) string {
	for _, character := range scope {
		switch {
		case character == '-' || character == '_' || character == '.' || character == '/' || character == ':':
		case character >= 'A' && character <= 'Z':
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		default:
			return `"` + scope + `"`
		}
	}
	return scope
}

// remedies are the exact commands that can change one blocking verdict, and
// the single place every report surface asks what to print - a remedy that
// only one of them knows about is how an operator is sent to a command that
// cannot help. A wrong target is a fact about the value, so only re-storing
// the credential changes it: --fix re-runs a login and a probe, and neither
// can turn another project's instance into this one.
func remedies(project string, status Status) []string {
	names := status.Missing
	if status.ProvedWrongTarget() {
		names = status.Declared
	}
	if len(names) == 0 {
		return []string{"cfo auth " + quoteScope(project) + " --fix"}
	}
	scope := ProjectName(project)
	commands := make([]string, 0, len(names))
	for _, name := range names {
		commands = append(commands, StoreCommand(scope, name))
	}
	return commands
}

// RefusedNames maps every credential an identity check proved wrong to the
// service that proved it. The verdict is about the value, so the name is
// refused for every service that declares it - a sibling that only checked
// liveness resolves the same credential, and an optional service whose state
// was softened to skipped proved the same fact.
func RefusedNames(report Report) map[string]string {
	refused := map[string]string{}
	for _, status := range report.Statuses {
		if !status.ProvedWrongTarget() {
			continue
		}
		for _, name := range status.Declared {
			if _, seen := refused[name]; !seen {
				refused[name] = status.Service
			}
		}
	}
	return refused
}

// WriteLoginRequest prints the single consolidated request a human answers:
// every service still blocking, what it needs, and where to do it. One block
// covering everything is the point - goblins failing one credential at a time
// is the behaviour this replaces.
func WriteLoginRequest(w io.Writer, reports ...Report) error {
	type ask struct {
		project string
		status  Status
	}
	var asks []ask
	for _, report := range reports {
		for _, status := range report.Blocking() {
			asks = append(asks, ask{project: report.Project, status: status})
		}
	}
	if len(asks) == 0 {
		return nil
	}

	if len(asks) == 1 {
		fmt.Fprintln(w, "\nSIGN-IN REQUEST (1 service needs the Supreme Overlord)")
	} else {
		fmt.Fprintf(w, "\nSIGN-IN REQUEST (%d services need the Supreme Overlord)\n", len(asks))
	}
	fmt.Fprintln(w, "Everything below is blocked. Nothing else in the fleet needs your attention.")
	for _, item := range asks {
		fmt.Fprintf(w, "\n  %s / %s (%s)\n", item.project, item.status.Service, item.status.State)
		fmt.Fprintf(w, "    why: %s\n", item.status.Detail)
		for _, command := range remedies(item.project, item.status) {
			fmt.Fprintf(w, "    fix with: %s\n", command)
		}
		if item.status.URL != "" {
			fmt.Fprintf(w, "    where: %s\n", item.status.URL)
		}
	}
	fmt.Fprintln(w)
	return nil
}

// InjectEnv returns the environment a goblin's pane needs: every variable of
// every usable service, resolved in this project's scope. A service the probe
// rejected, or whose identity check named a different instance, contributes
// nothing - so a goblin never starts holding a credential for somebody else's
// database.
func InjectEnv(store Store, project string, manifest Manifest, report Report) (map[string]string, error) {
	usable := map[string]bool{}
	for _, status := range report.Statuses {
		if status.Usable() {
			usable[status.Service] = true
		}
	}
	refused := RefusedNames(report)
	resolver := Resolver{Store: store, Project: project}
	env := map[string]string{}
	for _, service := range manifest.Services {
		if !usable[service.Name] {
			continue
		}
		resolution, err := resolver.Resolve(service)
		if err != nil {
			return nil, err
		}
		for name, resolved := range resolution.Values {
			if _, isRefused := refused[name]; isRefused {
				continue
			}
			if IsHarnessBillingKey(name) {
				continue
			}
			env[name] = resolved.Value
		}
	}
	return env, nil
}

// IsHarnessBillingKey reports whether name is one a harness would take as
// its own billing credential. Such a name is never injected into a goblin,
// whatever a manifest declares: the harness must run on the subscription,
// and a project that needs the key for its OWN code loads it from its own
// environment file at runtime rather than from the pane.
func IsHarnessBillingKey(name string) bool {
	for _, key := range harness.HarnessBillingKeys {
		if strings.EqualFold(name, key) {
			return true
		}
	}
	return false
}
