package supervisor

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// connectorFakes stand in for the snapshots' checks, which count themselves and
// start nothing. A check made while a test says it is only reading fails it.
type connectorFakes struct {
	t         *testing.T
	isReading atomic.Bool
	mu        sync.Mutex
	counts    map[string]int
	// hold keeps the check of the project it names until it is closed.
	hold     chan struct{}
	holdName string
}

func (f *connectorFakes) add(key string) {
	f.t.Helper()
	if f.isReading.Load() {
		f.t.Errorf("%s was checked by a read", key)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts[key]++
}

func (f *connectorFakes) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[key]
}

func (f *connectorFakes) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sum int
	for _, count := range f.counts {
		sum += count
	}
	return sum
}

// connectorsReply is the page's answer.
type connectorsReply struct {
	Instance string            `json:"instance"`
	Machine  connections.Level `json:"machine"`
	Projects []struct {
		connections.Project
		BrowserLogins []browserLoginEntry `json:"browser_logins"`
	} `json:"projects"`
	Goblins []connections.Goblin `json:"goblins"`
}

// connectorBoard is a supervisor over the made-up projects acme and globex
// whose manifests, checkouts and credential store are in the test.
type connectorBoard struct {
	service  *Service
	handler  *HTTP
	home     string
	state    string
	projects string
	fakes    *connectorFakes
	store    *Store
}

const acmeManifest = `{"project":"acme","services":[
	{"name":"sample","method":"oauth","url":"https://login.acme.example/start"},
	{"name":"tokens","method":"env","env":["ACME_TOKEN"]},
	{"name":"tool","method":"cli","login":["acme-cli","login"]}
]}`

