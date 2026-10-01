package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/terminal"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The CFO files a request over the supervisor's pipe, proven by its own
// process, and a goblin files one for its own task from inside its terminal,
// as cfo notify proves it. Both reach the board as names, labelled with who
// asked.
func TestTheCFOAndAGoblinFileCredentialRequestsProvenByTheirOwnProcess(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	t.Setenv(auth.StoreDirEnv, t.TempDir())
	_, identity, _, cfo := primaryFixture(t, store)
	service := &Service{Store: store, Options: Options{CFO: cfo, Credentials: auth.OpenStore}}
	runPipe(t, service)
	meta, _, _, goblin := goblinFixture(t, store)
	ctx := context.Background()

	// Act
	byCFO, cfoErr := FileCredentialRequest(ctx, h, cfo.Terminals, CredentialRequest{Project: "throwaway", Names: []string{"STRIPE_SECRET_KEY"}, Why: "Charge test cards", Link: "https://dashboard.stripe.com/apikeys"})
	byGoblin, goblinErr := FileCredentialRequest(ctx, h, goblin.Terminals, CredentialRequest{Task: meta.ID, Project: "throwaway", Names: []string{"RESEND_API_KEY"}, Why: "Send the receipt email"})
	ingestErr := service.ingestCredentialRequests()

	// Assert
	if cfoErr != nil || goblinErr != nil || ingestErr != nil {
		t.Fatalf("filing = %v, %v, %v", cfoErr, goblinErr, ingestErr)
	}
	requests := store.Snapshot().Credentials
	find := func(id string) CredentialRequest {
		t.Helper()
		i := slices.IndexFunc(requests, func(r CredentialRequest) bool { return r.ID == id })
		if i < 0 {
			t.Fatalf("request %s is not on the board: %+v", id, requests)
		}
		return requests[i]
	}
	if got := find(byCFO.ID); got.By != "cfo" || got.Identity != identity || got.State != "open" || !slices.Equal(got.Names, []string{"STRIPE_SECRET_KEY"}) {
		t.Fatalf("the CFO's request = %+v", got)
	}
	if got := find(byGoblin.ID); got.By != "goblin" || got.Task != meta.ID || got.Identity != goblinIdentity(meta) || !slices.Equal(got.Names, []string{"RESEND_API_KEY"}) {
		t.Fatalf("the goblin's request = %+v", got)
	}
}

// terminalBody is a request for the card's terminal, confirming replace.
func (b *credentialBoard) terminalBody(replace ...string) string {
	b.t.Helper()
	data, err := json.Marshal(struct {
		ID         string   `json:"id"`
		Generation string   `json:"generation"`
		Replace    []string `json:"replace,omitempty"`
	}{b.request.ID, b.request.Generation, replace})
	if err != nil {
		b.t.Fatal(err)
	}
	return string(data)
}

// credentialRun is the run item the board made for its request.
func (b *credentialBoard) credentialRun() Run {
	b.t.Helper()
	for _, r := range b.service.Store.Snapshot().Runs {
		if r.CredentialRequest == b.request.ID {
			return r
		}
	}
	b.t.Fatal("the board made no run for its request")
	return Run{}
}

