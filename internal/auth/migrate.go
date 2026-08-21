package auth

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
func Migrate(store Store, dataDir, project string, manifest Manifest) ([]Adopted, []string, error) {
	if store == nil {
		return nil, nil, fmt.Errorf("auth: no credential store configured")
	}
	scope := ProjectName(project)
	if scope == "" {
		scope = manifest.Project
	}
	if !ValidProjectName(scope) {
		return nil, nil, fmt.Errorf("auth: %q cannot be a credential scope", scope)
	}
	wanted := wantedNames(store, scope, manifest)
	if len(wanted) == 0 {
		return nil, nil, nil
	}
	names := make([]string, 0, len(wanted))
	for name := range wanted {
		names = append(names, name)
	}
	sort.Strings(names)

	// One pass over the data directory answers every name, and whether
	// attribution is available at all does not depend on the name: a manifest
	// that does not load cannot be shown not to declare any of them.
	index := loadManifestIndex(dataDir)
	if len(index.unreadable) > 0 {
		return nil, index.unreadable, nil
	}

	var migrated []Adopted
	for _, name := range names {
		value, found, err := store.Get(Shared(name))
		if err != nil {
			return migrated, nil, err
		}
		if !found || value == "" {
			continue
		}
		if owners := index.owners[name]; len(owners) != 1 || owners[0] != scope {
			continue
		}
		key := Scoped(scope, name)
		if err := store.Set(key, value); err != nil {
			return migrated, nil, err
		}
		migrated = append(migrated, Adopted{Name: name, Key: key, Origin: "store/shared (stored before namespacing)"})
	}
	return migrated, nil, nil
}

// MigrationPausedLine reports the manifests that could not be read and what
// follows from it: attribution is unavailable, so nothing stored before
// namespacing is claimed until they load. Without it the decline is invisible
// and the operator sees only a service that will not resolve, with the
// resolution chain offering a reason that is not the real one.
//
// It names manifest paths. No credential name and no value appears here, the
// same rule the adoption line keeps.
func MigrationPausedLine(unreadable []string) string {
	if len(unreadable) == 0 {
		return ""
	}
	return fmt.Sprintf("migration paused: %d manifest(s) could not be read (%s); credentials stored before namespacing stay put until they load",
		len(unreadable), strings.Join(unreadable, ", "))
}

// manifestIndex is one pass over the data directory: which projects consume
// each credential name, and the path of every manifest that could not be
// read. Both answers come from the same scan because both are properties of
// the directory rather than of any one name, and Migrate holds the per-home
// spawn lock while it asks.
//
// It is what decides whether a bare stored value can be attributed to one
// project: a name exactly one manifest consumes has one possible owner, and a
// name two manifests consume has none that can be established without asking.
// Consumption is the manifest's whole read surface, not only its declared
// names, because Resolver.lookup consults a declared alias against the shared
// scope too. A manifest that will not load is recorded rather than skipped:
// it cannot be shown not to claim a name, and dropping it from the count is
// how a shared name looks single-owner.
type manifestIndex struct {
	owners     map[string][]string
	unreadable []string
}

func loadManifestIndex(dataDir string) manifestIndex {
	index := manifestIndex{owners: map[string][]string{}}
	if dataDir == "" {
		return index
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, ManifestDirName))
	if err != nil {
		return index
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
			index.unreadable = append(index.unreadable, ManifestPath(dataDir, entry.Name()))
			continue
		}
		for name := range manifest.CredentialChains() {
			index.owners[name] = append(index.owners[name], entry.Name())
		}
	}
	for name := range index.owners {
		sort.Strings(index.owners[name])
	}
	sort.Strings(index.unreadable)
	return index
}
