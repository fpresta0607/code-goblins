package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
// the open request and its generation, replacing only a stored value it
// confirms, one terminal at a time. A confirmed one runs beside the names the
// scope does not hold. A terminal that fails checks only a row it provably
// stored: a name that held no value before and holds one now.
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
	unheld := b.send(b.handler, path, b.terminalBody("STRIPE_SECRET_KEY"), nil)
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
	if unheld.Code != http.StatusBadRequest {
		t.Fatalf("Run confirming a name the scope does not hold = %d %s, want it refused", unheld.Code, unheld.Body)
	}
	if confirmed.Code != http.StatusOK || again.Code != http.StatusConflict {
		t.Fatalf("confirmed Run = %d %s, a second Run while it is open = %d; want one terminal", confirmed.Code, confirmed.Body, again.Code)
	}
	if completeErr != nil {
		t.Fatal(completeErr)
	}
	launches := launcher.all()
	if len(launches) != 1 {
		t.Fatalf("launches = %d, want one terminal", len(launches))
	}
	script, err := os.ReadFile(launches[0].Script)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET"} {
		if !strings.Contains(string(script), "auth store --project 'throwaway' '"+name+"'") {
			t.Fatalf("the confirmed terminal's script does not store %s:\n%s", name, script)
		}
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

// openTerminal presses the card's Run, confirming replace, and starts the
// terminal it queued.
func (b *credentialBoard) openTerminal(replace ...string) Run {
	b.t.Helper()
	if response := b.send(b.handler, "/api/credentials/terminal", b.terminalBody(replace...), nil); response.Code != http.StatusOK {
		b.t.Fatalf("Run = %d %s", response.Code, response.Body)
	}
	if err := b.service.Store.ProcessOne(context.Background(), b.service.execute); err != nil {
		b.t.Fatal(err)
	}
	return b.credentialRun()
}

// typeValue stores a canary for name in the throwaway scope, as the
// terminal's cfo auth store does.
func (b *credentialBoard) typeValue(name string) {
	b.t.Helper()
	store, err := auth.OpenStore()
	if err != nil {
		b.t.Fatal(err)
	}
	if err := store.Set(auth.Key{Project: "throwaway", Name: name}, newCanary(b.t)); err != nil {
		b.t.Fatal(err)
	}
}

// endTerminal ends the request's running terminal with code.
func (b *credentialBoard) endTerminal(code int) {
	b.t.Helper()
	if err := b.service.completeRun(context.Background(), b.credentialRun(), &code, ""); err != nil {
		b.t.Fatal(err)
	}
}

// pendingDetails are the details of the CFO's pending wakes, in order.
func (b *credentialBoard) pendingDetails() []string {
	b.t.Helper()
	pending, err := wake.Pending(b.home.State)
	if err != nil {
		b.t.Fatal(err)
	}
	var details []string
	for _, record := range pending {
		details = append(details, record.Detail)
	}
	return details
}

