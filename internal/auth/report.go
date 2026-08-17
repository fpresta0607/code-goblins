package auth

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// WriteTable prints one honest line per service: its state, where each
// credential came from, and the reason behind anything that is not green.
// Values are never printed, only their provenance.
func WriteTable(w io.Writer, report Report) error {
	fmt.Fprintf(w, "project %s\n", report.Project)
	if report.Manifest != "" {
		fmt.Fprintf(w, "manifest %s\n", report.Manifest)
	}
	if report.Store != "" {
		fmt.Fprintf(w, "store %s\n", report.Store)
	}
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
	fmt.Fprintf(w, "\n%-*s  %-*s  %-7s  %s\n", width, "SERVICE", methodWidth, "METHOD", "STATE", "DETAIL")
	for _, status := range report.Statuses {
		detail := status.Detail
		if status.Fixed != "" {
			detail = status.Fixed + " -> " + detail
		}
		if sources := describeSources(status); sources != "" {
			detail = sources + "; " + detail
		}
		fmt.Fprintf(w, "%-*s  %-*s  %-7s  %s\n", width, status.Service, methodWidth, status.Method, status.State, detail)
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

func countState(report Report, state State) int {
	count := 0
	for _, status := range report.Statuses {
		if status.State == state {
			count++
		}
	}
	return count
}

// describeSources names where each resolved credential came from, so a
// surprising probe result can be traced without printing a secret.
func describeSources(status Status) string {
	if len(status.Sources) == 0 {
		return ""
	}
	names := make([]string, 0, len(status.Sources))
	for name := range status.Sources {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+" from "+status.Sources[name])
	}
	return strings.Join(parts, ", ")
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
		if len(item.status.Missing) > 0 {
			fmt.Fprintf(w, "    store with: cfo auth store %s <value>\n", item.status.Missing[0])
			if len(item.status.Missing) > 1 {
				fmt.Fprintf(w, "               (also %s)\n", strings.Join(item.status.Missing[1:], ", "))
			}
		}
		if item.status.URL != "" {
			fmt.Fprintf(w, "    where: %s\n", item.status.URL)
		}
	}
	fmt.Fprintln(w)
	return nil
}

// InjectEnv returns the environment a goblin's pane needs: every variable of
// every usable service, resolved. A service the probe rejected contributes
// nothing, so a goblin never starts with a credential that looks present and
// fails on first use.
func InjectEnv(store Store, manifest Manifest, report Report) (map[string]string, error) {
	usable := map[string]bool{}
	for _, status := range report.Statuses {
		if status.Usable() {
			usable[status.Service] = true
		}
	}
	var names []string
	for _, service := range manifest.Services {
		if usable[service.Name] {
			names = append(names, service.Env...)
		}
	}
	resolved, _, err := Resolve(store, names)
	if err != nil {
		return nil, err
	}
	env := make(map[string]string, len(resolved))
	for name, value := range resolved {
		env[name] = value.Value
	}
	return env, nil
}
