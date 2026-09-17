package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The manifests below are the real ones from this machine's checkouts,
// trimmed to the lines the reader looks at.
func TestReadProjectReadsDeployTargetsFromTheCheckoutsOwnManifests(t *testing.T) {
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	data := filepath.Join(root, "data")

	write(t, filepath.Join(checkout, ".vercel", "project.json"),
		`{"projectId":"prj_7sh1","orgId":"team_zpFDcV5s","projectName":"peak-craftsman"}`)
	write(t, filepath.Join(checkout, "fly.toml"), "# comment\napp = \"precisiondocs-prod\"\nprimary_region = \"sjc\"\n\n[build]\napp = \"not-this-one\"\n")
	write(t, filepath.Join(checkout, "supabase", "config.toml"), "# supabase\nproject_id = \"peakcraftsman-demo\"\n")
	write(t, filepath.Join(data, "projects", "checkout", "auth.json"), `{
	  "project": "checkout",
	  "services": [
	    {"name":"vercel","method":"cli","note":"DEPLOY WITH THE CLI, NEVER GIT. Vercel project peak-craftsman under scope peak-craftsman."},
	    {"name":"ionos","method":"env","env":["IONOS_SSH_KEY_PATH"],"note":"production VPS, Ubuntu 26.04 + Docker"},
	    {"name":"openrouter","method":"env","env":["OPENROUTER_API_KEY"],"note":"server-side text generation"}
	  ]
	}`)

	project, warnings := ReadProject(data, "checkout", checkout)
	if len(warnings) != 0 {
		t.Fatalf("ReadProject warned: %v", warnings)
	}

	targets := map[string]Target{}
	for _, target := range project.Targets {
		targets[target.Provider] = target
	}

	if got := targets[ProviderVercel]; !strings.Contains(got.Detail, "project peak-craftsman") || !strings.Contains(got.Detail, "scope team_zpFDcV5s") {
		t.Errorf("vercel = %+v, want the project and scope from .vercel/project.json", got)
	}
	// The credential note must be folded into the same row, not printed as a
	// second half-target.
	if got := targets[ProviderVercel]; !strings.Contains(got.Note, "NEVER GIT") {
		t.Errorf("vercel note = %q, want the credential manifest's deploy note", got.Note)
	}
	if got := targets[ProviderFly]; got.Detail != "app precisiondocs-prod, primary region sjc" {
		t.Errorf("fly = %+v, want the app and region from fly.toml", got)
	}
	if got := targets[ProviderSupabase]; got.Detail != "project ref peakcraftsman-demo" {
		t.Errorf("supabase = %+v, want the project ref", got)
	}
	// A hosting provider with no manifest of its own still surfaces, carrying
	// what the credential manifest knows: which box, and how it is reached.
	if got := targets["ionos"]; !strings.Contains(got.Note, "Ubuntu 26.04") {
		t.Errorf("ionos = %+v, want the box note surfaced", got)
	}
	// A service the app merely talks to is not a destination.
	if _, listed := targets["openrouter"]; listed {
		t.Error("openrouter was reported as a deploy target")
	}

	// Every fact must name the file it came from.
	for _, target := range project.Targets {
		if target.Source == "" {
			t.Errorf("target %+v names no source file", target)
		}
	}

	// The Supabase project id is also the local stack name, which is the
	// project's own claim on it.
	if len(project.Stacks) != 1 || project.Stacks[0].Name != "peakcraftsman-demo" {
		t.Errorf("stacks = %+v, want the Supabase project id claimed", project.Stacks)
	}
}

