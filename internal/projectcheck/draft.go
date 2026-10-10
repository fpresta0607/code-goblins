package projectcheck

import (
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/project"
)

// proven is what the checks proved that a record can be drafted from.
type proven struct {
	// record is the project's record, when it is there and loads.
	record *project.Manifest
	// gateTest is the gate's test command, one command for each of its
	// parts, when it can run as written at the root.
	gateTest []project.Command
	// workflowTests are the test commands the workflows run at the root that
	// can run as written, and workflowFiles the workflows that run them.
	workflowTests []project.Command
	workflowFiles []string
	// instructionTests are the test commands the instruction files name
	// that can run as written at the root, and instructionFiles those files.
	instructionTests []project.Command
	instructionFiles []string
	// services are the declared services the check counts as used.
	services []auth.Service
}

// verifies reports whether a record names a verification command. A record
// with none lets `cfo verify` pass with nothing run.
func verifies(manifest project.Manifest) bool {
	for _, tier := range [][]project.Command{manifest.Verification.Fast, manifest.Verification.Full, manifest.Verification.Deep} {
		for _, command := range tier {
			if len(command) > 0 {
				return true
			}
		}
	}
	return false
}

// draft returns the project's record as this assessment can vouch for it,
// and one sentence saying where its verification commands came from. It
// starts from the record that is there, so nothing a person set is drafted
// away, and fills only what that leaves empty: the services the check counts
// as used, and a fast tier. The fast tier is the gate's own test command.
// For a record with no verification command at all, which the check itself
// reports, it is otherwise the test commands the workflows run, or else
// those the instruction files name. The check runs none of them, and the
// sentence says so. Deployment is never drafted, since a wrong deploy
// command is one `cfo deploy` would run.
func (c *checker) draft() (project.Manifest, string) {
	manifest := project.Manifest{}
	if c.proven.record != nil {
		manifest = *c.proven.record
	}
	manifest.Project = c.project
	if len(manifest.Services) == 0 {
		for _, service := range c.proven.services {
			manifest.Services = append(manifest.Services, project.Service{
				Name: service.Name, Kind: serviceKind(service), Provider: service.Name, Env: service.Env,
			})
		}
	}
	fast := &manifest.Verification.Fast
	const unproven = ". This check ran none of them, so prove each before the draft is placed"
	switch {
	case len(*fast) == 0 && len(c.proven.gateTest) > 0:
		*fast = c.proven.gateTest
		return manifest, "the draft's fast tier is the gate's own test command, commands.test of " + gateFileName + ", as " + count(len(*fast), "command") + unproven
	case verifies(manifest):
		return manifest, "the draft keeps the verification commands of the record that is there"
	case len(c.proven.workflowTests) > 0:
		*fast = once(c.proven.workflowTests)
		return manifest, "the gate names no test command a record can hold, so the draft's fast tier is the " + count(len(*fast), "test command") + " the workflows run at the repository's root (" + strings.Join(c.proven.workflowFiles, ", ") + ")" + unproven
	case len(c.proven.instructionTests) > 0:
		*fast = once(c.proven.instructionTests)
		return manifest, "the gate names no test command a record can hold and no workflow runs one at the repository's root, so the draft's fast tier is the " + count(len(*fast), "test command") + " the instruction files name (" + strings.Join(c.proven.instructionFiles, ", ") + ")" + unproven
	}
	return manifest, "the draft names no verification command: the gate names no test command a record can hold, no workflow runs one at the repository's root and the instruction files name none that can run there. A record with none lets cfo verify pass with nothing run, so name the project's test command in " + gateFileName + " or write the record by hand"
}

// once returns commands with each kept the first time it is named.
func once(commands []project.Command) []project.Command {
	var kept []project.Command
	seen := map[string]bool{}
	for _, command := range commands {
		if text := strings.Join(command, " "); !seen[text] {
			seen[text] = true
			kept = append(kept, command)
		}
	}
	return kept
}

// serviceKinds map a word in a service's name or its variables to the kind
// a record files it under. A service none of them names is external.
var serviceKinds = []struct{ word, kind string }{
	{"POSTGRES", "database"}, {"DATABASE", "database"}, {"MYSQL", "database"}, {"MONGO", "database"},
	{"REDIS", "redis"}, {"VALKEY", "redis"}, {"UPSTASH", "redis"},
	{"QDRANT", "vector-store"}, {"PINECONE", "vector-store"}, {"WEAVIATE", "vector-store"},
}

// serviceKind says what kind of service a declared service is.
func serviceKind(service auth.Service) string {
	text := strings.ToUpper(service.Name + " " + strings.Join(service.Env, " "))
	for _, known := range serviceKinds {
		if strings.Contains(text, known.word) {
			return known.kind
		}
	}
	return "external"
}
