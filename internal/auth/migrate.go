package auth

import (
	"errors"
	"fmt"
	"io/fs"
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
// is exactly the failure this package's namespacing was built to end. For the
// same reason it claims nothing at all while any manifest in the data
// directory fails to load: a sibling that cannot be read cannot be shown not
// to declare the name, and counting it as absent is how a shared name looks
// single-owner. The dispatch still proceeds; only the migration declines.
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
		owners, unreadable := DeclaringProjects(dataDir, name)
		// A manifest that does not load cannot be shown not to declare this
		// name, and "attribution could not be established" is not "no other
		// owner". Claim nothing at all until every sibling can be read, so a
		// stray key in one manifest can never make a shared name look
		// single-owner. The dispatch itself still proceeds.
		if len(unreadable) > 0 {
			return migrated, nil
		}
		if len(owners) != 1 || owners[0] != scope {
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

// DeclaringProjects lists every project whose manifest consumes a credential
// name, sorted, alongside every project whose manifest could not be read. It
// is what decides whether a bare stored value can be attributed to one
// project: a name exactly one manifest consumes has one possible owner, and a
// name two manifests consume has none that can be established without asking.
//
// Consumption is the manifest's whole read surface, not only its declared
// names: Resolver.lookup consults a declared alias against the shared scope
// too, so a project that reads a bare name through an alias is an owner.
//
// A manifest that no longer loads is reported rather than skipped. It cannot
// be shown not to claim the name, and silently dropping it from the count is
// how a shared name looks single-owner. Reporting is not erroring: one broken
// sibling still must not stall a dispatch into an unrelated project, so the
// caller declines to migrate rather than failing.
func DeclaringProjects(dataDir, name string) (projects []string, unreadable []string) {
	if dataDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, ManifestDirName))
	if err != nil {
		return nil, nil
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifest, err := LoadManifest(dataDir, entry.Name())
		// A project directory with no manifest declares nothing, which is an
		// established fact rather than a failure to establish one.
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			unreadable = append(unreadable, entry.Name())
			continue
		}
		if _, consumed := manifest.CredentialChains()[name]; consumed {
			projects = append(projects, entry.Name())
		}
	}
	sort.Strings(projects)
	sort.Strings(unreadable)
	return projects, unreadable
}
