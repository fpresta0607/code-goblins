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
	// gateTest is the gate's test command, when it can run as written.
	gateTest project.Command
	// services are the declared services the repository reads.
	services []auth.Service
}

// draft returns the project's record as this assessment can vouch for it.
// It starts from the record that is there, so nothing a person set is
// drafted away, and fills only what that leaves empty: the services that
// are declared and read, and the gate's test command as the fast tier. A
// command an instruction file names is prose until someone runs it, so the
// full tier is left to whoever proves one, and deployment is never drafted,
// since a wrong deploy command is one `cfo deploy` would run.
func (c *checker) draft() project.Manifest {
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
	verification := &manifest.Verification
	if len(verification.Fast) == 0 && len(c.proven.gateTest) > 0 {
		verification.Fast = []project.Command{c.proven.gateTest}
	}
	return manifest
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