// A project's own worktree manifest is taken at its word; everything else is
// derived from what the checkout holds and must say so.
func TestReadProjectPrefersADeclaredLocalStackOverADerivedOne(t *testing.T) {
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	data := filepath.Join(root, "data")
	write(t, filepath.Join(checkout, "docker-compose.yml"), "services:\n  db: {}\n")
	write(t, filepath.Join(data, "projects", "checkout", "worktree.json"),
		`{"project":"checkout","local":{"up":["npm run demo"],"down":["npm run demo:stop"]}}`)

	project, _ := ReadProject(data, "checkout", checkout)
	if !project.Local.Declared {
		t.Errorf("local = %+v, want it marked as the project's own declaration", project.Local)
	}
	if len(project.Local.Up) != 1 || project.Local.Up[0] != "npm run demo" {
		t.Errorf("up = %v, want the declared command, not the compose file", project.Local.Up)
	}
	if !strings.HasSuffix(project.Local.Source, "worktree.json") {
		t.Errorf("source = %q, want the worktree manifest", project.Local.Source)
	}
}

func TestReadProjectDerivesALocalStackFromWhatTheCheckoutHolds(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		up    string
		down  string
	}{
		{
			name:  "supabase",
			files: map[string]string{"supabase/config.toml": "project_id = \"x\"\n"},
			up:    "npx supabase start",
			down:  "npx supabase stop",
		},
		{
			name:  "compose",
			files: map[string]string{"docker-compose.dev.yml": "services: {}\n"},
			up:    "docker compose -f docker-compose.dev.yml up -d",
			down:  "docker compose -f docker-compose.dev.yml down",
		},
		{
			name:  "package script",
			files: map[string]string{"package.json": `{"scripts":{"build":"x","dev":"next dev"}}`},
			up:    "npm run dev",
		},
		{
			name:  "nothing to go on",
			files: map[string]string{"README.md": "hello"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			checkout := filepath.Join(root, "checkout")
			for name, content := range testCase.files {
				write(t, filepath.Join(checkout, filepath.FromSlash(name)), content)
			}
			project, _ := ReadProject(filepath.Join(root, "data"), "checkout", checkout)
			local := project.Local
			if testCase.up == "" {
				if len(local.Up) != 0 {
					t.Fatalf("up = %v, want nothing derived from a checkout with no stack", local.Up)
				}
				return
			}
			if len(local.Up) != 1 || local.Up[0] != testCase.up {
				t.Errorf("up = %v, want %q", local.Up, testCase.up)
			}
			if testCase.down != "" && (len(local.Down) != 1 || local.Down[0] != testCase.down) {
				t.Errorf("down = %v, want %q", local.Down, testCase.down)
			}
			// A derived answer must never be presented as the project's own.
			if local.Declared {
				t.Error("a derived answer was marked as declared")
			}
		})
	}
}

// A compose file names its stack only when it says so. Without a `name:` the
// project name comes from whatever directory it was run in, which is not a
// claim the project made, and treating it as one would attribute somebody
// else's stack to this project.
func TestComposeNameOnlyReadsATopLevelName(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		content string
		want    string
	}{
		{"name: siqsermon-prod\nservices:\n  db: {}\n", "siqsermon-prod"},
		{"services:\n  db:\n    name: not-the-project\n", ""},
		{"services: {}\n", ""},
		{"name: \"quoted-name\"\n", "quoted-name"},
	}
	for index, testCase := range cases {
		path := filepath.Join(root, "compose", string(rune('a'+index)), "docker-compose.yml")
		write(t, path, testCase.content)
		if got := composeName(path); got != testCase.want {
			t.Errorf("composeName(%q) = %q, want %q", testCase.content, got, testCase.want)
		}
	}
}

// A key inside a [table] is that table's, not the file's. fly.toml repeats
// `app` under sections, and reading the wrong one would name the wrong app.
func TestTomlValueStopsAtTheFirstTable(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "fly.toml")
	write(t, path, "# PrecisionDocs\napp = \"precisiondocs-prod\"  # the real one\nprimary_region = \"sjc\"\n\n[build]\napp = \"wrong\"\n")
	if got := tomlValue(path, "app"); got != "precisiondocs-prod" {
		t.Errorf("app = %q, want the top-level value with its comment stripped", got)
	}
	if got := tomlValue(path, "nothing"); got != "" {
		t.Errorf("missing key = %q, want empty", got)
	}
}
