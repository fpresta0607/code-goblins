package supervisor

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// newEnvBoard is a board that took the CFO's request for names whose values
// also go to .env.docker.local in a throwaway checkout of the project, a file
// that already sets PORT.
func newEnvBoard(t *testing.T, names ...string) (*credentialBoard, string, func(dir string, args ...string)) {
	t.Helper()
	root, git := envRepository(t)
	path := filepath.Join(root, ".env.docker.local")
	if err := os.WriteFile(path, []byte("PORT=3000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := newCredentialBoardFor(t, CredentialRequest{ID: "cred-0123456789abcdef", Identity: strings.Repeat("c", 64), By: "cfo", Project: "precisiondocs", Repository: root, EnvFile: ".env.docker.local", Names: names, Why: "Local database for docker compose"})
	return b, path, git
}

// A save for a request that names an env file stores each value in the
// project's scope and also sets its line in that file, and the CFO hears the
// file's name, never the value.
func TestCredentialSaveAlsoSetsItsLineInTheRequestsEnvFile(t *testing.T) {
	// Arrange
	b, path, _ := newEnvBoard(t, "DATABASE_URL")
	canary := newCanary(t)

	// Act
	response := b.post(b.handler, b.body(map[string]string{"DATABASE_URL": canary}), nil)
	b.service.credentialWork.Wait()

	// Assert
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), canary) {
		t.Fatalf("save = %d, want 200 with names only", response.Code)
	}
	values, err := auth.ParseEnvFile(path)
	if err != nil || values["DATABASE_URL"] != canary || values["PORT"] != "3000" {
		t.Fatalf("the env file does not hold the value with PORT kept (%v)", err)
	}
	if stored, ok := b.vaultValue("DATABASE_URL"); !ok || stored != canary {
		t.Fatal("the project's scope does not hold the value")
	}
	if request := b.stored(); !slices.Equal(request.Written, []string{"DATABASE_URL"}) || request.Reason != "" {
		t.Fatalf("request written %v, reason %q; want the name written and no reason", request.Written, request.Reason)
	}
	details := strings.Join(b.pendingDetails(), "\n")
	if !strings.Contains(details, "written to .env.docker.local") || strings.Contains(details, canary) {
		t.Fatalf("wakes = %q, want them to name the file and never the value", details)
	}
}

// The board checks the env file again before it writes: a file git began to
// track after the request was filed is never written, while the value still
// reaches the project's scope and the CFO hears why.
func TestCredentialSaveChecksTheEnvFileAgainBeforeWriting(t *testing.T) {
	// Arrange
	b, path, git := newEnvBoard(t, "DATABASE_URL")
	git(filepath.Dir(path), "add", "-f", ".env.docker.local")
	canary := newCanary(t)

	// Act
	response := b.post(b.handler, b.body(map[string]string{"DATABASE_URL": canary}), nil)
	b.service.credentialWork.Wait()

	// Assert
	if response.Code != http.StatusOK {
		t.Fatalf("save = %d, want the value taken into the scope", response.Code)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "PORT=3000\n" {
		t.Fatalf("the tracked env file changed (%v)", err)
	}
	if stored, ok := b.vaultValue("DATABASE_URL"); !ok || stored != canary {
		t.Fatal("the project's scope does not hold the value")
	}
	if request := b.stored(); len(request.Written) != 0 || !strings.Contains(request.Reason, "tracked") {
		t.Fatalf("request written %v, reason %q; want nothing written and the reason", request.Written, request.Reason)
	}
	details := strings.Join(b.pendingDetails(), "\n")
	if !strings.Contains(details, "not written to .env.docker.local") || strings.Contains(details, canary) {
		t.Fatalf("wakes = %q, want them to say the file was not written, never the value", details)
	}
}

// The board refuses a request whose env file git would commit before the
// Overlord ever sees its card.
func TestBoardRefusesAnEnvFileRequestGitWouldCommit(t *testing.T) {
	// Arrange
	root, _ := envRepository(t)
	store, _ := testStore(t)
	t.Setenv(auth.StoreDirEnv, t.TempDir())
	service := &Service{Store: store, Options: Options{Credentials: auth.OpenStore}}

	// Act
	_, err := service.acceptCredentialRequest(CredentialRequest{ID: "cred-0123456789abcdef", Identity: strings.Repeat("c", 64), By: "cfo", Project: "precisiondocs", Repository: root, EnvFile: ".env.example", Names: []string{"DATABASE_URL"}, Why: "Local database"})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "tracked") {
		t.Fatalf("accept = %v, want it refused as tracked", err)
	}
	if requests := store.Snapshot().Credentials; len(requests) != 0 {
		t.Fatalf("the board holds %d requests, want none", len(requests))
	}
}

