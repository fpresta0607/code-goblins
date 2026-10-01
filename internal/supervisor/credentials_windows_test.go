package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
