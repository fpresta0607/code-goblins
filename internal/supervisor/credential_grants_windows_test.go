package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestTaskCredentialSaveGrantsOnlyItsRequestingTask(t *testing.T) {
	// Arrange
	s, stateDir := grantBoard(t)
	asOverlordsBoard(s)
	s.Options.Credentials = auth.OpenStore
	t.Cleanup(s.credentialWork.Wait)
	meta, err := state.ReadTaskMeta(stateDir, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	bystander := meta
	bystander.ID = "task-2"
	bystander.TaskTmp = t.TempDir()
	if err := state.WriteTaskMeta(stateDir, bystander); err != nil {
		t.Fatal(err)
	}
	meta = makeNative(t, stateDir, meta.ID)
	s.Options.CFO = &CFOConnection{State: stateDir}
	previousSender := typeIntoGoblin
	var notices []string
	typeIntoGoblin = func(_ context.Context, directory string, task state.TaskMeta, line string, _ bool) error {
		if directory != stateDir || task.ID != meta.ID || task.SpawnGen != meta.SpawnGen {
			t.Error("the re-source notice targeted another task generation")
		}
		notices = append(notices, line)
		return nil
	}
	t.Cleanup(func() { s.credentialWork.Wait(); typeIntoGoblin = previousSender })
	card, err := s.acceptCredentialRequest(CredentialRequest{ID: "cred-0123456789abcdef", By: "goblin", Identity: goblinIdentity(meta), Task: meta.ID, Project: "work", Names: []string{"SAMPLE_TOKEN"}, Why: "Read the test service"})
	if err != nil {
		t.Fatal(err)
	}
	value := newCanary(t)
	body, err := json.Marshal(credentialSave{ID: card.ID, Generation: card.Generation, Values: map[string]string{"SAMPLE_TOKEN": value}})
	if err != nil {
		t.Fatal(err)
	}

	// Act
	code, response := askTheBoard(t, s, "POST", "/api/credentials/save", string(body), nil)
	s.credentialWork.Wait()

	// Assert
	after, err := state.ReadTaskMeta(stateDir, meta.ID)
	if code != http.StatusOK || err != nil || !slices.Equal(after.Credentials, []string{"other", "sample"}) {
		t.Fatalf("save = %d, grants %v (%v)", code, after.Credentials, err)
	}
	script, err := os.ReadFile(filepath.Join(meta.TaskTmp, state.AuthScriptName))
	if err != nil || !strings.Contains(string(script), value) {
		t.Fatal("the requesting task's auth script does not carry the saved value")
	}
	other, err := state.ReadTaskMeta(stateDir, bystander.ID)
	if err != nil || !slices.Equal(other.Credentials, []string{"other"}) {
		t.Fatal("the bystander's grants changed")
	}
	if _, err := os.Stat(filepath.Join(bystander.TaskTmp, state.AuthScriptName)); !os.IsNotExist(err) {
		t.Fatal("the bystander's script changed")
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "re-source "+filepath.Join(meta.TaskTmp, state.AuthScriptName)) {
		t.Fatal("the requesting goblin was not told to re-source")
	}
	pending, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
	log, err := state.TailStatus(stateDir, meta.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response+string(encoded)+strings.Join(log, "\n")+strings.Join(notices, "\n"), value) {
		t.Fatal("the value escaped into a response or wake")
	}
}

func TestTaskCredentialSaveRequiresTheOverlordsOwnBoard(t *testing.T) {
	// Arrange
	s, stateDir := grantBoard(t)
	s.peerOf = func(netip.AddrPort, netip.AddrPort) (int, error) { return 0, errors.New("unknown peer") }
	s.Options.Credentials = auth.OpenStore
	card, err := s.acceptCredentialRequest(CredentialRequest{ID: "cred-0123456789abcdef", By: "cfo", Identity: strings.Repeat("c", 64), Task: "task-1", Project: "work", Names: []string{"SAMPLE_TOKEN"}, Why: "Read the test service"})
	if err != nil {
		t.Fatal(err)
	}
	value := newCanary(t)
	body, err := json.Marshal(credentialSave{ID: card.ID, Generation: card.Generation, Values: map[string]string{"SAMPLE_TOKEN": value}})
	if err != nil {
		t.Fatal(err)
	}

	// Act
	code, response := askTheBoard(t, s, "POST", "/api/credentials/save", string(body), nil)

	// Assert
	meta, err := state.ReadTaskMeta(stateDir, "task-1")
	if code != http.StatusForbidden || err != nil || !slices.Equal(meta.Credentials, []string{"other"}) {
		t.Fatalf("save = %d, grants %v (%v)", code, meta.Credentials, err)
	}
	store, err := auth.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := store.Get(auth.Key{Project: "work", Name: "SAMPLE_TOKEN"})
	if err != nil || found || strings.Contains(response, value) {
		t.Fatal("the refused save stored or disclosed its value")
	}
}

func TestTaskCredentialCardNeverGrantsOutsideItsRecordedScope(t *testing.T) {
	for _, scenario := range []string{"dismissed", "withdrawn", "expired", "stale card", "replaced", "cross-project", "undeclared", "changed declaration", "ambiguous", "fetched"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange
			s, stateDir := grantBoard(t)
			asOverlordsBoard(s)
			s.Options.Credentials = auth.OpenStore
			t.Cleanup(s.credentialWork.Wait)
			card, err := s.acceptCredentialRequest(CredentialRequest{ID: "cred-0123456789abcdef", By: "cfo", Identity: strings.Repeat("c", 64), Task: "task-1", Project: "work", Names: []string{"SAMPLE_TOKEN"}, Why: "Read the test service"})
			if err != nil {
				t.Fatal(err)
			}
			meta, err := state.ReadTaskMeta(stateDir, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "dismissed", "withdrawn":
				if _, err := s.closeCredential(card.ID, card.Generation, scenario, "overlord", "Not needed"); err != nil {
					t.Fatal(err)
				}
			case "expired":
				if _, err := s.Store.updateCredential(card.ID, func(request *CredentialRequest) { request.ExpiresAt = request.CreatedAt }); err != nil {
					t.Fatal(err)
				}
			case "replaced", "cross-project":
				if scenario == "replaced" {
					meta.SpawnGen = "g2"
				} else {
					meta.Project = filepath.Join(filepath.Dir(meta.Project), "elsewhere")
				}
				if err := state.WriteTaskMeta(stateDir, meta); err != nil {
					t.Fatal(err)
				}
			case "undeclared", "changed declaration", "ambiguous":
				services := `[]`
				if scenario == "changed declaration" {
					services = `[{"name":"replacement","method":"env","env":["SAMPLE_TOKEN"]}]`
				}
				if scenario == "ambiguous" {
					services = `[{"name":"sample","method":"env","env":["SAMPLE_TOKEN"]},{"name":"replacement","method":"env","env":["SAMPLE_TOKEN"]}]`
				}
				if err := os.WriteFile(auth.ManifestPath(s.Store.Home.Data, "work"), []byte(`{"project":"work","services":`+services+`}`), 0600); err != nil {
					t.Fatal(err)
				}
			}

			value := newCanary(t)
			input := credentialSave{ID: card.ID, Generation: card.Generation, Values: map[string]string{"SAMPLE_TOKEN": value}, isFetched: scenario == "fetched"}
			if scenario == "stale card" {
				input.Generation = "old"
			}
			isRefused := slices.Contains([]string{"dismissed", "withdrawn", "expired", "stale card"}, scenario)

			// Act
			if scenario == "fetched" {
				if _, err := s.saveCredentials(input); err != nil {
					t.Fatal(err)
				}
			} else {
				body, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				code, response := askTheBoard(t, s, "POST", "/api/credentials/save", string(body), nil)
				want := http.StatusOK
				if isRefused {
					want = http.StatusConflict
				}
				if code != want {
					t.Fatalf("save = %d %s, want %d", code, response, want)
				}
				if strings.Contains(response, value) {
					t.Fatal("the save response disclosed its value")
				}
			}
			s.credentialWork.Wait()

			// Assert
			after, err := state.ReadTaskMeta(stateDir, "task-1")
			if err != nil || !slices.Equal(after.Credentials, []string{"other"}) {
				t.Fatalf("grants changed to %v (%v)", after.Credentials, err)
			}
			if isRefused {
				store, err := auth.OpenStore()
				if err != nil {
					t.Fatal(err)
				}
				if _, found, err := store.Get(auth.Key{Project: "work", Name: "SAMPLE_TOKEN"}); err != nil || found {
					t.Fatal("a closed or stale card stored its value")
				}
			} else if scenario != "fetched" {
				updated, found := s.Store.credential(card.ID)
				if !found || !strings.HasPrefix(updated.Reason, "No service granted: ") {
					t.Fatal("the save did not report its refused grant")
				}
			}
		})
	}
}