// The card's Run opens a visible terminal on this PC that runs cfo auth store
// for each name the request still needs, which reads each value hidden, so a
// value never passes through the board. Once the terminal finishes, the rows
// it stored are checked, and the request closes when every name is stored.
func TestCredentialTerminalStoresEachValueHiddenAndChecksItsRows(t *testing.T) {
	// Arrange
	b := newCredentialBoard(t, "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET")
	launcher := &fakeRunLauncher{started: liveStart(t)}
	b.service.Options.Runs = launcher
	canaries := map[string]string{"STRIPE_SECRET_KEY": newCanary(t), "STRIPE_WEBHOOK_SECRET": newCanary(t)}
	ctx := context.Background()

	// Act
	opened := b.send(b.handler, "/api/credentials/terminal", b.terminalBody(), nil)
	run := b.credentialRun()
	if err := b.service.Store.ProcessOne(ctx, b.service.execute); err != nil {
		t.Fatal(err)
	}
	store, err := auth.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range canaries {
		if err := store.Set(auth.Key{Project: "throwaway", Name: name}, value); err != nil {
			t.Fatal(err)
		}
	}
	run = b.credentialRun()
	code := 0
	completeErr := b.service.completeRun(ctx, run, &code, "")

	// Assert
	if opened.Code != http.StatusOK || !strings.Contains(opened.Body.String(), run.ID) {
		t.Fatalf("Run = %d %s, want the run it opened", opened.Code, opened.Body)
	}
	if completeErr != nil {
		t.Fatal(completeErr)
	}
	launches := launcher.all()
	if len(launches) != 1 || launches[0].Shell != "powershell" || launches[0].Admin {
		t.Fatalf("launches = %+v, want one visible PowerShell window", launches)
	}
	script, err := os.ReadFile(launches[0].Script)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET"} {
		if !strings.Contains(string(script), "auth store --project 'throwaway' '"+name+"'") {
			t.Fatalf("the terminal's script does not store %s hidden:\n%s", name, script)
		}
	}
	if request := b.stored(); request.State != "saved" || !slices.Equal(request.Saved, []string{"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET"}) || !slices.Equal(request.Typed, []string{"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET"}) {
		t.Fatalf("request after its terminal = %+v, want both rows checked as typed and the request closed", request)
	}
	pending, err := wake.Pending(b.home.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || !strings.Contains(pending[0].Detail, "STRIPE_SECRET_KEY, STRIPE_WEBHOOK_SECRET stored for throwaway") || !strings.Contains(pending[0].Detail, "terminal") {
		t.Fatalf("wakes = %+v, want one telling the CFO the names typed in the terminal", pending)
	}
	for _, value := range canaries {
		if found := filesHolding(t, b.home.Root, value); len(found) != 0 {
			t.Fatalf("a typed value reached the home: %v", found)
		}
	}
}

// The card's Run takes the board's checks: only from this machine, only for
// the open request and its generation, asking before it replaces a stored
// value, one terminal at a time. A terminal that fails checks only a row it
// provably stored: a name that held no value before and holds one now.
func TestCredentialTerminalTakesTheBoardsChecksAndChecksOnlyWhatItStored(t *testing.T) {
	// Arrange
	b := newCredentialBoard(t, "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET")
	launcher := &fakeRunLauncher{started: liveStart(t)}
	b.service.Options.Runs = launcher
	store, err := auth.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(auth.Key{Project: "throwaway", Name: "STRIPE_WEBHOOK_SECRET"}, newCanary(t)); err != nil {
		t.Fatal(err)
	}
	path := "/api/credentials/terminal"

	// Act
	proxied := b.send(b.handler, path, b.terminalBody("STRIPE_WEBHOOK_SECRET"), func(r *http.Request) { r.Header.Set("X-Forwarded-For", "100.101.102.103") })
	other := b.send(b.handler, path, strings.Replace(b.terminalBody("STRIPE_WEBHOOK_SECRET"), b.request.Generation, strings.Repeat("0", len(b.request.Generation)), 1), nil)
	unconfirmed := b.send(b.handler, path, b.terminalBody(), nil)
	confirmed := b.send(b.handler, path, b.terminalBody("STRIPE_WEBHOOK_SECRET"), nil)
	again := b.send(b.handler, path, b.terminalBody("STRIPE_WEBHOOK_SECRET"), nil)
	if err := b.service.Store.ProcessOne(context.Background(), b.service.execute); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(auth.Key{Project: "throwaway", Name: "STRIPE_SECRET_KEY"}, newCanary(t)); err != nil {
		t.Fatal(err)
	}
	code := 1
	completeErr := b.service.completeRun(context.Background(), b.credentialRun(), &code, "")

	// Assert
	if proxied.Code != http.StatusForbidden || other.Code != http.StatusConflict {
		t.Fatalf("through a proxy = %d, another generation = %d; want both refused", proxied.Code, other.Code)
	}
	if unconfirmed.Code != http.StatusConflict || !strings.Contains(unconfirmed.Body.String(), `"existing":["STRIPE_WEBHOOK_SECRET"]`) {
		t.Fatalf("unconfirmed Run = %d %s, want it to ask before replacing", unconfirmed.Code, unconfirmed.Body)
	}
	if confirmed.Code != http.StatusOK || again.Code != http.StatusConflict {
		t.Fatalf("confirmed Run = %d %s, a second Run while it is open = %d; want one terminal", confirmed.Code, confirmed.Body, again.Code)
	}
	if completeErr != nil {
		t.Fatal(completeErr)
	}
	if launches := launcher.all(); len(launches) != 1 {
		t.Fatalf("launches = %d, want one terminal", len(launches))
	}
	if request := b.stored(); request.State != "open" || !slices.Equal(request.Saved, []string{"STRIPE_SECRET_KEY"}) {
		t.Fatalf("request after a failed terminal = %+v, want only the row it provably stored checked and the request open", request)
	}
}