// What a request's terminal stored goes to its env file too: the board reads
// each name it provably stored from the scope and sets its line.
func TestCredentialTerminalSetsTheEnvFileForWhatItStored(t *testing.T) {
	// Arrange
	b, path, _ := newEnvBoard(t, "DATABASE_URL")
	b.service.Options.Runs = &fakeRunLauncher{started: liveStart(t)}
	b.openTerminal()

	// Act
	b.typeValue("DATABASE_URL")
	b.endTerminal(0)

	// Assert
	stored, ok := b.vaultValue("DATABASE_URL")
	values, err := auth.ParseEnvFile(path)
	if !ok || err != nil || values["DATABASE_URL"] != stored || values["PORT"] != "3000" {
		t.Fatalf("the env file does not hold what the terminal stored (%v)", err)
	}
	if request := b.stored(); request.State != "saved" || !slices.Equal(request.Written, []string{"DATABASE_URL"}) {
		t.Fatalf("request = %+v, want it saved with the name written", request)
	}
	if details := strings.Join(b.pendingDetails(), "\n"); !strings.Contains(details, "written to .env.docker.local") || strings.Contains(details, stored) {
		t.Fatalf("wakes = %q, want them to name the file and never the value", details)
	}
}

// A goblin's request may name an env file only in its own task's checkout,
// so a goblin cannot have a value it does not hold written where it can read
// it.
func TestGoblinEnvFileRequestMustNameItsOwnTasksCheckout(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	t.Setenv(auth.StoreDirEnv, t.TempDir())
	service := &Service{Store: store, Options: Options{Credentials: auth.OpenStore}}
	own, _ := envRepository(t)
	other, _ := envRepository(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.Project = own
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	inbox := filepath.Join(h.State, credentialInbox)
	now := time.Now().UTC()
	plant(t, inbox, "a.json", CredentialRequest{ID: "cred-aaaaaaaaaaaaaaaa", Identity: goblinIdentity(meta), By: "goblin", Task: "task-1", Project: "precisiondocs", Repository: other, EnvFile: ".env.local", Names: []string{"DATABASE_URL"}, Why: "Local database", CreatedAt: now})
	plant(t, inbox, "b.json", CredentialRequest{ID: "cred-bbbbbbbbbbbbbbbb", Identity: goblinIdentity(meta), By: "goblin", Task: "task-1", Project: "precisiondocs", Repository: own, EnvFile: ".env.local", Names: []string{"REDIS_URL"}, Why: "Local cache", CreatedAt: now})

	// Act
	err = service.ingestCredentialRequests()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var taken []string
	for _, request := range store.Snapshot().Credentials {
		taken = append(taken, request.ID)
	}
	if !slices.Equal(taken, []string{"cred-bbbbbbbbbbbbbbbb"}) {
		t.Fatalf("taken = %v, want only the request for the goblin's own checkout", taken)
	}
	if issues := strings.Join(store.Snapshot().Issues, "\n"); !strings.Contains(issues, "its own task's checkout") {
		t.Fatalf("issues = %q, want the other request refused and named", issues)
	}
}
