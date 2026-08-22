package auth

import (
	"context"
	"errors"
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

	adopted, _, err := Discover(context.Background(), store, runner, manifest, filepath.Join(t.TempDir(), "code-goblins"))
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

// TestMigrationClaimsOnlyWhatOneManifestConsumes drives attribution through
// the entry point production uses. A name one project consumes is claimed, a
// name two consume is not, and a manifest that will not load pauses the whole
// migration by path.
func TestMigrationClaimsOnlyWhatOneManifestConsumes(t *testing.T) {
	clearEnv(t, "DATABASE_URL", "GITHUB_TOKEN")
	dataDir := t.TempDir()
	postgres := Service{Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"}}
	github := Service{Name: "github", Method: MethodCLI, Env: []string{"GITHUB_TOKEN"}}
	clockIn := Manifest{Project: "clock-in", Services: []Service{postgres, github}}
	writeManifest(t, dataDir, "clock-in", clockIn)
	writeManifest(t, dataDir, "precisiondocs", Manifest{Project: "precisiondocs", Services: []Service{postgres}})
	store := newMemoryStore(map[string]string{
		"DATABASE_URL": "postgres://somebody/appdb",
		"GITHUB_TOKEN": "ghp_stored_before_scopes",
	})

	migrated, unreadable, err := Migrate(store, dataDir, "clock-in", clockIn)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(unreadable) != 0 {
		t.Errorf("unreadable = %v, want every manifest readable", unreadable)
	}
	if len(migrated) != 1 || migrated[0].Key.String() != "clock-in/GITHUB_TOKEN" {
		t.Fatalf("migrated = %+v, want only the name one manifest consumes", migrated)
	}
	if _, claimed := store.values["clock-in/DATABASE_URL"]; claimed {
		t.Error("claimed a bare credential two manifests consume")
	}

	// A manifest that no longer loads cannot be shown not to claim a name, so
	// the migration declines by path rather than dropping it from the count.
	broken := ManifestPath(dataDir, "prometheus")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := newMemoryStore(map[string]string{"GITHUB_TOKEN": "ghp_stored_before_scopes"})
	migrated, unreadable, err = Migrate(fresh, dataDir, "clock-in", clockIn)
	if err != nil {
		t.Fatalf("a broken sibling manifest stalled the dispatch: %v", err)
	}
	if len(migrated) != 0 {
		t.Errorf("migrated %+v, want nothing claimed while a manifest cannot be read", migrated)
	}
	if len(unreadable) != 1 || unreadable[0] != broken {
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

// TestTheLocalEnvFileOwnsANameOverTheSharedOne pins dotenv's own layering.
// A project keeps dev defaults in .env and the real value in .env.local - the
// file its app loads last - so .env.local has to be the file that owns the
// name. Lexical order put .env first, which made a dev default overwrite a
// deliberately stored credential on every dispatch, with no way to pin an
// override because the next dispatch clobbered it again.
func TestTheLocalEnvFileOwnsANameOverTheSharedOne(t *testing.T) {
	clearEnv(t, "DATABASE_URL")
	manifest := Manifest{
		Project:  "precisiondocs",
		Services: []Service{{Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"}}},
	}
	project := filepath.Join(t.TempDir(), "precisiondocs")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		".env":       "DATABASE_URL=postgres://localhost/dev\n",
		".env.local": "DATABASE_URL=postgres://prod/appdb\n",
	} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// As a first adoption: the empty slot is filled from the override file.
	empty := newMemoryStore(nil)
	adopted, _, err := Discover(context.Background(), empty, gitIgnoresEverything(), manifest, project)
	if err != nil {
		t.Fatalf("Discover into an empty store: %v", err)
	}
	if empty.values["precisiondocs/DATABASE_URL"] != "postgres://prod/appdb" {
		t.Errorf("adopted %q, want the value from .env.local",
			Redact(empty.values["precisiondocs/DATABASE_URL"]))
	}
	if len(adopted) != 1 || filepath.Base(adopted[0].Origin) != ".env.local" {
		t.Errorf("adopted = %+v, want .env.local reported as the origin", adopted)
	}

	// As a refresh over a stored value: the dev default must not win, or the
	// stored credential is destroyed on every dispatch.
	stored := newMemoryStore(map[string]string{"precisiondocs/DATABASE_URL": "postgres://prod/was-rotated"})
	if _, _, err := Discover(context.Background(), stored, gitIgnoresEverything(), manifest, project); err != nil {
		t.Fatalf("Discover over a stored value: %v", err)
	}
	if stored.values["precisiondocs/DATABASE_URL"] != "postgres://prod/appdb" {
		t.Errorf("refreshed to %q, want the .env.local value rather than the .env dev default",
			Redact(stored.values["precisiondocs/DATABASE_URL"]))
	}
}

// TestARotatedAliasLineReachesTheDeclaredKey is the direction the round-2 fix
// left open. The store holds the credential under the declared name, the
// project's .env carries the alias the app itself uses, and a chain of the
// alias alone looks at a key that does not exist - so the rotation never
// lands and every goblin keeps the dead value.
func TestARotatedAliasLineReachesTheDeclaredKey(t *testing.T) {
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
		[]byte("PG_URL=postgres://host/after_rotation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore(map[string]string{"precisiondocs/DATABASE_URL": "postgres://host/before_rotation"})

	adopted, _, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if store.values["precisiondocs/DATABASE_URL"] != "postgres://host/after_rotation" {
		t.Errorf("DATABASE_URL = %q, want the rotated value on the key that answers the credential",
			Redact(store.values["precisiondocs/DATABASE_URL"]))
	}
	// Still exactly one key: an alias line rotates the credential, it does not
	// create a second copy that would then drift.
	if _, second := store.values["precisiondocs/PG_URL"]; second {
		t.Error("wrote a second key under the alias instead of refreshing the one that answers")
	}
	if len(adopted) != 1 || !adopted[0].Refreshed || adopted[0].Key.String() != "precisiondocs/DATABASE_URL" {
		t.Fatalf("adopted = %+v, want the declared key reported as refreshed", adopted)
	}

	resolution, err := Resolver{Store: store, Project: "precisiondocs"}.Resolve(postgres)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolution.Values["DATABASE_URL"].Value != "postgres://host/after_rotation" {
		t.Error("the resolved credential is still the value the .env replaced")
	}
}

// TestASecondServicesAliasStillReachesTheLiveKey covers a name two services
// declare with different alias lists. Refresh is handed a name off a .env
// line and cannot know which service wrote it, so a chain built from only the
// first declaration leaves the second service's alias pointing at a chain it
// is absent from, and the rotation lands nowhere.
func TestASecondServicesAliasStillReachesTheLiveKey(t *testing.T) {
	clearEnv(t, "DATABASE_URL", "PG_URL")
	manifest := Manifest{
		Project: "precisiondocs",
		Services: []Service{
			{Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"}},
			{
				Name: "analytics", Method: MethodEnv, Env: []string{"DATABASE_URL"},
				Aliases: map[string][]string{"DATABASE_URL": {"PG_URL"}},
			},
		},
	}
	project := filepath.Join(t.TempDir(), "precisiondocs")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".env"),
		[]byte("PG_URL=postgres://host/after_rotation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The key the analytics service actually reaches: the declared name is
	// unset in this project's scope, so the alias is what answers.
	store := newMemoryStore(map[string]string{"precisiondocs/PG_URL": "postgres://host/before_rotation"})

	adopted, _, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if store.values["precisiondocs/PG_URL"] != "postgres://host/after_rotation" {
		t.Errorf("PG_URL = %q, want the rotated value on the key that answers the credential",
			Redact(store.values["precisiondocs/PG_URL"]))
	}
	if len(adopted) != 1 || !adopted[0].Refreshed || adopted[0].Key.String() != "precisiondocs/PG_URL" {
		t.Fatalf("adopted = %+v, want the live key reported as refreshed", adopted)
	}
	// The invariant every round has kept: refresh rewrites a key that already
	// holds a value and never creates a second one, so a merged chain cannot
	// write a name into a key nothing resolves from.
	if _, second := store.values["precisiondocs/DATABASE_URL"]; second {
		t.Error("a merged chain created a key nothing resolved from")
	}
	resolution, err := Resolver{Store: store, Project: "precisiondocs"}.Resolve(manifest.Services[1])
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolution.Values["DATABASE_URL"].Value != "postgres://host/after_rotation" {
		t.Error("the resolved credential is still the value the .env replaced")
	}
}

// TestTheLocalFilesAliasBeatsTheSharedFilesDeclaredName pins the ordering
// from the other side: the file decides first, and the manifest's chain order
// only settles a tie inside one file. A dev default left in .env under the
// declared name must not outrank a rotation written to .env.local under an
// alias, or the stale value clobbers the real one on every dispatch and no
// override can be pinned.
func TestTheLocalFilesAliasBeatsTheSharedFilesDeclaredName(t *testing.T) {
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
	for name, body := range map[string]string{
		".env":       "DATABASE_URL=postgres://localhost/dev\n",
		".env.local": "PG_URL=postgres://prod/appdb\n",
	} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store := newMemoryStore(map[string]string{"precisiondocs/DATABASE_URL": "postgres://prod/before"})

	adopted, _, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if store.values["precisiondocs/DATABASE_URL"] != "postgres://prod/appdb" {
		t.Errorf("DATABASE_URL = %q, want the .env.local value rather than the .env dev default",
			Redact(store.values["precisiondocs/DATABASE_URL"]))
	}
	if len(adopted) != 1 {
		t.Fatalf("adopted = %+v, want one store key written once", adopted)
	}
	if filepath.Base(adopted[0].Origin) != ".env.local" {
		t.Errorf("adopted = %+v, want .env.local reported as the origin", adopted[0])
	}
	if adopted[0].Key.String() != "precisiondocs/DATABASE_URL" {
		t.Errorf("adopted = %+v, want the key that answers the credential", adopted[0])
	}
	if _, second := store.values["precisiondocs/PG_URL"]; second {
		t.Error("the alias line created a second key for one credential")
	}
}

// TestAWinningAliasLineAdoptsIntoTheDeclaredKey covers the empty-store half
// of the same rule. Collapsing a credential to one line means the winner can
// be an alias, and keying adoption off the line's own name would then adopt
// nothing at all: the credential is reported missing and the dispatch blocked
// while the value sits in the project's own .env.
//
// The value lands under the declared name, because an alias is a name a
// stored value may be found under rather than a credential of its own.
func TestAWinningAliasLineAdoptsIntoTheDeclaredKey(t *testing.T) {
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
	for name, body := range map[string]string{
		".env":       "DATABASE_URL=postgres://localhost/dev\n",
		".env.local": "PG_URL=postgres://prod/appdb\n",
	} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store := newMemoryStore(nil)

	adopted, _, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if store.values["precisiondocs/DATABASE_URL"] != "postgres://prod/appdb" {
		t.Errorf("DATABASE_URL = %q, want the .env.local value adopted under the declared name",
			Redact(store.values["precisiondocs/DATABASE_URL"]))
	}
	if _, second := store.values["precisiondocs/PG_URL"]; second {
		t.Error("adopted the alias into a key of its own instead of the declared one")
	}
	if len(adopted) != 1 {
		t.Fatalf("adopted = %+v, want the credential adopted exactly once", adopted)
	}
	if adopted[0].Refreshed {
		t.Errorf("adopted = %+v, want an empty slot filled rather than a value replaced", adopted[0])
	}
	if adopted[0].Name != "DATABASE_URL" || adopted[0].Key.String() != "precisiondocs/DATABASE_URL" {
		t.Errorf("adopted = %+v, want the credential reported by its declared name", adopted[0])
	}
	if filepath.Base(adopted[0].Origin) != ".env.local" {
		t.Errorf("adopted = %+v, want .env.local reported as the origin", adopted[0])
	}

	// It resolves through the ordinary path afterwards, which is the point:
	// the service is green rather than blocked on a name whose value was in
	// the project's own .env all along.
	resolution, err := Resolver{Store: store, Project: "precisiondocs"}.Resolve(postgres)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolution.Values["DATABASE_URL"].Value != "postgres://prod/appdb" {
		t.Errorf("resolved %q, want the adopted credential", Redact(resolution.Values["DATABASE_URL"].Value))
	}
}

// TestTheDeclaredLineBeatsTheAliasLineInOneFile covers the .env shape a
// managed Postgres hands out: a pooled DATABASE_URL and a direct alias, both
// carried in one file with different values by design. They answer one
// credential and target one store key, so without collapsing them the key is
// written twice in a run and alphabetical order picks the winner - which is
// not what the manifest says, and it repeats on every dispatch.
func TestTheDeclaredLineBeatsTheAliasLineInOneFile(t *testing.T) {
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
	// PG_URL sorts after DATABASE_URL, so a per-name loop would let it write
	// the same key second and win.
	if err := os.WriteFile(filepath.Join(project, ".env"),
		[]byte("DATABASE_URL=postgres://pooled/appdb\nPG_URL=postgres://direct/appdb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore(map[string]string{"precisiondocs/DATABASE_URL": "postgres://pooled/before"})

	adopted, _, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if store.values["precisiondocs/DATABASE_URL"] != "postgres://pooled/appdb" {
		t.Errorf("DATABASE_URL = %q, want the declared line's value rather than the alias line's",
			Redact(store.values["precisiondocs/DATABASE_URL"]))
	}
	if len(adopted) != 1 {
		t.Fatalf("adopted = %+v, want one store key reported once", adopted)
	}
	if adopted[0].Name != "DATABASE_URL" || adopted[0].Key.String() != "precisiondocs/DATABASE_URL" {
		t.Errorf("adopted = %+v, want the declared name reported as the rotation", adopted[0])
	}
	if _, second := store.values["precisiondocs/PG_URL"]; second {
		t.Error("the alias line created a second key for one credential")
	}
}

// TestOnlyTheEchoedFilesAreTreatedAsIgnored pins the security half of the
// batched ignore check. check-ignore exits zero when ANY path matched, so a
// run that classifies three files and echoes two is the ordinary answer for a
// project that tracks one of them - and trusting the exit code alone would
// adopt a credential out of a committed .env that everyone with the
// repository already has.
func TestOnlyTheEchoedFilesAreTreatedAsIgnored(t *testing.T) {
	clearEnv(t, "DATABASE_URL", "STRIPE_SECRET_KEY")
	project := filepath.Join(t.TempDir(), "precisiondocs")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	// .env is tracked, .env.local is not. Only the latter may be read.
	if err := os.WriteFile(filepath.Join(project, ".env"),
		[]byte("DATABASE_URL=postgres://committed/appdb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".env.local"),
		[]byte("STRIPE_SECRET_KEY=sk_local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{
		results:      map[string]execx.Result{"git": {ExitCode: 0}},
		ignoredPaths: []string{filepath.Join(project, ".env.local")},
	}
	manifest := Manifest{Project: "precisiondocs", Services: []Service{{
		Name: "app", Method: MethodEnv, Env: []string{"DATABASE_URL", "STRIPE_SECRET_KEY"},
	}}}
	store := newMemoryStore(nil)

	if _, unscanned, err := Discover(context.Background(), store, runner, manifest, project); err != nil {
		t.Fatalf("Discover: %v", err)
	} else if unscanned.Any() {
		t.Errorf("unscanned = %+v, want git's answer taken as complete", unscanned)
	}
	if store.values["precisiondocs/STRIPE_SECRET_KEY"] != "sk_local" {
		t.Errorf("STRIPE_SECRET_KEY = %q, want the gitignored file adopted",
			Redact(store.values["precisiondocs/STRIPE_SECRET_KEY"]))
	}
	if _, adopted := store.values["precisiondocs/DATABASE_URL"]; adopted {
		t.Error("adopted from a .env git tracks, which is not a local secret")
	}
}

// TestAnUnanswerableBatchFallsBackPerFile is the liveness half. A batch can
// fail for one path - one behind a symlinked package directory, a pathname
// git parses as an option - and treating that as "nothing is ignored" stops
// adoption and refresh for the whole project with no diagnostic, which is the
// stale-credential defect reopened. One bad path must cost only itself.
func TestAnUnanswerableBatchFallsBackPerFile(t *testing.T) {
	clearEnv(t, "DATABASE_URL", "STRIPE_SECRET_KEY")
	project := filepath.Join(t.TempDir(), "precisiondocs")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(project, ".env")
	ignoredFile := filepath.Join(project, ".env.local")
	if err := os.WriteFile(tracked, []byte("DATABASE_URL=postgres://committed/appdb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignoredFile, []byte("STRIPE_SECRET_KEY=sk_local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The batch fails the way an unknown switch does; the per-file asks then
	// answer honestly, ignored for one and tracked for the other.
	runner := &fakeRunner{
		results:      map[string]execx.Result{"git": {ExitCode: 129}},
		ignoredPaths: []string{ignoredFile},
		perFileExit:  map[string]int{tracked: 1, ignoredFile: 0},
	}
	manifest := Manifest{Project: "precisiondocs", Services: []Service{{
		Name: "app", Method: MethodEnv, Env: []string{"DATABASE_URL", "STRIPE_SECRET_KEY"},
	}}}
	store := newMemoryStore(nil)

	_, unscanned, err := Discover(context.Background(), store, runner, manifest, project)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if store.values["precisiondocs/STRIPE_SECRET_KEY"] != "sk_local" {
		t.Errorf("STRIPE_SECRET_KEY = %q, want the fallback to still adopt the gitignored file",
			Redact(store.values["precisiondocs/STRIPE_SECRET_KEY"]))
	}
	if _, adopted := store.values["precisiondocs/DATABASE_URL"]; adopted {
		t.Error("the fallback adopted from a .env git tracks")
	}
	if unscanned.Any() {
		t.Errorf("unscanned = %+v, want nothing left undetermined once the fallback answered", unscanned)
	}
}

// TestAFileGitCannotClassifyIsSkippedAndReported holds the direction that
// must never be guessed. A file whose status git will not answer, even per
// file, could be one the repository tracks, so it is not read - and the
// refusal is named on the dispatch line, because a scan that silently read
// nothing is indistinguishable from a project with nothing to rotate.
func TestAFileGitCannotClassifyIsSkippedAndReported(t *testing.T) {
	clearEnv(t, "DATABASE_URL")
	t.Setenv(StoreDirEnv, t.TempDir())
	dataDir := t.TempDir()
	writeManifest(t, dataDir, "precisiondocs", Manifest{
		Project:  "precisiondocs",
		Services: []Service{{Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"}}},
	})
	project := filepath.Join(t.TempDir(), "precisiondocs")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(project, ".env")
	if err := os.WriteFile(envFile, []byte("DATABASE_URL=postgres://unknown/appdb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Neither the batch nor the per-file ask can answer.
	runner := &fakeRunner{results: map[string]execx.Result{"git": {ExitCode: 128}}}

	result, err := SpawnPreflight{DataDir: dataDir, Runner: runner}.
		Preflight(context.Background(), project)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !strings.Contains(result.Warning, "ignore check failed") || !strings.Contains(result.Warning, envFile) {
		t.Errorf("warning %q does not name the file whose status could not be determined", result.Warning)
	}
	if strings.Contains(result.Warning, "postgres://unknown/appdb") {
		t.Error("the warning printed a credential value")
	}
	if result.Env["DATABASE_URL"] != "" {
		t.Errorf("injected %q, want an unclassifiable file left unread",
			Redact(result.Env["DATABASE_URL"]))
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

	adopted, _, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project)
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
	if _, _, err := Discover(context.Background(), empty, gitIgnoresEverything(), manifest, project); err != nil {
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

	adopted, _, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project)
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

	if _, _, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project); err != nil {
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

// TestCacheAuditNamesAnInheritedLocationRatherThanOmittingIt covers what an
// operator actually audits. CacheEnv returns only what the pane receives, so
// an audit built from it alone is silent about exactly the variable the
// operator tuned: absent and inherited read the same.
func TestCacheAuditNamesAnInheritedLocationRatherThanOmittingIt(t *testing.T) {
	clearEnv(t, "UV_CACHE_DIR", "npm_config_store_dir", "PLAYWRIGHT_BROWSERS_PATH", "GOMODCACHE")
	tuned := filepath.Join(t.TempDir(), "ms-playwright")
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", tuned)
	home := t.TempDir()

	audit := CacheAudit(home, nil)
	if len(audit) != len(cacheVars) {
		t.Fatalf("audit = %+v, want every cache variable listed", audit)
	}
	byName := map[string]CacheRedirect{}
	for _, redirect := range audit {
		byName[redirect.Name] = redirect
	}
	playwright := byName["PLAYWRIGHT_BROWSERS_PATH"]
	if playwright.Source != CacheSourceInherited || playwright.Path != tuned {
		t.Errorf("playwright = %+v, want it marked inherited and pointing where the operator set it", playwright)
	}
	if uv := byName["UV_CACHE_DIR"]; uv.Source != CacheSourceCFO || uv.Path != filepath.Join(home, CacheDirName, "uv") {
		t.Errorf("uv = %+v, want a redirect cfo set rather than an inherited one", uv)
	}
	// The launch path is unchanged: a pane still receives only what cfo sets,
	// so the audit can never hand back an inherited location as a redirect.
	if _, redirected := CacheEnv(home)["PLAYWRIGHT_BROWSERS_PATH"]; redirected {
		t.Error("the audit changed what a pane inherits")
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

// TestCacheAuditReportsTheProjectsOwnDeclaredLocation covers the case that
// makes this audit project-scoped rather than machine-wide. A project that
// declares a cache location in its worktree manifest overrides the shared
// root in both real consumers - the dependency install and the pane - so an
// audit answering "where does this project's goblin build" with the machine
// value would name a directory nothing uses, for exactly the variable
// somebody bothered to tune.
func TestCacheAuditReportsTheProjectsOwnDeclaredLocation(t *testing.T) {
	clearEnv(t, "UV_CACHE_DIR", "npm_config_store_dir", "PLAYWRIGHT_BROWSERS_PATH", "GOMODCACHE")
	home := t.TempDir()
	declared := filepath.Join(t.TempDir(), "project-browsers")
	// The operator's own environment names a third location, so this also
	// pins the precedence: the project beats an inherited value, not just an
	// unset one.
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", filepath.Join(t.TempDir(), "operator-browsers"))

	byName := map[string]CacheRedirect{}
	for _, redirect := range CacheAudit(home, map[string]string{"PLAYWRIGHT_BROWSERS_PATH": declared}) {
		byName[redirect.Name] = redirect
	}
	playwright := byName["PLAYWRIGHT_BROWSERS_PATH"]
	if playwright.Source != CacheSourceProject || playwright.Path != declared {
		t.Errorf("playwright = %+v, want the project's own declared location", playwright)
	}
	// A declaration for one ecosystem must not silence the rest.
	if uv := byName["UV_CACHE_DIR"]; uv.Source != CacheSourceCFO || uv.Path != filepath.Join(home, CacheDirName, "uv") {
		t.Errorf("uv = %+v, want the shared root still reported", uv)
	}
	// The launch path is untouched: a pane still receives only what cfo sets.
	if _, redirected := CacheEnv(home)["PLAYWRIGHT_BROWSERS_PATH"]; redirected {
		t.Error("the audit changed what a pane inherits")
	}
}

// TestTwoCredentialsLandingOnOneKeyProduceOneWrite covers the case where the
// run's own deduplication and the store's disagree. One service's alias
// target being another service's declared name yields two credentials whose
// chains resolve to a single project key, and writing both would leave the
// key holding whichever ran last while the dispatch line reported the other.
// An operator has to be able to trust that "refreshed X" means the store now
// holds X.
func TestTwoCredentialsLandingOnOneKeyProduceOneWrite(t *testing.T) {
	clearEnv(t, "DATABASE_URL", "PG_URL")
	project := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	// Both names are carried by the same file, and both credentials resolve
	// to proj/PG_URL because that is the only key holding a value.
	body := "DATABASE_URL=postgres://named-indirectly/appdb\nPG_URL=postgres://named-directly/appdb\n"
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Services: []Service{
		{
			Name:    "postgres",
			Method:  MethodEnv,
			Env:     []string{"DATABASE_URL"},
			Aliases: map[string][]string{"DATABASE_URL": {"PG_URL"}},
		},
		{Name: "reporting", Method: MethodEnv, Env: []string{"PG_URL"}},
	}}
	store := newMemoryStore(map[string]string{"proj/PG_URL": "postgres://before/appdb"})

	adopted, _, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	writes := 0
	for _, item := range adopted {
		if item.Key.String() == "proj/PG_URL" {
			writes++
		}
	}
	if writes != 1 {
		t.Errorf("reported %d writes to proj/PG_URL, want exactly one", writes)
	}
	// The credential whose own declared name IS the contested key wins: that
	// .env line names the key being written, where the other only reaches it
	// by falling down an alias chain.
	if store.values["proj/PG_URL"] != "postgres://named-directly/appdb" {
		t.Errorf("proj/PG_URL = %q, want the line naming the key directly to have won", Redact(store.values["proj/PG_URL"]))
	}
	// Every reported refresh has to match what the store actually ends up
	// holding, or the dispatch line reports a write that was replaced.
	for _, item := range adopted {
		if item.Refreshed && store.values[item.Key.String()] != "postgres://named-directly/appdb" {
			t.Errorf("reported refreshing %s, but the store holds something else", item.Key)
		}
	}
	// No second key is invented for the alias-only credential.
	if _, created := store.values["proj/DATABASE_URL"]; created {
		t.Error("created a second key for a credential the store already answered through its alias")
	}
}

// TestAnEnvSharedWithALiveWorktreeIsNotAnAdoptionOrigin closes the vector the
// .worktrees prune could not. Provisioning hardlinks a project's .env into
// every goblin worktree, and the worktree copy IS the project's file - same
// inode - so a goblin writing .env writes the Overlord's file and, with
// refresh enabled, would rotate the fleet's stored credential. The link count
// is the only thing that distinguishes a shared file from a private one, and
// it drops back to one when cfo cleanup returns the worktree.
func TestAnEnvSharedWithALiveWorktreeIsNotAnAdoptionOrigin(t *testing.T) {
	clearEnv(t, "STRIPE_SECRET_KEY")
	project := filepath.Join(t.TempDir(), "clock-in")
	worktree := filepath.Join(project, ".worktrees", "gb-1")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(project, ".env")
	if err := os.WriteFile(envPath, []byte("STRIPE_SECRET_KEY=sk_written_by_a_goblin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// This is exactly what provisioning does, and what makes the file
	// goblin-writable: one inode, two directory entries.
	if err := os.Link(envPath, filepath.Join(worktree, ".env")); err != nil {
		t.Skipf("this filesystem does not support hard links: %v", err)
	}

	manifest := Manifest{Services: []Service{{Name: "stripe", Method: MethodEnv, Env: []string{"STRIPE_SECRET_KEY"}}}}
	store := newMemoryStore(map[string]string{"clock-in/STRIPE_SECRET_KEY": "sk_deliberately_stored"})

	adopted, skipped, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if store.values["clock-in/STRIPE_SECRET_KEY"] != "sk_deliberately_stored" {
		t.Error("a goblin-writable file rotated the fleet's stored credential")
	}
	if len(adopted) != 0 {
		t.Errorf("adopted %+v, want nothing taken from a shared file", adopted)
	}
	// Pausing has to be visible, or it is indistinguishable from a credential
	// that simply had nothing to rotate.
	if !contains(skipped.WorktreeShared, envPath) {
		t.Errorf("skipped = %+v, want the shared file named", skipped)
	}
	line := WorktreeSharedLine(skipped.WorktreeShared)
	if !strings.Contains(line, "cfo cleanup") {
		t.Errorf("line = %q, want it to name what releases the file", line)
	}
	if strings.Contains(line, "sk_written_by_a_goblin") || strings.Contains(line, "STRIPE_SECRET_KEY") {
		t.Error("the paused-adoption line disclosed a credential name or value")
	}

	// Returning the worktree releases the file and adoption resumes on its
	// own, with no command and no state of its own to get stuck.
	if err := os.Remove(filepath.Join(worktree, ".env")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project); err != nil {
		t.Fatalf("Discover after cleanup: %v", err)
	}
	if store.values["clock-in/STRIPE_SECRET_KEY"] != "sk_written_by_a_goblin" {
		t.Error("adoption did not resume once the worktree released the file")
	}
}

// TestAnUnreadableLinkCountIsReportedAsItsOwnCause keeps two facts apart that
// share one outcome. A file whose link count cannot be read is skipped
// exactly as a worktree-shared one is, because not knowing whether a goblin
// can write a file is not the same answer as knowing it cannot - but filing
// it under WorktreeShared would tell the operator to run cfo cleanup, and
// returning a worktree cannot fix a stat that failed.
func TestAnUnreadableLinkCountIsReportedAsItsOwnCause(t *testing.T) {
	clearEnv(t, "STRIPE_SECRET_KEY")
	project := filepath.Join(t.TempDir(), "clock-in")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(project, ".env")
	if err := os.WriteFile(envPath, []byte("STRIPE_SECRET_KEY=sk_never_read\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A genuine stat failure here is a race - the file has to vanish between
	// the scan and the count - so the failure is injected at the seam.
	original := linkCount
	t.Cleanup(func() { linkCount = original })
	linkCount = func(string) (uint32, error) { return 0, errors.New("cannot open file for metadata") }

	manifest := Manifest{Services: []Service{{Name: "stripe", Method: MethodEnv, Env: []string{"STRIPE_SECRET_KEY"}}}}
	store := newMemoryStore(map[string]string{"clock-in/STRIPE_SECRET_KEY": "sk_deliberately_stored"})

	adopted, skipped, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	// The skip is unchanged: an unreadable count is never adopted from.
	if store.values["clock-in/STRIPE_SECRET_KEY"] != "sk_deliberately_stored" {
		t.Error("adopted from a file whose link count could not be read")
	}
	if len(adopted) != 0 {
		t.Errorf("adopted %+v, want nothing taken from an uninspectable file", adopted)
	}

	// The cause is its own, and is not filed under the worktree remedy.
	if !contains(skipped.LinkCheckFailed, envPath) {
		t.Errorf("skipped = %+v, want the file reported under the link-check cause", skipped)
	}
	if len(skipped.WorktreeShared) != 0 {
		t.Errorf("skipped.WorktreeShared = %v, want a stat failure kept out of the worktree cause", skipped.WorktreeShared)
	}
	if line := WorktreeSharedLine(skipped.WorktreeShared); line != "" {
		t.Errorf("worktree line = %q, want silence when no worktree shares anything", line)
	}

	line := LinkCheckFailedLine(skipped.LinkCheckFailed)
	if !strings.Contains(line, envPath) {
		t.Errorf("line = %q, want the file named", line)
	}
	// The remedy that cannot help must not be offered.
	if strings.Contains(line, "cfo cleanup") {
		t.Errorf("line = %q, want no worktree remedy for a stat failure", line)
	}
	// Same rule the adoption line keeps: paths only.
	if strings.Contains(line, "STRIPE_SECRET_KEY") || strings.Contains(line, "sk_never_read") {
		t.Error("the link-check line disclosed a credential name or value")
	}
}