// newConnectorBoard makes the board, with the snapshots' checks replaced by
// fakes that answer the items a fix needs of acme unless real says to run the
// project check as it is.
func newConnectorBoard(t *testing.T, isRealProjectCheck bool) *connectorBoard {
	t.Helper()
	store, h := testStore(t)
	projects := filepath.Join(h.Root, "projects")
	t.Setenv(install.ProjectsRootVariable, projects)
	t.Setenv(auth.StoreDirEnv, filepath.Join(h.Root, "credentials"))
	for name, manifest := range map[string]string{"acme": acmeManifest, "globex": `{"project":"globex","services":[{"name":"tokens","method":"env","env":["GLOBEX_TOKEN"]}]}`} {
		if err := os.MkdirAll(filepath.Join(projects, name), 0o700); err != nil {
			t.Fatal(err)
		}
		path := auth.ManifestPath(h.Data, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{Store: store, Instance: "test-instance"}
	fakes := &connectorFakes{t: t, counts: map[string]int{}}
	snapshots := service.connectors()
	t.Cleanup(snapshots.Close)
	snapshots.CheckMachine = func(context.Context) connections.Level {
		fakes.add("machine")
		return connections.Level{Items: []connections.Item{{ID: "cli:acme-cli", Level: connections.LevelPC, Name: "acme-cli", Status: connections.StatusSet}}}
	}
	if !isRealProjectCheck {
		snapshots.CheckProject = func(ctx context.Context, name, _ string) connections.Level {
			fakes.add("project:" + name)
			if fakes.hold != nil && name == fakes.holdName {
				select {
				case <-fakes.hold:
				case <-ctx.Done():
				}
			}
			if name != "acme" {
				return connections.Level{Items: []connections.Item{}}
			}
			return connections.Level{Items: []connections.Item{
				{ID: "service:sample", Name: "sample", Status: connections.StatusUnauthorized, Actions: []string{"login", "cli"}},
				{ID: "service:tokens", Name: "tokens", Status: connections.StatusMissing, Actions: []string{"store:ACME_TOKEN", "store:LOOSE_TOKEN"}},
				{ID: "service:tool", Name: "tool", Status: connections.StatusUnauthorized, Actions: []string{"cli"}},
			}}
		}
	}
	snapshots.ReadGoblin = func(meta state.TaskMeta, _ string, _ int64, _ connections.Goblin) (connections.Goblin, int64) {
		fakes.add("goblin:" + meta.ID)
		return connections.Goblin{Task: meta.ID, Generation: meta.SpawnGen, Harness: meta.Harness, Project: auth.ProjectName(meta.Project)}, 0
	}
	snapshots.Transcript = func(state.TaskMeta) string { return "" }
	return &connectorBoard{service: service, handler: NewHTTP(service, "board.local", nil), home: h.Root, state: h.State, projects: projects, fakes: fakes, store: store}
}

// ask sends the page's request, with the headers of a board of its own unless
// change says otherwise.
func (b *connectorBoard) ask(t *testing.T, method, path, body string, change func(*http.Request)) (int, string) {
	t.Helper()
	request := httptest.NewRequest(method, "http://board.local"+path, strings.NewReader(body))
	if method != http.MethodGet {
		request.Header.Set("Origin", "http://board.local")
		request.Header.Set("X-CEO-Token", "test-instance")
		request.Header.Set("Content-Type", "application/json")
	}
	if change != nil {
		change(request)
	}
	response := httptest.NewRecorder()
	b.handler.ServeHTTP(response, request)
	return response.Code, response.Body.String()
}

func (b *connectorBoard) read(t *testing.T) connectorsReply {
	t.Helper()
	code, body := b.ask(t, http.MethodGet, "/api/connectors", "", nil)
	var reply connectorsReply
	if code != http.StatusOK || json.Unmarshal([]byte(body), &reply) != nil {
		t.Fatalf("read = %d %s", code, body)
	}
	return reply
}

// checked waits until the project's check has ended, the project listed.
func (b *connectorBoard) checked(t *testing.T, project string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		for _, listed := range b.read(t).Projects {
			if listed.Name == project && !listed.Checking && !listed.CheckedAt.IsZero() {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("project %s was not checked in a minute", project)
		}
		time.Sleep(time.Millisecond)
	}
}

// machineChecked waits until the PC's check has ended.
func (b *connectorBoard) machineChecked(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for machine := b.read(t).Machine; machine.Checking || machine.CheckedAt.IsZero(); machine = b.read(t).Machine {
		if time.Now().After(deadline) {
			t.Fatal("the PC was not checked in a minute")
		}
		time.Sleep(time.Millisecond)
	}
}

func (b *connectorBoard) refresh(t *testing.T, level, scope string) {
	t.Helper()
	body, err := json.Marshal(connectorRefreshRequest{Level: level, Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if code, answer := b.ask(t, http.MethodPost, "/api/connectors/refresh", string(body), nil); code != http.StatusOK {
		t.Fatalf("refresh %s %s = %d %s", level, scope, code, answer)
	}
}

// The watch the supervisor starts reads every goblin whose records say it runs,
// one with no lifecycle record among them, and no paused one, and checks this
// PC.
func TestConnectorsWatchReadsOnlyRunningGoblinsAndChecksThePC(t *testing.T) {
	// Arrange
	b := newConnectorBoard(t, false)
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for _, task := range []string{"acme-running", "acme-unrecorded", "acme-paused"} {
		meta := state.TaskMeta{ID: task, SpawnGen: "generation-" + task, Harness: "codex", Backend: "native", Project: filepath.Join(b.projects, "acme")}
		if err := state.WriteTaskMeta(b.state, meta); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.WriteLifecycle(b.state, state.Lifecycle{ID: "acme-running", Generation: "generation-acme-running", Operation: "resume-acme-running", Action: "resume", Phase: "running", Started: at, Updated: at}); err != nil {
		t.Fatal(err)
	}
	condition, err := state.NewPauseCondition("overlord", "", at)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(b.state, state.Lifecycle{ID: "acme-paused", Generation: "generation-acme-paused", Operation: "pause-acme-paused", Action: "pause", Phase: "paused", Started: at, Updated: at, Pause: &condition}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		b.service.closeConnectors()
	})

	// Act
	b.service.startConnectors(ctx)
	b.machineChecked(t)
	reply := b.read(t)

	// Assert
	var goblins []string
	for _, goblin := range reply.Goblins {
		if strings.HasPrefix(goblin.Task, "acme-") {
			goblins = append(goblins, goblin.Task)
		}
	}
	slices.Sort(goblins)
	if !slices.Equal(goblins, []string{"acme-running", "acme-unrecorded"}) {
		t.Fatalf("goblins = %v, want the running one and the one with no lifecycle record", goblins)
	}
	if got := b.fakes.count("goblin:acme-paused"); got != 0 {
		t.Errorf("the paused goblin was read %d times, want none", got)
	}
	if got := b.fakes.count("machine"); got != 1 {
		t.Errorf("the PC was checked %d times, want once", got)
	}
}

// Reading the page asks for no check: however often it is read, and whatever it
// is read for, no check is made and nothing is started.
func TestConnectorsReadChecksNothing(t *testing.T) {
	// Arrange
	b := newConnectorBoard(t, false)
	b.refresh(t, connections.RefreshMachine, "")
	b.refresh(t, connections.RefreshProject, "acme")
	b.checked(t, "acme")
	b.fakes.isReading.Store(true)
	before := b.fakes.total()

	// Act
	var reply connectorsReply
	for range 100 {
		reply = b.read(t)
	}

	// Assert
	if b.fakes.total() != before || before != 2 {
		t.Fatalf("checks = %d after the reads and %d before them, want the two the refreshes made and none for a read", b.fakes.total(), before)
	}
	if len(reply.Projects) != 1 || reply.Projects[0].Name != "acme" || len(reply.Projects[0].Items) != 3 || reply.Instance != "test-instance" || len(reply.Machine.Items) != 1 {
		t.Fatalf("reply = %+v, want the one checked project and the machine", reply)
	}
	if reply.Projects[0].BrowserLogins == nil {
		t.Error("browser_logins is null, want a list")
	}
}

func TestConnectorsRefreshChecksOnlyTheLevelNamed(t *testing.T) {
	cases := []struct {
		name, level, scope string
		want               map[string]int
	}{
		{"the machine", connections.RefreshMachine, "", map[string]int{"machine": 1}},
		{"one project", connections.RefreshProject, "acme", map[string]int{"project:acme": 1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := newConnectorBoard(t, false)

			// Act
			b.refresh(t, test.level, test.scope)
			if test.level == connections.RefreshProject {
				b.checked(t, test.scope)
			} else {
				b.machineChecked(t)
			}

			// Assert
			for key, want := range test.want {
				if got := b.fakes.count(key); got != want {
					t.Errorf("%s checked %d times, want %d", key, got, want)
				}
			}
			if b.fakes.total() != 1 {
				t.Errorf("%d checks, want only the one asked for", b.fakes.total())
			}
		})
	}
}

func TestConnectorsRefreshRefusesWhatIsNotAskedForRightOrNotListed(t *testing.T) {
	cases := []struct {
		name, body, origin, token string
		want                      int
	}{
		{"unknown level", `{"level":"pc","scope":""}`, "http://board.local", "test-instance", http.StatusBadRequest},
		{"unlisted project", `{"level":"project","scope":"initech"}`, "http://board.local", "test-instance", http.StatusNotFound},
		{"unlisted goblin", `{"level":"goblin","scope":"no-such-task"}`, "http://board.local", "test-instance", http.StatusNotFound},
		{"a field the request does not have", `{"level":"project","scope":"acme","command":"evil"}`, "http://board.local", "test-instance", http.StatusBadRequest},
		{"not JSON", `level=project`, "http://board.local", "test-instance", http.StatusBadRequest},
		{"another origin", `{"level":"project","scope":"acme"}`, "http://elsewhere", "test-instance", http.StatusForbidden},
		{"wrong token", `{"level":"project","scope":"acme"}`, "http://board.local", "wrong", http.StatusForbidden},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := newConnectorBoard(t, false)

			// Act
			code, body := b.ask(t, http.MethodPost, "/api/connectors/refresh", test.body, func(request *http.Request) {
				request.Header.Set("Origin", test.origin)
				request.Header.Set("X-CEO-Token", test.token)
			})

			// Assert
			if code != test.want || b.fakes.total() != 0 {
				t.Fatalf("refresh = %d %s after %d checks, want %d and no check", code, body, b.fakes.total(), test.want)
			}
		})
	}
}

// The fix endpoint makes the repair the project's item offers and nothing the
// request brings: the manifest's sign-in page, a card that stores the clipboard
// under a name the manifest declares, or a card that runs the service's login.
func TestConnectorsFixMakesOnlyTheRepairTheProjectItemOffers(t *testing.T) {
	cases := []struct {
		name, body string
		want       int
	}{
		{"sign-in page", `{"project":"acme","connection":"service:sample","action":"login"}`, 200},
		{"clipboard store", `{"project":"acme","connection":"service:tokens","action":"store:ACME_TOKEN"}`, 200},
		{"login command", `{"project":"acme","connection":"service:tool","action":"cli"}`, 200},
		{"action the item offers and the manifest cannot do", `{"project":"acme","connection":"service:sample","action":"cli"}`, 409},
		{"variable no manifest declares", `{"project":"acme","connection":"service:tokens","action":"store:LOOSE_TOKEN"}`, 409},
		{"action the item does not offer", `{"project":"acme","connection":"service:tool","action":"login"}`, 409},
		{"project that is not listed", `{"project":"initech","connection":"service:sample","action":"login"}`, 409},
		{"a command in the request", `{"project":"acme","connection":"service:tool","action":"cli","command":"evil"}`, 400},
		{"a value in the request", `{"project":"acme","connection":"service:tokens","action":"store:ACME_TOKEN","value":"pasted-secret-1"}`, 400},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := newConnectorBoard(t, false)
			b.refresh(t, connections.RefreshProject, "acme")
			b.checked(t, "acme")
			before := len(b.store.Snapshot().Runs)

			// Act
			code, body := b.ask(t, http.MethodPost, "/api/connectors/fix", test.body, nil)

			// Assert
			runs := b.store.Snapshot().Runs
			if code != test.want {
				t.Fatalf("fix = %d %s, want %d", code, body, test.want)
			}
			if test.want != 200 {
				if len(runs) != before {
					t.Fatalf("a refused fix recorded a card: %d runs, want %d", len(runs), before)
				}
				return
			}
			var reply map[string]string
			if err := json.Unmarshal([]byte(body), &reply); err != nil {
				t.Fatal(err)
			}
			if hasURL, hasCard := reply["url"] != "", reply["run_id"] != ""; hasURL == hasCard {
				t.Fatalf("reply = %v, want a sign-in page or a card, not both or neither", reply)
			}
			if reply["url"] != "" {
				if reply["url"] != "https://login.acme.example/start" || len(runs) != before {
					t.Fatalf("reply = %v with %d runs, want the manifest's page and no card", reply, len(runs))
				}
				return
			}
			var run Run
			for _, made := range runs {
				if made.ID == reply["run_id"] {
					run = made
				}
			}
			project := filepath.Join(b.projects, "acme")
			if run.ID == "" || run.ConnectionProject != "acme" || run.Cwd != project || strings.Contains(run.Command, "Set-Clipboard") {
				t.Fatalf("card = %+v, want one for acme that runs in its checkout and never writes the clipboard", run)
			}
			isStore := strings.Contains(test.body, "store:")
			if isStore && (!strings.Contains(run.Command, "Get-Clipboard -Raw | & ") || !strings.HasSuffix(run.Command, " auth store --project '"+project+"' 'ACME_TOKEN'\nexit $LASTEXITCODE\n") || run.Title != "Store ACME_TOKEN from clipboard for acme") {
				t.Fatalf("card = %q %q, want the clipboard stored under ACME_TOKEN for acme", run.Title, run.Command)
			}
			if !isStore && !strings.Contains(run.Command, " connection-repair '"+project+"' 'service:tool' 'cli'\n") {
				t.Fatalf("card command = %q, want goblins connection-repair for the project, connection and action", run.Command)
			}
		})
	}
}

