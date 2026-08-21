package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Migrate moves a credential stored before namespacing into the project scope
// that now looks for it. Every credential written by an earlier build lives in
// the shared scope under a bare environment variable name, and a service that
// does not declare `shared` no longer reads from there, so without this a
// working credential simply stops resolving.
//
// It runs on the ordinary read path rather than as a one-off command, so a
// goblin dispatched mid-migration still resolves its credentials, and it
// leaves the bare value in place: nothing is removed until nothing references
// it.
//
// A bare value is only claimed when it can be attributed. `DATABASE_URL` is
// declared by more than one project, and a bare one cannot say whose database
// it names, so it is left where it is and the resolution report already prints
// the `cfo auth copy` command that would claim it deliberately. Guessing there
// is exactly the failure this package's namespacing was built to end.
func Migrate(store Store, dataDir, project string, manifest Manifest) ([]Adopted, error) {
	if store == nil {
		return nil, fmt.Errorf("auth: no credential store configured")
	}
	scope := ProjectName(project)
	if scope == "" {
		scope = manifest.Project
	}
	if !ValidProjectName(scope) {
		return nil, fmt.Errorf("auth: %q cannot be a credential scope", scope)
	}
	wanted := wantedNames(store, scope, manifest)
	if len(wanted) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(wanted))
	for name := range wanted {
		names = append(names, name)
	}
	sort.Strings(names)

	var migrated []Adopted
	for _, name := range names {
		value, found, err := store.Get(Shared(name))
		if err != nil {
			return migrated, err
		}
		if !found || value == "" {
			continue
		}
		if owners := DeclaringProjects(dataDir, name); len(owners) != 1 || owners[0] != scope {
			continue
		}
		key := Scoped(scope, name)
		if err := store.Set(key, value); err != nil {
			return migrated, err
		}
		migrated = append(migrated, Adopted{Name: name, Key: key, Origin: "store/shared (stored before namespacing)"})
	}
	return migrated, nil
}

// DeclaringProjects lists every project whose manifest declares a credential
// name, sorted. It is what decides whether a bare stored value can be
// attributed to one project: a name exactly one manifest declares has one
// possible owner, and a name two manifests declare has none that can be
// established without asking.
//
// A manifest that no longer loads is skipped rather than reported, because the
// question here is only which projects can be shown to claim a name, and one
// broken sibling must not stall a dispatch into an unrelated project.
func DeclaringProjects(dataDir, name string) []string {
	if dataDir == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, ManifestDirName))
	if err != nil {
		return nil
	}
	var projects []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifest, err := LoadManifest(dataDir, entry.Name())
		if err != nil {
			continue
		}
		for _, declared := range manifest.EnvNames() {
			if declared == name {
				projects = append(projects, entry.Name())
				break
			}
		}
	}
	sort.Strings(projects)
	return projects
}
