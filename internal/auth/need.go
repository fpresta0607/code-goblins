package auth

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// NoServices is the word a brief writes for a task that needs no service's
// credentials. A manifest cannot name a service by it.
const NoServices = "none"

// needSection and needKey are where a brief names its services: one
// `credentials:` line under its Authentication heading. mcpKey is the line
// beside it that names MCP servers.
const (
	needSection = "Authentication"
	needKey     = "credentials"
	mcpKey      = "mcp"
)

// Need is what a task asks of its project's manifest: the services whose
// credentials its terminal carries. A terminal carries nothing else from the
// credential store, so the zero Need is a task that carries none.
type Need struct {
	// Services are the services named for the task, by their names in the
	// project's manifest.
	Services []string
	// IsUnstated marks a brief that neither names a service nor says none,
	// the form every brief had before a brief named its services. Such a task
	// carries the services the manifest marks default and no other.
	IsUnstated bool
	// MCPServers are the MCP servers named for the task, by their names in
	// the project's .mcp.json. A server whose entry there holds a value
	// reaches the task only when it is named, so a brief with no `mcp:` line
	// is given none of those.
	MCPServers []string
}

// NeedFromBrief reads what a brief's Authentication section asks for: the
// services on its `credentials:` line, separated by commas, or none, and the
// MCP servers on its `mcp:` line in the same form. A brief with no
// `credentials:` line is in the older form and its need is unstated.
//
// A line that cannot be read is refused rather than guessed at, because the
// two guesses are a task that starts without what it needs and a task that
// starts with what nobody gave it.
func NeedFromBrief(brief string) (Need, error) {
	var need Need
	section, isStated := "", map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(brief, "\r\n", "\n"), "\n") {
		if heading, found := strings.CutPrefix(line, "## "); found {
			section = strings.TrimSpace(heading)
			continue
		}
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		key = strings.ToLower(strings.TrimSpace(key))
		if section != needSection || !found || (key != needKey && key != mcpKey) {
			continue
		}
		if isStated[key] {
			return Need{}, fmt.Errorf("the brief's %s section has more than one %s line", needSection, key)
		}
		isStated[key] = true
		names, err := needNames(key, value)
		if err != nil {
			return Need{}, err
		}
		if key == mcpKey {
			need.MCPServers = names
		} else {
			need.Services = names
		}
	}
	need.IsUnstated = !isStated[needKey]
	return need, nil
}

// needNames reads the value of a brief's credentials line or its mcp line.
func needNames(key, value string) ([]string, error) {
	named := "service"
	if key == mcpKey {
		named = "server"
	}
	form := "write " + key + ": followed by " + named + " names separated by commas, or " + key + ": " + NoServices
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("the brief's " + key + " line names nothing: " + form)
	}
	var names []string
	isNone := false
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		switch {
		case name == "":
			return nil, errors.New("the brief's " + key + " line has an empty name: " + form)
		case strings.EqualFold(name, NoServices):
			isNone = true
		case !slices.Contains(names, name):
			names = append(names, name)
		}
	}
	if isNone && len(names) > 0 {
		return nil, errors.New("the brief's " + key + " line says " + NoServices + " beside a " + named + ": " + form)
	}
	return names, nil
}

// Grant is a need resolved against a manifest.
type Grant struct {
	// Services are the services whose credentials the task carries, in the
	// manifest's order. It is what the task's record keeps.
	Services []string
	// Withheld are the services the manifest declares and the task does not
	// carry, in the manifest's order.
	Withheld []string
	// Unknown are the services the need names that the manifest does not
	// declare, in the order named.
	Unknown []string
}

// Grant resolves a need: the services it names, or the default ones when it
// is unstated. A name the manifest does not declare grants nothing and is
// returned as unknown, for the caller to refuse or to report.
func (m Manifest) Grant(need Need) Grant {
	var grant Grant
	for _, service := range m.Services {
		isGranted := slices.Contains(need.Services, service.Name)
		if need.IsUnstated {
			isGranted = service.Default
		}
		if isGranted {
			grant.Services = append(grant.Services, service.Name)
		} else {
			grant.Withheld = append(grant.Withheld, service.Name)
		}
	}
	if need.IsUnstated {
		return grant
	}
	for _, name := range need.Services {
		if !slices.Contains(grant.Services, name) && !slices.Contains(grant.Unknown, name) {
			grant.Unknown = append(grant.Unknown, name)
		}
	}
	return grant
}

