package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// useScratchHome points cfo at a home of the test's own, so a request it
// files can never reach the live fleet's board.
func useScratchHome(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CFO_HOME", root)
	t.Setenv("CFO_STATE_OVERRIDE", filepath.Join(root, "state"))
	return root
}

// A credential request takes names only. An argument shaped like a value is
// refused before anything is filed, and the refusal never repeats it.
func TestAuthRequestRefusesValuesWithoutRepeatingThem(t *testing.T) {
	random := "q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc5Fg0Js"
	t.Run("a plain request gets past the checks to who is asking", func(t *testing.T) {
		useScratchHome(t)
		code, _, stderr := runCLI(t, "request", "--project", "throwaway", "--why", "Charge test cards", "--link", "https://dashboard.stripe.com/apikeys", "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET")
		if code != 1 || !strings.Contains(stderr, "registered CFO") {
			t.Fatalf("a plain request = %d %q, want it refused only for its caller", code, stderr)
		}
	})
	for name, args := range map[string][]string{
		"a key as a name":           {"--project", "throwaway", "--why", "Charge test cards", "sk" + "_live_" + random},
		"a name with its value":     {"--project", "throwaway", "--why", "Charge test cards", "STRIPE_SECRET_KEY=" + random},
		"random text as a name":     {"--project", "throwaway", "--why", "Charge test cards", random},
		"a key in the reason":       {"--project", "throwaway", "--why", "use " + "ghp" + "_" + random, "GITHUB_TOKEN"},
		"a key in the link":         {"--project", "throwaway", "--why", "Charge test cards", "--link", "https://example.com/" + random, "STRIPE_SECRET_KEY"},
		"a link with a query":       {"--project", "throwaway", "--why", "Charge test cards", "--link", "https://example.com/keys?token=" + random, "STRIPE_SECRET_KEY"},
		"no project":                {"--why", "Charge test cards", "STRIPE_SECRET_KEY"},
		"no reason":                 {"--project", "throwaway", "STRIPE_SECRET_KEY"},
		"no names":                  {"--project", "throwaway", "--why", "Charge test cards"},
		"a scope that walks upward": {"--project", "../escaped", "--why", "Charge test cards", "STRIPE_SECRET_KEY"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			root := useScratchHome(t)

			// Act
			code, stdout, stderr := runCLI(t, append([]string{"request"}, args...)...)

			// Assert
			if code != 2 {
				t.Fatalf("cfo auth request = %d, want 2; stderr %s", code, stderr)
			}
			if strings.Contains(stdout+stderr, random) {
				t.Fatal("the refusal repeats the value")
			}
			if _, err := os.Stat(filepath.Join(root, "state", "credential-requests-inbox")); !os.IsNotExist(err) {
				t.Fatal("a refused request reached the inbox")
			}
		})
	}
}

// A request names the checkout its scope is for, so the card can show the
// repository a value goes to; a scope with no checkout here names none.
func TestAuthRequestNamesTheRepositoryItsScopeIsFor(t *testing.T) {
	// Arrange
	root := t.TempDir()
	checkout := filepath.Join(root, "Acme-Shop")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{projectsRoot: func() (string, error) { return root, nil }}
	request := func(project string) (string, string) {
		t.Helper()
		filed, err := credentialRequest(runtime, project, "", "Charge test cards", "", "", []string{"STRIPE_SECRET_KEY"})
		if err != nil {
			t.Fatalf("credentialRequest(%q) = %v", project, err)
		}
		return filed.Project, filed.Repository
	}

	// Act
	byNameScope, byNameRepository := request("acme")
	byPathScope, byPathRepository := request(checkout)
	elsewhereScope, elsewhereRepository := request("elsewhere")
	notesScope, notesRepository := request(filepath.Join(root, "notes"))

	// Assert
	if byNameScope != "Acme-Shop" || byNameRepository != checkout || byPathScope != "Acme-Shop" || byPathRepository != checkout {
		t.Fatalf("by name = %q %q, by path = %q %q; want the checkout's scope and folder", byNameScope, byNameRepository, byPathScope, byPathRepository)
	}
	if elsewhereScope != "elsewhere" || elsewhereRepository != "" || notesScope != "notes" || notesRepository != "" {
		t.Fatalf("no checkout = %q %q, a folder without .git = %q %q; want the scope and no repository", elsewhereScope, elsewhereRepository, notesScope, notesRepository)
	}
}