// A Run for a request whose scope already holds one of its names opens a
// terminal for the other alone, without asking, and leaves the stored value
// as it is. A clean finish closes the request; a failed one leaves it open
// with only the stored name left, which a Run then asks before replacing.
func TestCredentialTerminalSkipsAStoredNameUnlessConfirmed(t *testing.T) {
	for _, test := range []struct {
		name  string
		code  int
		state string
		again string
	}{
		{"a clean finish", 0, "saved", "already saved"},
		{"a failed finish", 1, "open", `"existing":["STRIPE_WEBHOOK_SECRET"]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := newCredentialBoard(t, "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET")
			b.service.Options.Runs = &fakeRunLauncher{started: liveStart(t)}
			b.typeValue("STRIPE_WEBHOOK_SECRET")
			held, _ := b.vaultValue("STRIPE_WEBHOOK_SECRET")

			// Act
			run := b.openTerminal()
			b.typeValue("STRIPE_SECRET_KEY")
			b.endTerminal(test.code)
			again := b.send(b.handler, "/api/credentials/terminal", b.terminalBody(), nil)

			// Assert
			if !slices.Equal(run.CredentialNames, []string{"STRIPE_SECRET_KEY"}) {
				t.Fatalf("the terminal runs for %v, want the name the scope does not hold alone", run.CredentialNames)
			}
			if request := b.stored(); request.State != test.state || !slices.Equal(request.Saved, []string{"STRIPE_SECRET_KEY"}) || len(request.Replaced) != 0 {
				t.Fatalf("request after its terminal = %+v, want it %s with only the typed name saved", request, test.state)
			}
			if got, _ := b.vaultValue("STRIPE_WEBHOOK_SECRET"); got != held {
				t.Fatal("the terminal changed the value the scope already held")
			}
			if again.Code != http.StatusConflict || !strings.Contains(again.Body.String(), test.again) {
				t.Fatalf("Run after the terminal = %d %s, want it refused with %s", again.Code, again.Body, test.again)
			}
		})
	}
}

// While a request's terminal is open, a card save is refused and stores
// nothing, and the request does not expire under it. When the terminal stores
// one name and fails, that name is recorded and reported, and the request then
// expires naming only the name never stored.
func TestCredentialTerminalHoldsOffTheSaveAndExpiryUntilItEnds(t *testing.T) {
	// Arrange
	b := newCredentialBoard(t, "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET")
	b.service.Options.Runs = &fakeRunLauncher{started: liveStart(t)}
	canary := newCanary(t)
	b.openTerminal()

	// Act
	saved := b.post(b.handler, b.body(map[string]string{"STRIPE_SECRET_KEY": canary}), nil)
	_, savedValue := b.vaultValue("STRIPE_SECRET_KEY")
	b.service.Store.mu.Lock()
	b.service.Store.db.Credentials[0].ExpiresAt = time.Now().Add(-time.Minute)
	b.service.Store.mu.Unlock()
	expireErr := b.service.expireCredentials(time.Now())
	whileOpen := b.stored()
	b.typeValue("STRIPE_SECRET_KEY")
	b.endTerminal(1)
	afterTerminal := b.stored()
	laterErr := b.service.expireCredentials(time.Now())

	// Assert
	if saved.Code != http.StatusConflict || !strings.Contains(saved.Body.String(), "A terminal for this request is open") || strings.Contains(saved.Body.String(), canary) {
		t.Fatalf("save while the terminal is open = %d %s, want it refused naming the terminal", saved.Code, saved.Body)
	}
	if savedValue {
		t.Fatal("a save refused while the terminal is open stored a value")
	}
	if expireErr != nil || laterErr != nil {
		t.Fatal(expireErr, laterErr)
	}
	if whileOpen.State != "open" {
		t.Fatalf("request past its expiry with its terminal open = %+v, want it open", whileOpen)
	}
	if afterTerminal.State != "open" || !slices.Equal(afterTerminal.Saved, []string{"STRIPE_SECRET_KEY"}) {
		t.Fatalf("request after its terminal failed = %+v, want the typed name recorded", afterTerminal)
	}
	if request := b.stored(); request.State != "expired" || !slices.Equal(request.Saved, []string{"STRIPE_SECRET_KEY"}) {
		t.Fatalf("request after its terminal ended = %+v, want it expired with the typed name kept", request)
	}
	details := b.pendingDetails()
	if len(details) != 2 || !strings.HasPrefix(details[0], "STRIPE_SECRET_KEY stored for throwaway in a terminal") || !strings.HasPrefix(details[1], "STRIPE_WEBHOOK_SECRET for throwaway: the credential request") || strings.Contains(details[1], "STRIPE_SECRET_KEY") {
		t.Fatalf("wakes = %q, want the typed name reported, then only the other named as expired unsaved", details)
	}
}

// A card save after a terminal stored one name and failed adds its name to
// the one the terminal stored, and the CFO hears the name the save stored.
func TestCredentialSaveAfterAFailedTerminalAddsToWhatItStored(t *testing.T) {
	// Arrange
	b := newCredentialBoard(t, "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET")
	b.service.Options.Runs = &fakeRunLauncher{started: liveStart(t)}
	b.openTerminal()
	b.typeValue("STRIPE_SECRET_KEY")
	b.endTerminal(1)
	canary := newCanary(t)

	// Act
	response := b.post(b.handler, b.body(map[string]string{"STRIPE_WEBHOOK_SECRET": canary}), nil)
	b.service.credentialWork.Wait()

	// Assert
	if response.Code != http.StatusOK {
		t.Fatalf("save = %d %s", response.Code, response.Body)
	}
	if request := b.stored(); request.State != "saved" || !slices.Equal(request.Saved, []string{"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET"}) || !slices.Equal(request.Typed, []string{"STRIPE_SECRET_KEY"}) {
		t.Fatalf("request after the save = %+v, want both names saved and the terminal's typed", request)
	}
	details := b.pendingDetails()
	if len(details) != 2 || !strings.HasPrefix(details[1], "STRIPE_WEBHOOK_SECRET stored for throwaway from the board") || strings.Contains(details[1], canary) {
		t.Fatalf("wakes = %q, want the save's notice to name the name it stored", details)
	}
}

// A goblin's request filed while the board holds its most open requests waits
// in the inbox, and the board takes it once one of them closes.
func TestGoblinCredentialRequestWaitsInTheInboxWhileTheBoardIsFull(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	t.Setenv(auth.StoreDirEnv, t.TempDir())
	service := &Service{Store: store, Options: Options{Credentials: auth.OpenStore}}
	meta, _, _, goblin := goblinFixture(t, store)
	for i := range maxCredentialRequests {
		if _, err := service.acceptCredentialRequest(CredentialRequest{ID: fmt.Sprintf("cred-%016x", i), Identity: strings.Repeat("c", 64), By: "cfo", Project: "throwaway", Names: []string{fmt.Sprintf("NAME_%d", i)}, Why: "Fill the board"}); err != nil {
			t.Fatal(err)
		}
	}
	filed, err := FileCredentialRequest(context.Background(), h, goblin.Terminals, CredentialRequest{Task: meta.ID, Project: "throwaway", Names: []string{"RESEND_API_KEY"}, Why: "Send the receipt email"})
	if err != nil {
		t.Fatal(err)
	}
	inbox := filepath.Join(h.State, credentialInbox)
	onBoard := func() bool {
		return slices.ContainsFunc(store.Snapshot().Credentials, func(r CredentialRequest) bool { return r.ID == filed.ID })
	}

	// Act
	fullErr := service.ingestCredentialRequests()
	waiting, _ := os.ReadDir(inbox)
	takenWhileFull := onBoard()
	now := time.Now().UTC()
	if _, err := store.updateCredential(fmt.Sprintf("cred-%016x", 0), func(r *CredentialRequest) { r.State, r.ClosedAt = "saved", &now }); err != nil {
		t.Fatal(err)
	}
	roomErr := service.ingestCredentialRequests()
	left, _ := os.ReadDir(inbox)

	// Assert
	if fullErr != nil || roomErr != nil {
		t.Fatal(fullErr, roomErr)
	}
	if len(waiting) != 1 || takenWhileFull {
		t.Fatalf("while the board is full: %d inbox files, request on the board = %v; want it waiting in the inbox", len(waiting), takenWhileFull)
	}
	if !onBoard() || len(left) != 0 {
		t.Fatalf("once a request closed: request on the board = %v, %d inbox files; want it taken", onBoard(), len(left))
	}
	if issues := strings.Join(store.Snapshot().Issues, "\n"); strings.Contains(issues, "Credential request refused") {
		t.Fatalf("issues = %q, want the waiting request never refused", issues)
	}
}