// UnknownLine says which services a need names that the project's manifest
// does not declare, and which it does. A name shaped like a credential value
// is counted and never repeated: a brief names a service, never a value.
func UnknownLine(project string, grant Grant) string {
	var names []string
	valueShaped := 0
	for _, name := range grant.Unknown {
		if SecretShape(name) != "" {
			valueShaped++
			continue
		}
		names = append(names, name)
	}
	if valueShaped > 0 {
		names = append(names, fmt.Sprintf("%d shaped like a credential value, which a brief never holds", valueShaped))
	}
	line := fmt.Sprintf("%s's manifest declares no service named %s", ProjectName(project), strings.Join(names, ", "))
	declared := append(slices.Clone(grant.Services), grant.Withheld...)
	if len(declared) == 0 {
		return line + ". It declares none, so there is none to name"
	}
	slices.Sort(declared)
	return line + ". It declares " + strings.Join(declared, ", ")
}

// Only returns the manifest reduced to the named services, in its own order.
// It is the gate every terminal's credentials pass: what builds a terminal's
// environment or its credential script reads the services of this manifest
// and nothing else.
func (m Manifest) Only(services []string) Manifest {
	only := m
	only.Services = nil
	for _, service := range m.Services {
		if slices.Contains(services, service.Name) {
			only.Services = append(only.Services, service)
		}
	}
	return only
}

// sharing returns the manifest reduced to the named services and every other
// service that declares a variable one of them declares. An identity check's
// verdict is about the value, so such a sibling is checked with the task's
// own services: the name it proves wrong is refused for them too.
func (m Manifest) sharing(services []string) Manifest {
	declared := map[string]bool{}
	for _, service := range m.Only(services).Services {
		for _, name := range service.Env {
			declared[name] = true
		}
	}
	sharing := m
	sharing.Services = nil
	for _, service := range m.Services {
		isSibling := slices.ContainsFunc(service.Env, func(name string) bool { return declared[name] })
		if isSibling || slices.Contains(services, service.Name) {
			sharing.Services = append(sharing.Services, service)
		}
	}
	return sharing
}

// consumed returns every name the manifest's services read: the ones they
// declare and the ones their aliases name.
func (m Manifest) consumed() map[string]bool {
	names := map[string]bool{}
	for _, chain := range m.CredentialChains() {
		for _, name := range chain {
			names[name] = true
		}
	}
	return names
}

// withheldNames returns, sorted, the names the manifest reads only for
// services outside carried: the variables a task that carries those services
// must not find in its terminal, whatever road they took there. A name a
// carried service reads too is not among them.
func (m Manifest) withheldNames(carried []string) []string {
	kept := m.Only(carried).consumed()
	var names []string
	for name := range m.consumed() {
		if !kept[name] {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// UndeclaredNames returns the names stored in a project's scope that no
// service of its manifest declares or aliases, sorted. No service can be
// named for one, so it reaches no terminal: it is returned by name for the
// report that says so, and its value is never read.
func UndeclaredNames(store Store, project string, manifest Manifest) ([]string, error) {
	chains := manifest.CredentialChains()
	declared := map[string]bool{}
	for _, chain := range chains {
		for _, name := range chain {
			declared[name] = true
		}
	}
	keys, err := store.Keys()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, key := range keys {
		if key.Project == project && !declared[key.Name] && !IsHarnessBillingKey(key.Name) {
			names = append(names, key.Name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// WithheldLine says in one line what a task's terminal does not carry and the
// one command that grants a service, or nothing when it carries all there is.
// It names services and credential names, never a value.
func WithheldLine(task, project string, result Result) string {
	var parts []string
	if len(result.Grant.Withheld) > 0 {
		parts = append(parts, fmt.Sprintf("withheld %d of %s's services (%s), grant one with `cfo auth grant %s <service>`",
			len(result.Grant.Withheld), ProjectName(project), strings.Join(result.Grant.Withheld, ", "), task))
	}
	if len(result.Undeclared) > 0 {
		parts = append(parts, fmt.Sprintf("%d stored name(s) no service declares reach no task (%s), declare a service for one in the manifest to grant it",
			len(result.Undeclared), strings.Join(result.Undeclared, ", ")))
	}
	if len(parts) == 0 {
		return ""
	}
	return "auth: " + strings.Join(parts, ". ")
}