// A value saved on the board reaches running goblins through cfo auth
// store's own refresh: each live task of the project gets its auth.ps1
// regenerated and a re-source notice, which names the script and never the
// value. The board hears which goblins were told, and why one was not.
func TestBoardSavesRefreshThroughTheAuthStoreRefresh(t *testing.T) {
	// Arrange
	useFileStore(t)
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	live := writeCmdTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	writeCmdTaskMeta(t, stateDir, "deaf-1", project, "pane-deaf", true)
	writeCmdTaskMeta(t, stateDir, "parked-1", project, "pane-parked", true)
	canary := "canary-" + strings.Repeat("7f", 16)
	store, err := auth.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(auth.Key{Project: "precisiondocs", Name: "STRIPE_SECRET_KEY"}, canary); err != nil {
		t.Fatal(err)
	}
	var sent []string
	runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{"pane-live": true, "pane-deaf": true}}, &sent)
	deliver := runtime.sendText
	runtime.sendText = func(ctx context.Context, h home.Home, target, text string) error {
		if target == "gb-deaf-1" {
			return errors.New("the pane did not take it")
		}
		return deliver(ctx, h, target, text)
	}

	// Act
	told, refreshErr := boardCredentialRefresh(runtime)(context.Background(), "precisiondocs")

	// Assert
	if !slices.Equal(told, []string{"live-1"}) {
		t.Fatalf("told = %v, want only the live goblin whose pane took the notice", told)
	}
	if refreshErr == nil || !strings.Contains(refreshErr.Error(), "deaf-1") {
		t.Fatalf("refresh error = %v, want the goblin that was not told named", refreshErr)
	}
	if len(sent) != 1 || sent[0] != "gb-live-1 credentials refreshed: re-source "+filepath.Join(live.TaskTmp, "auth.ps1") {
		t.Fatalf("notices = %v, want one re-source notice naming the script", sent)
	}
	if strings.Contains(strings.Join(sent, "\n")+refreshErr.Error(), canary) {
		t.Fatal("a notice or the refresh's report holds the value")
	}
	script, err := os.ReadFile(filepath.Join(live.TaskTmp, "auth.ps1"))
	if err != nil || !strings.Contains(string(script), canary) {
		t.Fatalf("the live goblin's auth.ps1 does not carry the saved value: %v", err)
	}
}

// Only the registered CFO, or a goblin from its own terminal naming its own
// task, can file a request; anyone else is refused with who can.
func TestAuthRequestFromNeitherTheCFONorTheGoblinIsRefused(t *testing.T) {
	// Arrange
	root := useScratchHome(t)

	// Act
	code, _, stderr := runCLI(t, "request", "--project", "throwaway", "--task", "task-1", "--why", "Charge test cards", "STRIPE_SECRET_KEY")

	// Assert
	if code != 1 || !strings.Contains(stderr, "registered CFO") {
		t.Fatalf("cfo auth request from a test process = %d %q, want it refused naming who can ask", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "credential-requests-inbox")); !os.IsNotExist(err) {
		t.Fatal("a refused request reached the inbox")
	}
}

// A request that names an env file is checked with git before anything is
// filed: one git would commit, or one with no checkout, is refused saying
// why, and an ignored, untracked one goes on to be filed.
func TestAuthRequestChecksItsEnvFileBeforeFiling(t *testing.T) {
	// Arrange
	useScratchHome(t)
	root := t.TempDir()
	checkout := filepath.Join(root, "acme-shop")
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", checkout}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "--initial-branch=main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	for name, content := range map[string]string{".gitignore": ".env*\n!.env.example\n", ".env.example": "DATABASE_URL=\n"} {
		if err := os.WriteFile(filepath.Join(checkout, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".gitignore", ".env.example")
	git("commit", "-q", "-m", "start")
	runtime := commandRuntime{projectsRoot: func() (string, error) { return root, nil }}
	for _, test := range []struct {
		name, project, file, want string
		code                      int
	}{
		{"a tracked file", "acme-shop", ".env.example", "tracked", 2},
		{"no checkout", "elsewhere", ".env.local", "checkout", 2},
		{"an ignored, untracked file", "acme-shop", ".env.local", "registered CFO", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			// Act
			code := runAuthRequest([]string{"--project", test.project, "--env-file", test.file, "--why", "Local database for docker compose", "DATABASE_URL"}, &stdout, &stderr, runtime)

			// Assert
			if code != test.code || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("exit %d, stderr %q; want exit %d saying %q", code, stderr.String(), test.code, test.want)
			}
		})
	}
}
