package auth

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestProjectNameReducesEveryWayAProjectIsNamed(t *testing.T) {
	cases := map[string]string{
		"projects/clock-in":                          "clock-in",
		"projects/clock-in/":                         "clock-in",
		`C:\dev\code-goblins\projects\precisiondocs`: "precisiondocs",
		"homescout":                                  "homescout",
		"":                                           "",
	}
	for input, want := range cases {
		if got := ProjectName(input); got != want {
			t.Errorf("ProjectName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLoadManifestRoundTripsThroughItsOnDiskLocation(t *testing.T) {
	dataDir := t.TempDir()
	written := Manifest{
		Project: "clock-in",
		Services: []Service{
			{Name: "neon", Method: MethodCLI, Env: []string{"DATABASE_URL"}, Probe: []string{"neonctl", "projects", "list"}},
			{Name: "github", Method: MethodOAuth, Env: []string{"GITHUB_TOKEN"}, URL: "https://github.com/login"},
		},
	}
	path := writeManifest(t, dataDir, "projects/clock-in", written)

	loaded, err := LoadManifest(dataDir, "projects/clock-in")
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if loaded.Path != path {
		t.Errorf("Path = %q, want %q", loaded.Path, path)
	}
	if len(loaded.Services) != 2 || loaded.Services[0].Name != "neon" {
		t.Fatalf("services = %+v, want manifest order preserved", loaded.Services)
	}
	if got := loaded.EnvNames(); len(got) != 2 || got[0] != "DATABASE_URL" || got[1] != "GITHUB_TOKEN" {
		t.Errorf("EnvNames() = %v, want both names sorted", got)
	}
}

func TestLoadManifestReportsAMissingFileAsNotExist(t *testing.T) {
	_, err := LoadManifest(t.TempDir(), "projects/nothing")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want it to satisfy fs.ErrNotExist so callers can treat it as 'no manifest'", err)
	}
}

func TestValidateRefusesManifestsThatWouldReportMisleadingStatus(t *testing.T) {
	cases := map[string]Manifest{
		"nameless service":     {Services: []Service{{Method: MethodEnv, Env: []string{"A"}}}},
		"unknown method":       {Services: []Service{{Name: "x", Method: "magic"}}},
		"duplicate name":       {Services: []Service{{Name: "x", Method: MethodCLI}, {Name: "x", Method: MethodCLI}}},
		"unexportable env":     {Services: []Service{{Name: "x", Method: MethodEnv, Env: []string{"BAD-NAME"}}}},
		"env method, no names": {Services: []Service{{Name: "x", Method: MethodEnv}}},
	}
	for name, manifest := range cases {
		if err := manifest.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want a refusal", name)
		}
	}
}

func TestValidateAcceptsACLIServiceWithNoEnvironmentOfItsOwn(t *testing.T) {
	// gh keeps its own credential; cfo only needs to know how to probe it.
	manifest := Manifest{Services: []Service{{Name: "github", Method: MethodCLI, Probe: []string{"gh", "auth", "status"}}}}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidEnvNameMatchesThePaneShellRule(t *testing.T) {
	valid := []string{"A", "DATABASE_URL", "_UNDERSCORE", "KEY2"}
	invalid := []string{"", "2LEADING", "has-dash", "has.dot", "has space"}
	for _, name := range valid {
		if !ValidEnvName(name) {
			t.Errorf("ValidEnvName(%q) = false, want true", name)
		}
	}
	for _, name := range invalid {
		if ValidEnvName(name) {
			t.Errorf("ValidEnvName(%q) = true, want false", name)
		}
	}
}

func TestFileStoreRoundTripsAndRestrictsTheSecret(t *testing.T) {
	root := filepath.Join(t.TempDir(), "credentials")
	store, err := OpenFileStore(root)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	if _, found, err := store.Get("ABSENT_KEY"); err != nil || found {
		t.Fatalf("Get(absent) = (_, %v, %v), want (\"\", false, nil)", found, err)
	}
	if err := store.Set("STRIPE_SECRET_KEY", "sk_test_value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	value, found, err := store.Get("STRIPE_SECRET_KEY")
	if err != nil || !found || value != "sk_test_value" {
		t.Fatalf("Get = (%q, %v, %v), want the stored value", value, found, err)
	}
	keys, err := store.Keys()
	if err != nil || len(keys) != 1 || keys[0] != "STRIPE_SECRET_KEY" {
		t.Fatalf("Keys() = (%v, %v), want one name and no values", keys, err)
	}
	assertOwnerOnly(t, filepath.Join(root, "STRIPE_SECRET_KEY"))
}

func TestFileStoreTrimsTheTrailingNewlineAShellRedirectAdds(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "TOKEN_VALUE"), []byte("secret\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := store.Get("TOKEN_VALUE")
	if err != nil {
		t.Fatal(err)
	}
	if value != "secret" {
		t.Errorf("value = %q, want the newline trimmed; a token with one is a different token", value)
	}
}

func TestFileStoreRefusesAKeyThatIsNotAnEnvironmentName(t *testing.T) {
	store, err := OpenFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A key that escapes its directory would write a secret anywhere.
	if err := store.Set("../escaped", "value"); err == nil {
		t.Error("Set(\"../escaped\") = nil, want a refusal")
	}
	if _, _, err := store.Get("../escaped"); err == nil {
		t.Error("Get(\"../escaped\") = nil, want a refusal")
	}
}

func TestWriteSecretFileIsNotWorldReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "auth.ps1")
	if err := WriteSecretFile(path, "$env:A = 'b'\n"); err != nil {
		t.Fatalf("WriteSecretFile: %v", err)
	}
	assertOwnerOnly(t, path)
}

func TestParseEnvFileReadsTheShapesRealFilesUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := strings.Join([]string{
		"# a comment",
		"",
		"DATABASE_URL=postgres://user:pass@host/db",
		`STRIPE_SECRET_KEY="sk_test_quoted"`,
		"export SENTRY_DSN='https://key@sentry.io/1'",
		"NOT AN ASSIGNMENT",
		"lowercase_ignored=value",
		"EMPTY=",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := ParseEnvFile(path)
	if err != nil {
		t.Fatalf("ParseEnvFile: %v", err)
	}
	want := map[string]string{
		"DATABASE_URL":      "postgres://user:pass@host/db",
		"STRIPE_SECRET_KEY": "sk_test_quoted",
		"SENTRY_DSN":        "https://key@sentry.io/1",
		"lowercase_ignored": "value",
		"EMPTY":             "",
	}
	for name, expected := range want {
		if values[name] != expected {
			t.Errorf("%s = %q, want %q", name, values[name], expected)
		}
	}
	if _, present := values["NOT AN ASSIGNMENT"]; present {
		t.Error("a line with no = became a variable")
	}
}

func TestDiscoverAdoptsProjectEnvFilesWithoutOverwritingTheStore(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("DATABASE_URL=postgres://adopted\nSTRIPE_SECRET_KEY=sk_from_env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A nested workspace package keeps its own file.
	nested := filepath.Join(project, "apps", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, ".env"), []byte("SENTRY_DSN=https://nested\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A dependency tree must not be walked for secrets.
	modules := filepath.Join(project, "node_modules", "pkg")
	if err := os.MkdirAll(modules, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modules, ".env"), []byte("UNDECLARED_KEY=nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := Manifest{Services: []Service{
		{Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"}},
		{Name: "stripe", Method: MethodEnv, Env: []string{"STRIPE_SECRET_KEY"}},
		{Name: "sentry", Method: MethodEnv, Env: []string{"SENTRY_DSN"}},
	}}
	store := newMemoryStore(map[string]string{"STRIPE_SECRET_KEY": "sk_deliberate"})

	adopted, err := Discover(context.Background(), store, &fakeRunner{}, manifest, project)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if store.values["DATABASE_URL"] != "postgres://adopted" {
		t.Errorf("DATABASE_URL = %q, want it adopted from the project .env", store.values["DATABASE_URL"])
	}
	if store.values["SENTRY_DSN"] != "https://nested" {
		t.Errorf("SENTRY_DSN = %q, want the nested package file adopted", store.values["SENTRY_DSN"])
	}
	// An existing credential is deliberate; adoption must never replace it.
	if store.values["STRIPE_SECRET_KEY"] != "sk_deliberate" {
		t.Errorf("STRIPE_SECRET_KEY = %q, want the stored value kept", store.values["STRIPE_SECRET_KEY"])
	}
	if _, present := store.values["UNDECLARED_KEY"]; present {
		t.Error("adopted a name the manifest never declared")
	}
	for _, item := range adopted {
		if item.Name == "STRIPE_SECRET_KEY" {
			t.Error("reported adopting a credential that was already stored")
		}
	}
}

func TestDiscoverAdoptsTheTokenGhAlreadyOwns(t *testing.T) {
	manifest := Manifest{Services: []Service{{Name: "github", Method: MethodCLI, Env: []string{"GITHUB_TOKEN"}}}}
	store := newMemoryStore(nil)
	runner := &fakeRunner{results: map[string]execx.Result{
		"gh": {ExitCode: 0, Stdout: []byte("gho_alreadyauthenticated\n")},
	}}

	adopted, err := Discover(context.Background(), store, runner, manifest, t.TempDir())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if store.values["GITHUB_TOKEN"] != "gho_alreadyauthenticated" {
		t.Errorf("GITHUB_TOKEN = %q, want the token gh already holds", store.values["GITHUB_TOKEN"])
	}
	if len(adopted) != 1 || adopted[0].Origin != "gh auth token" {
		t.Errorf("adopted = %+v, want one entry crediting gh", adopted)
	}
}

func TestDiscoverSurfacesAStoreThatCannotBeWritten(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("DATABASE_URL=postgres://x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Services: []Service{{Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"}}}}
	store := newMemoryStore(nil)
	store.setErr = errors.New("vault is locked")

	if _, err := Discover(context.Background(), store, &fakeRunner{}, manifest, project); err == nil {
		t.Fatal("Discover = nil, want the store failure surfaced rather than silently dropping a credential")
	}
}