// Any process of this Windows user can write the inbox and speak to the pipe.
// A request written into the inbox in the CFO's name, or in a goblin's name
// with an identity that is not its live generation's, and one sent over the
// pipe by a process outside the CFO's tree, never reach the board.
func TestForgedCredentialRequestsNeverReachTheBoard(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	t.Setenv(auth.StoreDirEnv, t.TempDir())
	cfoProcess := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-Command", "Start-Sleep -Seconds 120")
	cfoProcess.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	if err := cfoProcess.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cfoProcess.Process.Kill()
		_ = cfoProcess.Wait()
	})
	entries, err := proc.Ancestry(cfoProcess.Process.Pid, 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the stand-in CFO's start time: %v %v", entries, err)
	}
	hostname, _ := os.Hostname()
	writeRegistration(t, h.State, lock.Info{PID: cfoProcess.Process.Pid, Start: entries[0].Start, Hostname: hostname})
	connection := &CFOConnection{State: h.State, Terminals: terminal.HerdrSessions(&herdr.Client{Commands: &cfoRunner{t: t, pid: cfoProcess.Process.Pid}})}
	service := &Service{Store: store, Options: Options{CFO: connection, Credentials: auth.OpenStore}}
	runPipe(t, service)
	identity := forgedIdentity(t, h.State)
	now := time.Now().UTC()
	inbox := filepath.Join(h.State, credentialInbox)
	hashed := func(id string) string { sum := sha256.Sum256([]byte(id)); return hex.EncodeToString(sum[:]) + ".json" }
	plant(t, inbox, hashed("cred-1111111111111111"), CredentialRequest{ID: "cred-1111111111111111", Identity: identity, By: "cfo", Project: "throwaway", Names: []string{"STRIPE_SECRET_KEY"}, Why: "Forged as the CFO", CreatedAt: now})
	plant(t, inbox, hashed("cred-2222222222222222"), CredentialRequest{ID: "cred-2222222222222222", Identity: strings.Repeat("f", 64), By: "goblin", Task: "task-1", Project: "throwaway", Names: []string{"STRIPE_SECRET_KEY"}, Why: "Forged as a goblin", CreatedAt: now})

	// Act
	ingestErr := service.ingestCredentialRequests()
	_, filedErr := FileCredentialRequest(context.Background(), h, connection.Terminals, CredentialRequest{Project: "throwaway", Names: []string{"STRIPE_SECRET_KEY"}, Why: "Asked from outside the CFO"})
	forged := CredentialRequest{ID: "cred-3333333333333333", Identity: identity, By: "cfo", Project: "throwaway", Names: []string{"STRIPE_SECRET_KEY"}, Why: "Sent straight to the pipe"}
	pipeErr := sendPipeRequest(h.State, runPipeRequest{Kind: "credential", Credential: &forged})

	// Assert
	if ingestErr != nil {
		t.Fatal(ingestErr)
	}
	if filedErr == nil || pipeErr == nil || !strings.Contains(pipeErr.Error(), "does not run under the registered CFO") {
		t.Fatalf("filing from outside the CFO = %v, straight to the pipe = %v; want both refused", filedErr, pipeErr)
	}
	if requests := store.Snapshot().Credentials; len(requests) != 0 {
		t.Fatalf("forged requests reached the board: %+v", requests)
	}
	if entries, _ := os.ReadDir(inbox); len(entries) != 0 {
		t.Fatal("a forged request was left in the inbox")
	}
	issues := strings.Join(store.Snapshot().Issues, "\n")
	if !strings.Contains(issues, "only over the supervisor's pipe") || !strings.Contains(issues, "restarted or ended") {
		t.Fatalf("issues = %q, want both forgeries refused and named", issues)
	}
}