func TestGrantRequestCardRejectsMismatchedOrClosedPresses(t *testing.T) {
	for _, scenario := range []string{"denied", "cancelled", "stale task", "stale card", "another task", "another service", "cross-project", "undeclared", "done"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange
			s, stateDir := grantBoard(t)
			asOverlordsBoard(s)
			t.Cleanup(s.credentialWork.Wait)
			card, err := s.acceptCredentialRequest(CredentialRequest{ID: "cred-0123456789abcdef", By: "cfo", Identity: strings.Repeat("c", 64), Task: "task-1", Project: "work", GrantService: "sample", Why: "Read the test service"})
			if err != nil {
				t.Fatal(err)
			}
			input := connectionRequest{Task: "task-1", Generation: "g1", Connection: "service:sample", Action: "grant", Request: card.ID, RequestGeneration: card.Generation}
			switch scenario {
			case "denied", "cancelled":
				closed := "dismissed"
				if scenario == "cancelled" {
					closed = "withdrawn"
				}
				if _, err := s.closeCredential(card.ID, card.Generation, closed, "overlord", "Not needed"); err != nil {
					t.Fatal(err)
				}
			case "stale card":
				input.RequestGeneration = "old"
			case "another task":
				input.Task = "task-2"
			case "another service":
				input.Connection = "service:other"
			case "stale task", "cross-project":
				meta, err := state.ReadTaskMeta(stateDir, "task-1")
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "stale task" {
					meta.SpawnGen = "g2"
				} else {
					meta.Project = filepath.Join(filepath.Dir(meta.Project), "elsewhere")
				}
				if err := state.WriteTaskMeta(stateDir, meta); err != nil {
					t.Fatal(err)
				}
			case "undeclared":
				if err := os.WriteFile(auth.ManifestPath(s.Store.Home.Data, "work"), []byte(`{"project":"work","services":[]}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			body, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}

			// Act
			code := 0
			if scenario == "done" {
				_, err = s.closeCredential(card.ID, card.Generation, "done", "overlord", "")
				if err != nil {
					code = http.StatusConflict
				}
			} else {
				code, _ = askTheBoard(t, s, "POST", "/api/connections/grant", string(body), nil)
			}

			// Assert
			after, err := state.ReadTaskMeta(stateDir, "task-1")
			if code != http.StatusConflict || err != nil || !slices.Equal(after.Credentials, []string{"other"}) {
				t.Fatalf("press = %d, grants %v (%v)", code, after.Credentials, err)
			}
		})
	}
}

func TestGrantRequestCardGrantsAfterThePressAndWakesTheCFO(t *testing.T) {
	// Arrange
	s, stateDir := grantBoard(t)
	asOverlordsBoard(s)
	card, err := s.acceptCredentialRequest(CredentialRequest{ID: "cred-0123456789abcdef", By: "cfo", Identity: strings.Repeat("c", 64), Task: "task-1", Project: "work", GrantService: "sample", Why: "Read the test service"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := state.ReadTaskMeta(stateDir, "task-1")
	if err != nil || !slices.Equal(before.Credentials, []string{"other"}) {
		t.Fatal("filing a card granted credentials")
	}

	// Act
	code, body := askTheBoard(t, s, "POST", "/api/connections/grant", `{"task":"task-1","generation":"g1","connection":"service:sample","action":"grant","request":"`+card.ID+`","request_generation":"`+card.Generation+`"}`, nil)

	// Assert
	after, err := state.ReadTaskMeta(stateDir, "task-1")
	closed, found := s.Store.credential(card.ID)
	if code != http.StatusOK || err != nil || !slices.Equal(after.Credentials, []string{"other", "sample"}) || !found || closed.State != "granted" {
		t.Fatalf("Grant = %d %s, grants %v, card %s (%v)", code, body, after.Credentials, closed.State, err)
	}
	pending, err := wake.Pending(stateDir)
	if err != nil || !slices.ContainsFunc(pending, func(record wake.Record) bool { return strings.Contains(record.Detail, "granted sample") }) {
		t.Fatal("the CFO was not told of the grant")
	}
}

func TestGrantRequestCardKeepsTheRecordedGrantAndRecoveryWhenScriptRefreshFails(t *testing.T) {
	// Arrange
	s, stateDir := grantBoard(t)
	asOverlordsBoard(s)
	card, err := s.acceptCredentialRequest(CredentialRequest{ID: "cred-0123456789abcdef", By: "cfo", Identity: strings.Repeat("c", 64), Task: "task-1", Project: "work", GrantService: "sample", Why: "Read the test service"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := state.ReadTaskMeta(stateDir, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(before.TaskTmp, state.AuthScriptName)
	const originalScript = "$env:STANDIN = 'unchanged'\n"
	if err := os.WriteFile(script, []byte(originalScript), 0600); err != nil {
		t.Fatal(err)
	}
	cleanupLock := state.CleanupLockName(before.ID)
	if _, err := lock.AcquireExclusiveNamed(stateDir, cleanupLock); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(stateDir, cleanupLock) })

	// Act
	code, body := askTheBoard(t, s, "POST", "/api/connections/grant", `{"task":"task-1","generation":"g1","connection":"service:sample","action":"grant","request":"`+card.ID+`","request_generation":"`+card.Generation+`"}`, nil)

	// Assert
	after, err := state.ReadTaskMeta(stateDir, "task-1")
	closed, found := s.Store.credential(card.ID)
	if code != http.StatusOK || err != nil || !slices.Equal(after.Credentials, []string{"other", "sample"}) || !found || closed.State != "granted" {
		t.Fatalf("Grant = %d %s, grants %v, card %s (%v)", code, body, after.Credentials, closed.State, err)
	}
	for _, want := range []string{"its auth.ps1 was not rewritten", "run cfo auth refresh task-1"} {
		if !strings.Contains(closed.Reason, want) {
			t.Errorf("card reason = %q, want %q", closed.Reason, want)
		}
	}
	content, err := os.ReadFile(script)
	if err != nil || string(content) != originalScript {
		t.Fatalf("script changed after failed refresh: %v", err)
	}
}

func TestGrantRequestFilingRequiresItsOwnProjectAndDeclaredService(t *testing.T) {
	for _, scenario := range []string{"cross-project", "undeclared", "stale generation", "different goblin"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange
			s, stateDir := grantBoard(t)
			request := CredentialRequest{ID: "cred-0123456789abcdef", By: "cfo", Identity: strings.Repeat("c", 64), Task: "task-1", TaskGeneration: "g1", Project: "work", GrantService: "sample", Why: "Read the test service"}
			switch scenario {
			case "cross-project":
				request.Project = "another-project"
			case "undeclared":
				request.GrantService = "missing"
			case "stale generation":
				request.TaskGeneration = "old"
			case "different goblin":
				request.By = "goblin"
			}

			// Act
			_, filingErr := s.acceptCredentialRequest(request)

			// Assert
			meta, err := state.ReadTaskMeta(stateDir, "task-1")
			if filingErr == nil || err != nil || !slices.Equal(meta.Credentials, []string{"other"}) || len(s.Store.Snapshot().Credentials) != 0 {
				t.Fatalf("filing = %v, grants %v (%v)", filingErr, meta.Credentials, err)
			}
		})
	}
}

func TestTaskCredentialTerminalGrantsOnlyAfterItStoredTheRequestedName(t *testing.T) {
	// Arrange
	s, stateDir := grantBoard(t)
	s.Options.Credentials = auth.OpenStore
	card, err := s.acceptCredentialRequest(CredentialRequest{ID: "cred-0123456789abcdef", By: "cfo", Identity: strings.Repeat("c", 64), Task: "task-1", Project: "work", Names: []string{"SAMPLE_TOKEN"}, Why: "Read the test service"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := auth.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(auth.Key{Project: "work", Name: "SAMPLE_TOKEN"}, newCanary(t)); err != nil {
		t.Fatal(err)
	}
	code := 0

	// Act
	s.credentialTerminalEnded(Run{CredentialRequest: card.ID, CredentialNames: []string{"SAMPLE_TOKEN"}}, &code)

	// Assert
	meta, err := state.ReadTaskMeta(stateDir, "task-1")
	if err != nil || !slices.Equal(meta.Credentials, []string{"other", "sample"}) {
		t.Fatalf("grants = %v (%v)", meta.Credentials, err)
	}
	closed, found := s.Store.credential(card.ID)
	if !found || closed.State != "saved" || !slices.Equal(closed.Typed, []string{"SAMPLE_TOKEN"}) {
		t.Fatal("the terminal's card did not record its stored name")
	}
}
