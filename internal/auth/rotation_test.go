package auth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// TestARotatedEnvValueReachesTheNextPreflight is the defect this file exists
// for: adoption used to return early whenever the store already held a value,
// so a credential the Overlord rotated in a project's .env never reached the
// store and every goblin dispatched afterwards carried the dead one.
func TestARotatedEnvValueReachesTheNextPreflight(t *testing.T) {
	clearEnv(t, "STRIPE_SECRET_KEY")
	t.Setenv(StoreDirEnv, t.TempDir())
	dataDir := t.TempDir()
	writeManifest(t, dataDir, "clock-in", Manifest{
		Project:  "clock-in",
		Services: []Service{{Name: "stripe", Method: MethodEnv, Env: []string{"STRIPE_SECRET_KEY"}}},
	})
	project := filepath.Join(t.TempDir(), "clock-in")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(project, ".env")
	write := func(value string) {
		t.Helper()
		if err := os.WriteFile(envFile, []byte("STRIPE_SECRET_KEY="+value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	preflight := SpawnPreflight{DataDir: dataDir, Runner: gitIgnoresEverything()}

	write("sk_before_rotation")
	first, err := preflight.Preflight(context.Background(), project)
	if err != nil {
		t.Fatalf("first preflight: %v", err)
	}
	if first.Env["STRIPE_SECRET_KEY"] != "sk_before_rotation" {
		t.Fatalf("first preflight injected %q, want the adopted value", Redact(first.Env["STRIPE_SECRET_KEY"]))
	}

	write("sk_after_rotation")
	second, err := preflight.Preflight(context.Background(), project)
	if err != nil {
		t.Fatalf("second preflight: %v", err)
	}
	if second.Env["STRIPE_SECRET_KEY"] != "sk_after_rotation" {
		t.Errorf("second preflight injected %q, want the rotated value", Redact(second.Env["STRIPE_SECRET_KEY"]))
	}
	// The refresh has to be visible at dispatch, by name and origin, or a
	// credential changing under the fleet is indistinguishable from one that
	// did not.
	for _, want := range []string{"refreshed", "STRIPE_SECRET_KEY", envFile} {
		if !strings.Contains(second.Warning, want) {
			t.Errorf("warning %q does not report %q", second.Warning, want)
		}
	}
	if strings.Contains(second.Warning, "sk_after_rotation") || strings.Contains(second.Warning, "sk_before_rotation") {
		t.Error("the preflight warning printed a credential value")
	}
}

// TestAToolTokenNeverReplacesADeliberatelyStoredCredential holds the other
// half of the rule. A token gh happens to own is not the Overlord deciding
// anything about this project, so it may fill an empty slot and never
// overwrite a value somebody stored on purpose.
func TestAToolTokenNeverReplacesADeliberatelyStoredCredential(t *testing.T) {
	clearEnv(t, "GITHUB_TOKEN")
	store := newMemoryStore(map[string]string{"code-goblins/GITHUB_TOKEN": "ghp_deliberate"})
	runner := &fakeRunner{results: map[string]execx.Result{
		"git": {ExitCode: 0},
		"gh":  {Stdout: []byte("ghp_whatever_gh_holds_today\n")},
	}}
	manifest := Manifest{Services: []Service{{Name: "github", Method: MethodCLI, Env: []string{"GITHUB_TOKEN"}}}}

	adopted, err := Discover(context.Background(), store, runner, manifest, filepath.Join(t.TempDir(), "code-goblins"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if store.values["code-goblins/GITHUB_TOKEN"] != "ghp_deliberate" {
		t.Errorf("GITHUB_TOKEN = %q, want the deliberately stored value kept", Redact(store.values["code-goblins/GITHUB_TOKEN"]))
	}
	if len(adopted) != 0 {
		t.Errorf("adopted %+v, want nothing reported", adopted)
	}
}

func TestAManifestWithAnUnknownFieldFailsToLoad(t *testing.T) {
	dataDir := t.TempDir()
	path := ManifestPath(dataDir, "clock-in")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// `shared` spent two manifests doing nothing because there was no field
	// to receive it. A misspelling is the same failure with a worse disguise.
	body := `{"project":"clock-in","services":[{"name":"github","method":"cli","env":["GITHUB_TOKEN"],"sharedd":true}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadManifest(dataDir, "clock-in")
	if err == nil {
		t.Fatal("LoadManifest accepted a manifest with an unknown field")
	}
	for _, want := range []string{"unknown field", "sharedd"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestAManifestThatOnlyUsesDeclaredFieldsStillLoads(t *testing.T) {
	dataDir := t.TempDir()
	path := ManifestPath(dataDir, "clock-in")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"project":"clock-in","services":[{"name":"github","method":"cli","env":["GITHUB_TOKEN"],"shared":true,` +
		`"probe":["gh","auth","status"],"url":"https://github.com/settings/tokens","note":"n","optional":false,` +
		`"aliases":{"GITHUB_TOKEN":["GH_TOKEN"]},"login":["gh","auth","login"],"confirm":["Authorize"],` +
		`"identity":{"var":"GITHUB_TOKEN","expect":"ghp","note":"n"}}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(dataDir, "clock-in")
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if !manifest.Services[0].Shared {
		t.Error("shared did not reach the service it was declared on")
	}
}

// TestABareCredentialMigratesIntoTheOnlyProjectThatDeclaresIt covers the
// state every existing store is in: one flat slot per name, written before
// there were scopes. A goblin dispatched mid-migration still resolves, which
// is why this happens on the read path rather than as a one-off command.
func TestABareCredentialMigratesIntoTheOnlyProjectThatDeclaresIt(t *testing.T) {
	clearEnv(t, "OPENAI_API_KEY")
	dataDir := t.TempDir()
	manifest := Manifest{
		Project:  "precisiondocs",
		Services: []Service{{Name: "openai", Method: MethodEnv, Env: []string{"OPENAI_API_KEY"}}},
	}
	writeManifest(t, dataDir, "precisiondocs", manifest)
	writeManifest(t, dataDir, "clock-in", Manifest{
		Project:  "clock-in",
		Services: []Service{{Name: "github", Method: MethodCLI, Env: []string{"GITHUB_TOKEN"}, Shared: true}},
	})
	store := newMemoryStore(map[string]string{"OPENAI_API_KEY": "sk_stored_before_scopes"})

	migrated, _, err := Migrate(store, dataDir, "precisiondocs", manifest)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if store.values["precisiondocs/OPENAI_API_KEY"] != "sk_stored_before_scopes" {
		t.Errorf("OPENAI_API_KEY = %q, want it landed in the project scope", Redact(store.values["precisiondocs/OPENAI_API_KEY"]))
	}
	// The bare value stays until nothing references it, so anything still
	// reading the old key keeps working through the migration.
	if store.values["OPENAI_API_KEY"] != "sk_stored_before_scopes" {
		t.Error("migration removed the bare credential instead of leaving it in place")
	}
	if len(migrated) != 1 || migrated[0].Key.String() != "precisiondocs/OPENAI_API_KEY" {
		t.Fatalf("migrated = %+v, want one namespaced key reported", migrated)
	}
	if strings.Contains(migrated[0].Origin, "sk_stored_before_scopes") {
		t.Error("the migration origin printed the credential value")
	}

	// It resolves through the ordinary path afterwards, which is the whole
	// point: an existing credential still works and is now namespaced.
	resolution, err := Resolver{Store: store, Project: "precisiondocs"}.Resolve(manifest.Services[0])
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolution.Values["OPENAI_API_KEY"].From != "store/precisiondocs:OPENAI_API_KEY" {
		t.Errorf("resolved from %q, want the project scope", resolution.Values["OPENAI_API_KEY"].From)
	}

	// Running it again changes nothing: the name resolves, so there is
	// nothing left to migrate.
	again, _, err := Migrate(store, dataDir, "precisiondocs", manifest)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second migration reported %+v, want it to be a no-op", again)
	}
}

// TestABareCredentialTwoProjectsDeclareIsLeftWhereItIs is the case that must
// never be guessed. A bare DATABASE_URL cannot say whose database it names,
// and handing it to whichever project preflights first is exactly the
// incident namespacing was built to end.
func TestABareCredentialTwoProjectsDeclareIsLeftWhereItIs(t *testing.T) {
	clearEnv(t, "DATABASE_URL")
	dataDir := t.TempDir()
	postgres := Service{Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"}}
	manifest := Manifest{Project: "clock-in", Services: []Service{postgres}}
	writeManifest(t, dataDir, "clock-in", manifest)
	writeManifest(t, dataDir, "precisiondocs", Manifest{Project: "precisiondocs", Services: []Service{postgres}})
	store := newMemoryStore(map[string]string{"DATABASE_URL": "postgres://somebody/appdb"})

	migrated, _, err := Migrate(store, dataDir, "clock-in", manifest)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(migrated) != 0 {
		t.Fatalf("migrated %+v, want an unattributable value left alone", migrated)
	}
	if _, claimed := store.values["clock-in/DATABASE_URL"]; claimed {
		t.Error("claimed a bare credential that two projects declare")
	}
	// The operator is not left guessing: the resolution chain already names
	// the value it declined and the command that would claim it deliberately.
	resolution, err := Resolver{Store: store, Project: "clock-in"}.Resolve(postgres)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var note string
	for _, candidate := range resolution.Chains["DATABASE_URL"] {
		if candidate.Note != "" {
			note = candidate.Note
		}
	}
	if !strings.Contains(note, "cfo auth copy") {
		t.Errorf("chain note = %q, want the copy command that claims it", note)
	}
}

func TestDeclaringProjectsCountsEveryManifestThatClaimsAName(t *testing.T) {
	dataDir := t.TempDir()
	postgres := Service{Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"}}
	writeManifest(t, dataDir, "clock-in", Manifest{Project: "clock-in", Services: []Service{postgres}})
	writeManifest(t, dataDir, "precisiondocs", Manifest{Project: "precisiondocs", Services: []Service{postgres}})
	writeManifest(t, dataDir, "homescout", Manifest{
		Project:  "homescout",
		Services: []Service{{Name: "github", Method: MethodCLI, Env: []string{"GITHUB_TOKEN"}, Shared: true}},
	})
	// A manifest that no longer loads cannot be shown not to claim a name, so
	// it is reported rather than dropped from the count.
	broken := ManifestPath(dataDir, "prometheus")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, _ := DeclaringProjects(dataDir, "DATABASE_URL"); len(got) != 2 || got[0] != "clock-in" || got[1] != "precisiondocs" {
		t.Errorf("DeclaringProjects(DATABASE_URL) = %v, want both declaring projects sorted", got)
	}
	if got, _ := DeclaringProjects(dataDir, "GITHUB_TOKEN"); len(got) != 1 || got[0] != "homescout" {
		t.Errorf("DeclaringProjects(GITHUB_TOKEN) = %v, want the one project that declares it", got)
	}
	if got, _ := DeclaringProjects(dataDir, "NOBODY_DECLARES_THIS"); len(got) != 0 {
		t.Errorf("DeclaringProjects = %v, want nothing", got)
	}
	if _, unreadable := DeclaringProjects(dataDir, "DATABASE_URL"); len(unreadable) != 1 || unreadable[0] != ManifestPath(dataDir, "prometheus") {
		t.Errorf("unreadable = %v, want the path of the manifest that could not be read", unreadable)
	}
}

// TestABareCredentialAnotherProjectReadsThroughAnAliasIsLeftWhereItIs is the
// half of attribution that counting declared names alone misses. A project
// that declares PG_URL and aliases it to DATABASE_URL reads the bare
// DATABASE_URL from the shared scope, so it is an owner, and claiming the
// value for the other project bakes in exactly the cross-project database the
// namespacing was built to end.
func TestABareCredentialAnotherProjectReadsThroughAnAliasIsLeftWhereItIs(t *testing.T) {
	clearEnv(t, "DATABASE_URL", "PG_URL")
	dataDir := t.TempDir()
	manifest := Manifest{
		Project:  "clock-in",
		Services: []Service{{Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"}}},
	}
	writeManifest(t, dataDir, "clock-in", manifest)
	writeManifest(t, dataDir, "precisiondocs", Manifest{
		Project: "precisiondocs",
		Services: []Service{{
			Name: "postgres", Method: MethodEnv, Env: []string{"PG_URL"}, Shared: true,
			Aliases: map[string][]string{"PG_URL": {"DATABASE_URL"}},
		}},
	})
	store := newMemoryStore(map[string]string{"DATABASE_URL": "postgres://somebody/appdb"})

	if owners, _ := DeclaringProjects(dataDir, "DATABASE_URL"); len(owners) != 2 {
		t.Errorf("DeclaringProjects(DATABASE_URL) = %v, want the alias reader counted as an owner", owners)
	}
	migrated, _, err := Migrate(store, dataDir, "clock-in", manifest)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(migrated) != 0 {
		t.Errorf("migrated %+v, want a value another project reads through an alias left alone", migrated)
	}
	if _, claimed := store.values["clock-in/DATABASE_URL"]; claimed {
		t.Error("claimed a bare credential another project reads through a declared alias")
	}
}

// TestASiblingManifestThatWillNotLoadBlocksMigration holds the other half:
// DisallowUnknownFields makes an unreadable sibling far more likely, and
// "attribution could not be established" is not "no other owner". The
// dispatch still proceeds - only the claim declines.
func TestASiblingManifestThatWillNotLoadBlocksMigration(t *testing.T) {
	clearEnv(t, "OPENAI_API_KEY")
	dataDir := t.TempDir()
	manifest := Manifest{
		Project:  "precisiondocs",
		Services: []Service{{Name: "openai", Method: MethodEnv, Env: []string{"OPENAI_API_KEY"}}},
	}
	writeManifest(t, dataDir, "precisiondocs", manifest)
	broken := ManifestPath(dataDir, "clock-in")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	// A single stray key is all it takes now, and this manifest may well
	// declare OPENAI_API_KEY too - nothing here can tell.
	body := `{"project":"clock-in","services":[{"name":"openai","method":"env","env":["OPENAI_API_KEY"],"sharedd":true}]}`
	if err := os.WriteFile(broken, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore(map[string]string{"OPENAI_API_KEY": "sk_stored_before_scopes"})

	migrated, _, err := Migrate(store, dataDir, "precisiondocs", manifest)
	if err != nil {
		t.Fatalf("Migrate returned an error, which would stall a dispatch into an unrelated project: %v", err)
	}
	if len(migrated) != 0 {
		t.Errorf("migrated %+v, want nothing claimed while a sibling manifest cannot be read", migrated)
	}
	if _, claimed := store.values["precisiondocs/OPENAI_API_KEY"]; claimed {
		t.Error("claimed a bare credential while a sibling manifest could not be read")
	}
}

// TestAPausedMigrationSaysWhichManifestStoppedIt is why the decline is
// reported rather than only performed. Nothing else in the fleet ever reports
// that a manifest fails to load, so a silent decline leaves the operator with
// a credential that will not resolve and a resolution chain whose stated
// reason is not the real one.
func TestAPausedMigrationSaysWhichManifestStoppedIt(t *testing.T) {
	clearEnv(t, "OPENAI_API_KEY")
	t.Setenv(StoreDirEnv, t.TempDir())
	dataDir := t.TempDir()
	writeManifest(t, dataDir, "precisiondocs", Manifest{
		Project:  "precisiondocs",
		Services: []Service{{Name: "openai", Method: MethodEnv, Env: []string{"OPENAI_API_KEY"}}},
	})
	broken := ManifestPath(dataDir, "clock-in")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"project":"clock-in","services":[{"name":"openai","method":"env","env":["OPENAI_API_KEY"],"sharedd":true}]}`
	if err := os.WriteFile(broken, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "precisiondocs")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := SpawnPreflight{DataDir: dataDir, Runner: gitIgnoresEverything()}.
		Preflight(context.Background(), project)
	if err != nil {
		t.Fatalf("a broken sibling manifest stalled a dispatch into an unrelated project: %v", err)
	}
	_, paused, found := strings.Cut(result.Warning, "migration paused")
	if !found {
		t.Fatalf("warning %q does not report that migration declined", result.Warning)
	}
	if !strings.Contains(paused, broken) {
		t.Errorf("paused line %q does not name the manifest that has to be fixed", paused)
	}
	// The blocking service's own remedy names the credential; the paused line
	// must not, because it is about a manifest and nothing else.
	if strings.Contains(paused, "OPENAI_API_KEY") {
		t.Errorf("paused line %q named a credential rather than only the manifest", paused)
	}
}

// TestAGoblinWorktreeEnvIsNeverAnOrigin is the boundary that keeps the
// refresh rule honest. Only the Overlord's own file may rotate a stored
// value, and a goblin writes .env files inside <project>/.worktrees/<id>
// while git ignores that whole tree - so without pruning it, an agent
// bootstrapping a local database would overwrite the fleet's credential.
func TestAGoblinWorktreeEnvIsNeverAnOrigin(t *testing.T) {
	clearEnv(t, "DATABASE_URL")
	manifest := Manifest{
		Project:  "precisiondocs",
		Services: []Service{{Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"}}},
	}
	project := filepath.Join(t.TempDir(), "precisiondocs")
	goblin := filepath.Join(project, ".worktrees", "gb-1")
	if err := os.MkdirAll(goblin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goblin, ".env"),
		[]byte("DATABASE_URL=postgres://localhost:5432/dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore(map[string]string{"precisiondocs/DATABASE_URL": "postgres://prod/appdb"})

	adopted, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if store.values["precisiondocs/DATABASE_URL"] != "postgres://prod/appdb" {
		t.Errorf("DATABASE_URL = %q, want the Overlord's stored value untouched by a goblin's worktree",
			Redact(store.values["precisiondocs/DATABASE_URL"]))
	}
	if len(adopted) != 0 {
		t.Errorf("adopted %+v, want a goblin's worktree to be no origin at all", adopted)
	}

	// An empty slot is not a loophole either: a goblin's file must not fill
	// one any more than it may replace a value.
	empty := newMemoryStore(nil)
	if _, err := Discover(context.Background(), empty, gitIgnoresEverything(), manifest, project); err != nil {
		t.Fatalf("Discover into an empty store: %v", err)
	}
	if _, claimed := empty.values["precisiondocs/DATABASE_URL"]; claimed {
		t.Error("adopted a credential from a goblin's own worktree")
	}
}

// TestARotatedEnvValueReachesACredentialStoredUnderAnAlias covers the sibling
// path of the rotation defect. A project whose credential lives under a
// declared alias key must pick up a rotated .env value the same way a
// declared one does, or the refresh silently does not fire for it.
func TestARotatedEnvValueReachesACredentialStoredUnderAnAlias(t *testing.T) {
	clearEnv(t, "DATABASE_URL", "PG_URL")
	postgres := Service{
		Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"},
		Aliases: map[string][]string{"DATABASE_URL": {"PG_URL"}},
	}
	manifest := Manifest{Project: "precisiondocs", Services: []Service{postgres}}
	project := filepath.Join(t.TempDir(), "precisiondocs")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(project, ".env")
	if err := os.WriteFile(envFile, []byte("PG_URL=postgres://host/after_rotation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore(map[string]string{"precisiondocs/PG_URL": "postgres://host/before_rotation"})

	adopted, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if store.values["precisiondocs/PG_URL"] != "postgres://host/after_rotation" {
		t.Errorf("PG_URL = %q, want the rotated value on the key that answers the name",
			Redact(store.values["precisiondocs/PG_URL"]))
	}
	// The declared name must not gain a second copy that would then drift
	// from the alias key resolution actually reaches.
	if _, shadowed := store.values["precisiondocs/DATABASE_URL"]; shadowed {
		t.Error("wrote a second copy under the declared name instead of refreshing the key that holds the value")
	}
	if len(adopted) != 1 || !adopted[0].Refreshed || adopted[0].Key.String() != "precisiondocs/PG_URL" {
		t.Fatalf("adopted = %+v, want the alias key reported as refreshed", adopted)
	}
	// The rotated value resolves through the ordinary path afterwards, which
	// is the whole point.
	resolution, err := Resolver{Store: store, Project: "precisiondocs"}.Resolve(postgres)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolution.Values["DATABASE_URL"].Value != "postgres://host/after_rotation" {
		t.Error("the resolved credential is still the value the .env replaced")
	}
}

// TestARotatedEnvValueReachesTheAliasKeyWhenTheFileCarriesTheDeclaredName is
// the same defect from the other side: the .env carries the declared name
// while the store holds the value under the alias, so the key resolution
// actually reaches is the one that has to be rewritten.
func TestARotatedEnvValueReachesTheAliasKeyWhenTheFileCarriesTheDeclaredName(t *testing.T) {
	clearEnv(t, "DATABASE_URL", "PG_URL")
	postgres := Service{
		Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"},
		Aliases: map[string][]string{"DATABASE_URL": {"PG_URL"}},
	}
	manifest := Manifest{Project: "precisiondocs", Services: []Service{postgres}}
	project := filepath.Join(t.TempDir(), "precisiondocs")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".env"),
		[]byte("DATABASE_URL=postgres://host/after_rotation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore(map[string]string{"precisiondocs/PG_URL": "postgres://host/before_rotation"})

	if _, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if store.values["precisiondocs/PG_URL"] != "postgres://host/after_rotation" {
		t.Errorf("PG_URL = %q, want the rotated value on the key that answers DATABASE_URL",
			Redact(store.values["precisiondocs/PG_URL"]))
	}
}

func TestCacheEnvPinsOneSharedStorePerEcosystem(t *testing.T) {
	for _, name := range []string{"UV_CACHE_DIR", "npm_config_store_dir", "PLAYWRIGHT_BROWSERS_PATH", "GOMODCACHE", "CARGO_HOME"} {
		clearEnv(t, name)
	}
	home := t.TempDir()
	env := CacheEnv(home)
	root := filepath.Join(home, CacheDirName)
	want := map[string]string{
		"UV_CACHE_DIR":             filepath.Join(root, "uv"),
		"npm_config_store_dir":     filepath.Join(root, "pnpm"),
		"PLAYWRIGHT_BROWSERS_PATH": filepath.Join(root, "playwright"),
		"GOMODCACHE":               filepath.Join(root, "go-mod"),
	}
	// CARGO_HOME is not a cache redirect: it relocates config.toml,
	// credentials.toml and bin/ too, so a goblin would lose the operator's
	// registry and linker configuration and fail a private fetch as an auth
	// error far from its cause.
	if _, redirected := env["CARGO_HOME"]; redirected {
		t.Error("redirected CARGO_HOME, which relocates cargo's whole home rather than a cache")
	}
	for name, path := range want {
		if env[name] != path {
			t.Errorf("%s = %q, want %q", name, env[name], path)
		}
	}
	if len(env) != len(want) {
		t.Errorf("CacheEnv = %v, want exactly the declared redirects", env)
	}
	// Every name has to survive the pane shell, or a redirect breaks the
	// launch prefix instead of sharing a cache.
	for name := range env {
		if !ValidEnvName(name) {
			t.Errorf("%q is not exportable into a pane shell", name)
		}
	}
	// Nothing is created: an ecosystem that never runs on this machine must
	// not leave an empty directory behind.
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("CacheEnv created %s", root)
	}
}

func TestCacheEnvLeavesALocationTheOperatorAlreadyChose(t *testing.T) {
	clearEnv(t, "UV_CACHE_DIR", "npm_config_store_dir", "PLAYWRIGHT_BROWSERS_PATH", "GOMODCACHE", "CARGO_HOME")
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", filepath.Join(t.TempDir(), "ms-playwright"))
	env := CacheEnv(t.TempDir())
	if _, redirected := env["PLAYWRIGHT_BROWSERS_PATH"]; redirected {
		t.Error("overrode a cache location the operator had already set, stranding whatever is warm there")
	}
	if env["UV_CACHE_DIR"] == "" {
		t.Error("one inherited variable suppressed the rest")
	}
}

func TestCacheEnvIsEmptyWithoutACFOHome(t *testing.T) {
	if env := CacheEnv(""); len(env) != 0 {
		t.Errorf("CacheEnv with no home = %v, want nothing rather than redirects rooted at the working directory", env)
	}
}

func TestAdoptionLineNamesWhatChangedAndNeverAValue(t *testing.T) {
	line := AdoptionLine([]Adopted{
		{Name: "STRIPE_SECRET_KEY", Key: Scoped("clock-in", "STRIPE_SECRET_KEY"), Origin: "/p/clock-in/.env", Refreshed: true},
		{Name: "GITHUB_TOKEN", Key: Shared("GITHUB_TOKEN"), Origin: "gh auth token"},
	})
	for _, want := range []string{"refreshed 1", "STRIPE_SECRET_KEY from /p/clock-in/.env", "adopted 1", "GITHUB_TOKEN from gh auth token"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q does not report %q", line, want)
		}
	}
	if AdoptionLine(nil) != "" {
		t.Error("a preflight that changed nothing still printed an adoption line")
	}
}