// A project whose check is still running cannot be fixed from what it showed
// before: the page says to wait.
func TestConnectorsFixWaitsForAProjectThatIsBeingChecked(t *testing.T) {
	// Arrange
	b := newConnectorBoard(t, false)
	b.refresh(t, connections.RefreshProject, "acme")
	b.checked(t, "acme")
	b.fakes.hold, b.fakes.holdName = make(chan struct{}), "acme"
	var release sync.Once
	unblock := func() { release.Do(func() { close(b.fakes.hold) }) }
	t.Cleanup(unblock)
	b.refresh(t, connections.RefreshProject, "acme")

	// Act
	code, body := b.ask(t, http.MethodPost, "/api/connectors/fix", `{"project":"acme","connection":"service:sample","action":"login"}`, nil)
	unblock()

	// Assert
	if code != http.StatusConflict || !strings.Contains(body, "Wait for the connection check") {
		t.Fatalf("fix = %d %s, want it refused until the check ends", code, body)
	}
}

// A repair that ended checks its project again, and its run item says whose it
// is.
func TestCompletedConnectionRepairChecksItsProjectAgainAndPublishesTheOwner(t *testing.T) {
	// Arrange
	b := newConnectorBoard(t, false)
	b.refresh(t, connections.RefreshProject, "acme")
	b.checked(t, "acme")
	project := connections.Project{Name: "acme", Path: filepath.Join(b.projects, "acme")}
	run, err := b.service.connectionRun(project, connectorFixRequest{Project: "acme", Connection: "service:tokens", Action: "store:ACME_TOKEN"}, connections.Repair{Credential: "ACME_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	b.store.mu.Lock()
	for index := range b.store.db.Runs {
		b.store.db.Runs[index].RunAction = "action"
		b.store.db.Runs[index].State = "running"
	}
	b.store.mu.Unlock()
	run.RunAction = "action"
	code := 0

	// Act
	err = b.service.completeRun(context.Background(), run, &code, "")
	deadline := time.Now().Add(time.Minute)
	for b.fakes.count("project:acme") < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	b.checked(t, "acme")

	// Assert
	if err != nil {
		t.Fatalf("completion error = %v", err)
	}
	if got := b.fakes.count("project:acme"); got != 2 {
		t.Fatalf("acme checked %d times, want a recheck after the repair", got)
	}
	data, err := json.Marshal(b.store.Snapshot().Runs[0])
	if err != nil || !strings.Contains(string(data), `"connection_project":"acme"`) || !strings.Contains(string(data), `"finished_at"`) {
		t.Fatalf("published run = %s, %v", data, err)
	}
}

// A clipboard card never reads the clipboard itself, and says whether it stores
// a variable for its project.
func TestConnectionClipboardCardNeverWritesTheClipboardAndNamesTheProject(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	service := &Service{Store: store, Instance: "test-instance"}
	project := connections.Project{Name: "acme", Path: filepath.Join(h.Root, "acme")}
	if err := os.MkdirAll(project.Path, 0o700); err != nil {
		t.Fatal(err)
	}

	// Act
	run, err := service.connectionRun(project, connectorFixRequest{Project: "acme", Connection: "service:tokens", Action: "store:ACME_TOKEN"}, connections.Repair{Credential: "ACME_TOKEN"})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if run.Title != "Store ACME_TOKEN from clipboard for acme" || run.ConnectionProject != "acme" || run.Cwd != project.Path {
		t.Fatalf("card = %q for %q in %q", run.Title, run.ConnectionProject, run.Cwd)
	}
	if !strings.Contains(run.Command, "Get-Clipboard -Raw | & ") || strings.Contains(run.Command, "Set-Clipboard") || !strings.HasSuffix(run.Command, " auth store --project '"+project.Path+"' 'ACME_TOKEN'\nexit $LASTEXITCODE\n") {
		t.Fatalf("not the clipboard store: %q", run.Command)
	}
}

// No credential value reaches the page's answers or the snapshot files kept
// under the state folder: a stored credential and a process variable are
// planted, the real project check runs against them, and the refresh's answer,
// the read and every file are searched.
func TestConnectorsAnswersAndFilesHoldNoCredentialValue(t *testing.T) {
	// Arrange
	const storedSecret, environmentSecret = "stored-secret-8d41f0", "process-secret-02be7c"
	b := newConnectorBoard(t, true)
	t.Setenv("ACME_TOKEN", environmentSecret)
	credentials, err := auth.OpenFileStore(os.Getenv(auth.StoreDirEnv))
	if err != nil {
		t.Fatal(err)
	}
	if err := credentials.Set(auth.Scoped("acme", "ACME_TOKEN"), storedSecret); err != nil {
		t.Fatal(err)
	}

	// Act
	_, refresh := b.ask(t, http.MethodPost, "/api/connectors/refresh", `{"level":"project","scope":"acme"}`, nil)
	b.checked(t, "acme")
	_, read := b.ask(t, http.MethodGet, "/api/connectors", "", nil)
	answers := []string{refresh, read}
	var files strings.Builder
	err = filepath.WalkDir(filepath.Join(b.state, connectorsFolder), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		data, readErr := os.ReadFile(path)
		files.Write(data)
		return readErr
	})

	// Assert
	if err != nil || files.Len() == 0 {
		t.Fatalf("snapshot files = %d bytes (%v), want the project's", files.Len(), err)
	}
	var reply connectorsReply
	if json.Unmarshal([]byte(read), &reply) != nil || len(reply.Projects) != 1 {
		t.Fatalf("read = %s", read)
	}
	var tokens connections.Item
	for _, item := range reply.Projects[0].Items {
		if item.ID == "service:tokens" {
			tokens = item
		}
	}
	if tokens.Status != connections.StatusSet || len(tokens.Credentials) != 1 || !tokens.Credentials[0].IsResolved {
		t.Fatalf("tokens = %+v, want its stored variable resolved, so the check read the planted values", tokens)
	}
	for _, text := range append(answers, files.String()) {
		for _, secret := range []string{storedSecret, environmentSecret} {
			if strings.Contains(text, secret) {
				t.Fatalf("the value %q reached an answer or a snapshot file", secret)
			}
		}
	}
}
