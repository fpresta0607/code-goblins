package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const credentialBoardHost = "127.0.0.1:4310"

// credentialBoard is a board with one open credential request for the
// throwaway scope, a file credential store outside the home, and a refresh
// that records each project it was asked to refresh.
type credentialBoard struct {
	t         *testing.T
	service   *Service
	handler   *HTTP
	home      home.Home
	vault     string
	request   CredentialRequest
	mu        sync.Mutex
	refreshed []string
}

func newCredentialBoard(t *testing.T, names ...string) *credentialBoard {
	t.Helper()
	return newCredentialBoardFor(t, CredentialRequest{ID: "cred-0123456789abcdef", Identity: strings.Repeat("c", 64), By: "cfo", Project: "throwaway", Names: names, Why: "Charge test cards in the checkout tests", Link: "https://dashboard.stripe.com/apikeys"})
}

// newCredentialBoardFor is a board that took the CFO's request filed.
func newCredentialBoardFor(t *testing.T, filed CredentialRequest) *credentialBoard {
	t.Helper()
	store, h := testStore(t)
	b := &credentialBoard{t: t, home: h, vault: t.TempDir()}
	t.Setenv(auth.StoreDirEnv, b.vault)
	b.service = &Service{Store: store, Instance: "test-instance", Options: Options{
		Credentials: auth.OpenStore,
		RefreshCredentials: func(_ context.Context, project string) ([]string, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.refreshed = append(b.refreshed, project)
			return []string{"task-1"}, nil
		},
	}}
	b.handler = NewHTTP(b.service, credentialBoardHost, nil)
	request, err := b.service.acceptCredentialRequest(filed)
	if err != nil {
		t.Fatal(err)
	}
	b.request = request
	t.Cleanup(b.service.credentialWork.Wait)
	return b
}

// newCanary is a value no file, log or response holds unless the code under
// test put it there.
func newCanary(t *testing.T) string {
	t.Helper()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	return "canary" + hex.EncodeToString(random[:])
}

// body is a save of values for the board's request, confirming replace.
func (b *credentialBoard) body(values map[string]string, replace ...string) string {
	b.t.Helper()
	data, err := json.Marshal(struct {
		ID         string            `json:"id"`
		Generation string            `json:"generation"`
		Values     map[string]string `json:"values"`
		Replace    []string          `json:"replace,omitempty"`
	}{b.request.ID, b.request.Generation, values, replace})
	if err != nil {
		b.t.Fatal(err)
	}
	return string(data)
}

// post sends a save the way the board's own page on this machine does, after
// change has had its say over the request.
func (b *credentialBoard) post(handler *HTTP, body string, change func(*http.Request)) *httptest.ResponseRecorder {
	b.t.Helper()
	return b.send(handler, "/api/credentials/save", body, change)
}

// send posts body to path the way the board's own page on this machine does.
func (b *credentialBoard) send(handler *HTTP, path, body string, change func(*http.Request)) *httptest.ResponseRecorder {
	b.t.Helper()
	request := httptest.NewRequest("POST", "http://"+handler.Host+path, strings.NewReader(body))
	request.Host = handler.Host
	request.RemoteAddr = "127.0.0.1:50000"
	request.Header.Set("Origin", "http://"+handler.Host)
	request.Header.Set("X-CFO-Token", "test-instance")
	request.Header.Set("Content-Type", "application/json")
	if change != nil {
		change(request)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// vaultValue reads what the credential store holds for name in the request's
// scope, straight from the file store.
func (b *credentialBoard) vaultValue(name string) (string, bool) {
	b.t.Helper()
	data, err := os.ReadFile(filepath.Join(b.vault, b.request.Project, name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false
	}
	if err != nil {
		b.t.Fatal(err)
	}
	return string(data), true
}

func (b *credentialBoard) stored() CredentialRequest {
	b.t.Helper()
	for _, request := range b.service.Store.Snapshot().Credentials {
		if request.ID == b.request.ID {
			return request
		}
	}
	b.t.Fatal("the board's request is gone")
	return CredentialRequest{}
}

// filesHolding names every file under root whose content holds text.
func filesHolding(t *testing.T, root, text string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(text)) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// The Overlord's value goes into the project's scope of the credential
// store and nowhere else, and the request it answered closes.
func TestCredentialSaveStoresTheValueInTheProjectScopeAndClosesTheRequest(t *testing.T) {
	// Arrange
	b := newCredentialBoard(t, "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET")
	canary := newCanary(t)

	// Act
	response := b.post(b.handler, b.body(map[string]string{"STRIPE_SECRET_KEY": canary}), nil)
	b.service.credentialWork.Wait()

	// Assert
	if response.Code != http.StatusOK {
		t.Fatalf("save = %d %s", response.Code, response.Body)
	}
	if got, found := b.vaultValue("STRIPE_SECRET_KEY"); !found || got != canary {
		t.Fatal("the value did not reach the project's scope")
	}
	if _, err := os.Stat(filepath.Join(b.vault, "STRIPE_SECRET_KEY")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the value reached the shared scope")
	}
	if _, found := b.vaultValue("STRIPE_WEBHOOK_SECRET"); found {
		t.Fatal("a name the Overlord left empty was stored")
	}
	var result struct {
		ID    string   `json:"id"`
		State string   `json:"state"`
		Saved []string `json:"saved"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.ID != b.request.ID || result.State != "saved" || !slices.Equal(result.Saved, []string{"STRIPE_SECRET_KEY"}) {
		t.Fatalf("response = %s, want the request saved with the one name", response.Body)
	}
	if request := b.stored(); request.State != "saved" || !slices.Equal(request.Saved, []string{"STRIPE_SECRET_KEY"}) || request.ClosedAt == nil {
		t.Fatalf("request after the save = %+v, want it closed as saved", request)
	}
}

// Only the board's own page on this machine, through its own token and
// origin, can save a value, and only names its open request asks for. Each
// refusal stores nothing and leaves the request open: the same board then
// takes the save as the board's page sends it.
func TestCredentialSaveRefusesEverythingButAnOpenRequestFromThisMachine(t *testing.T) {
	header := func(name, value string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set(name, value) }
	}
	values := func(pairs ...string) func(*credentialBoard, string) string {
		return func(b *credentialBoard, canary string) string {
			sent := map[string]string{}
			for i := 0; i < len(pairs); i += 2 {
				sent[pairs[i]] = strings.ReplaceAll(pairs[i+1], "CANARY", canary)
			}
			return b.body(sent)
		}
	}
	edited := func(edit func(b *credentialBoard, valid string) string) func(*credentialBoard, string) string {
		return func(b *credentialBoard, canary string) string {
			return edit(b, b.body(map[string]string{"STRIPE_SECRET_KEY": canary}))
		}
	}
	for _, test := range []struct {
		name    string
		tailnet bool
		body    func(*credentialBoard, string) string
		change  func(*http.Request)
		code    int
	}{
		{"a board served under a name off this machine", true, nil, nil, http.StatusForbidden},
		{"X-Forwarded-For", false, nil, header("X-Forwarded-For", "100.101.102.103"), http.StatusForbidden},
		{"X-Forwarded-Host", false, nil, header("X-Forwarded-Host", "goblins.tailnet.ts.net"), http.StatusForbidden},
		{"X-Forwarded-Proto", false, nil, header("X-Forwarded-Proto", "https"), http.StatusForbidden},
		{"Forwarded", false, nil, header("Forwarded", "for=100.101.102.103"), http.StatusForbidden},
		{"X-Real-Ip", false, nil, header("X-Real-Ip", "100.101.102.103"), http.StatusForbidden},
		{"Via", false, nil, header("Via", "1.1 tailscale"), http.StatusForbidden},
		{"Tailscale-User-Login", false, nil, header("Tailscale-User-Login", "overlord@example.com"), http.StatusForbidden},
		{"Tailscale-User-Name", false, nil, header("Tailscale-User-Name", "Overlord"), http.StatusForbidden},
		{"Tailscale-User-Profile-Pic", false, nil, header("Tailscale-User-Profile-Pic", "https://example.com/p.png"), http.StatusForbidden},
		{"Tailscale-Headers-Info", false, nil, header("Tailscale-Headers-Info", "https://tailscale.com/s/serve-headers"), http.StatusForbidden},
		{"a peer off this machine", false, nil, func(r *http.Request) { r.RemoteAddr = "100.101.102.103:51234" }, http.StatusForbidden},
		{"no board token", false, nil, func(r *http.Request) { r.Header.Del("X-CFO-Token") }, http.StatusForbidden},
		{"another board's token", false, nil, header("X-CFO-Token", "other-instance"), http.StatusForbidden},
		{"no origin", false, nil, func(r *http.Request) { r.Header.Del("Origin") }, http.StatusForbidden},
		{"a cross-site origin", false, nil, header("Origin", "https://evil.example"), http.StatusForbidden},
		{"a GET", false, nil, func(r *http.Request) { r.Method = "GET" }, http.StatusNotFound},
		{"a PUT", false, nil, func(r *http.Request) { r.Method = "PUT" }, http.StatusNotFound},
		{"a body that is not JSON", false, nil, header("Content-Type", "text/plain"), http.StatusUnsupportedMediaType},
		{"a name the request does not ask for", false, values("STRIPE_SECRET_KEY", "CANARY", "OTHER_SECRET", "CANARY"), nil, http.StatusBadRequest},
		{"a field the save does not take", false, edited(func(_ *credentialBoard, valid string) string {
			return strings.Replace(valid, `"values"`, `"project":"other","values"`, 1)
		}), nil, http.StatusBadRequest},
		{"another generation", false, edited(func(b *credentialBoard, valid string) string {
			return strings.Replace(valid, b.request.Generation, strings.Repeat("0", len(b.request.Generation)), 1)
		}), nil, http.StatusConflict},
		{"a request that is not on the board", false, edited(func(b *credentialBoard, valid string) string {
			return strings.Replace(valid, b.request.ID, "cred-ffffffffffffffff", 1)
		}), nil, http.StatusConflict},
		{"no values", false, values(), nil, http.StatusBadRequest},
		{"an empty value", false, values("STRIPE_SECRET_KEY", "   "), nil, http.StatusBadRequest},
		{"a value with a line break", false, values("STRIPE_SECRET_KEY", "CANARY\nCANARY"), nil, http.StatusBadRequest},
		{"two saves in one body", false, edited(func(_ *credentialBoard, valid string) string { return valid + valid }), nil, http.StatusBadRequest},
		{"a body over its limit", false, values("STRIPE_SECRET_KEY", "CANARY"+strings.Repeat("x", 80<<10)), nil, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := newCredentialBoard(t, "STRIPE_SECRET_KEY")
			canary := newCanary(t)
			valid := b.body(map[string]string{"STRIPE_SECRET_KEY": canary})
			body := valid
			if test.body != nil {
				body = test.body(b, canary)
			}
			handler := b.handler
			if test.tailnet {
				handler = NewHTTP(b.service, "goblins.tailnet.ts.net:443", nil)
			}

			// Act
			response := b.post(handler, body, test.change)

			// Assert
			if response.Code != test.code {
				t.Fatalf("save = %d %s, want %d", response.Code, response.Body, test.code)
			}
			if strings.Contains(response.Body.String(), canary) {
				t.Fatal("the refusal repeats the value")
			}
			if _, found := b.vaultValue("STRIPE_SECRET_KEY"); found {
				t.Fatal("a refused save stored a value")
			}
			if request := b.stored(); request.State != "open" {
				t.Fatalf("a refused save left the request %s", request.State)
			}
			if response := b.post(b.handler, valid, nil); response.Code != http.StatusOK {
				t.Fatalf("the board's own save after the refusal = %d %s", response.Code, response.Body)
			}
		})
	}
}

// A request takes one save: the same save sent again, or another value for
// the same name, is refused and the first value stays.
func TestCredentialRequestTakesOneSave(t *testing.T) {
	// Arrange
	b := newCredentialBoard(t, "STRIPE_SECRET_KEY")
	first, second := newCanary(t), newCanary(t)
	if response := b.post(b.handler, b.body(map[string]string{"STRIPE_SECRET_KEY": first}), nil); response.Code != http.StatusOK {
		t.Fatalf("first save = %d %s", response.Code, response.Body)
	}

	// Act
	replayed := b.post(b.handler, b.body(map[string]string{"STRIPE_SECRET_KEY": first}), nil)
	changed := b.post(b.handler, b.body(map[string]string{"STRIPE_SECRET_KEY": second}, "STRIPE_SECRET_KEY"), nil)

	// Assert
	if replayed.Code != http.StatusConflict || changed.Code != http.StatusConflict {
		t.Fatalf("replay = %d, second value = %d, want both refused", replayed.Code, changed.Code)
	}
	if got, _ := b.vaultValue("STRIPE_SECRET_KEY"); got != first {
		t.Fatal("a second save changed the stored value")
	}
}

// A request nobody saved within its lifetime takes no value, closes as
// expired, and the CFO hears so by name.
func TestExpiredCredentialRequestTakesNoValueAndTellsTheCFO(t *testing.T) {
	// Arrange
	b := newCredentialBoard(t, "STRIPE_SECRET_KEY")
	canary := newCanary(t)
	b.service.Store.mu.Lock()
	b.service.Store.db.Credentials[0].ExpiresAt = time.Now().Add(-time.Minute)
	b.service.Store.mu.Unlock()

	// Act
	saved := b.post(b.handler, b.body(map[string]string{"STRIPE_SECRET_KEY": canary}), nil)
	b.service.expireCredentials(time.Now())

	// Assert
	if saved.Code != http.StatusConflict {
		t.Fatalf("save of an expired request = %d %s", saved.Code, saved.Body)
	}
	if _, found := b.vaultValue("STRIPE_SECRET_KEY"); found {
		t.Fatal("an expired request stored a value")
	}
	if request := b.stored(); request.State != "expired" || request.ClosedAt == nil {
		t.Fatalf("request = %+v, want it closed as expired", request)
	}
	pending, err := wake.Pending(b.home.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Kind != "review" || !strings.Contains(pending[0].Detail, "STRIPE_SECRET_KEY") || !strings.Contains(pending[0].Detail, "expired") {
		t.Fatalf("wakes = %+v, want one telling the CFO the request expired", pending)
	}
}

// Expiry waits for a save in flight: a request the save records as saved just
// past its expiry stays saved, and the CFO hears nothing of it expiring.
func TestCredentialExpiryWaitsForASaveInFlight(t *testing.T) {
	// Arrange
	b := newCredentialBoard(t, "STRIPE_SECRET_KEY")
	b.service.Store.mu.Lock()
	b.service.Store.db.Credentials[0].ExpiresAt = time.Now().Add(-time.Minute)
	b.service.Store.mu.Unlock()
	b.service.credentialSaves.Lock()
	expired := make(chan error, 1)

	// Act
	go func() { expired <- b.service.expireCredentials(time.Now()) }()
	var finishedDuringSave bool
	select {
	case <-expired:
		finishedDuringSave = true
	case <-time.After(200 * time.Millisecond):
	}
	now := time.Now().UTC()
	_, recordErr := b.service.Store.updateCredential(b.request.ID, func(r *CredentialRequest) {
		r.State, r.Saved, r.ClosedAt = "saved", []string{"STRIPE_SECRET_KEY"}, &now
	})
	b.service.credentialSaves.Unlock()
	var expireErr error
	if !finishedDuringSave {
		select {
		case expireErr = <-expired:
		case <-time.After(5 * time.Second):
			t.Fatal("expiry never finished once the save released the request")
		}
	}

	// Assert
	if finishedDuringSave {
		t.Fatal("expiry acted on the request while a save held it")
	}
	if recordErr != nil || expireErr != nil {
		t.Fatal(recordErr, expireErr)
	}
	if request := b.stored(); request.State != "saved" {
		t.Fatalf("request after the save and expiry = %+v, want it saved", request)
	}
	pending, err := wake.Pending(b.home.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("wakes = %+v, want no expired-unsaved notice for a saved request", pending)
	}
}

// A name that already holds a value in the scope is replaced only when the
// save says so, which the card asks the Overlord first.
func TestCredentialSaveReplacesAStoredValueOnlyWhenConfirmed(t *testing.T) {
	// Arrange
	b := newCredentialBoard(t, "STRIPE_SECRET_KEY")
	old, replacement := newCanary(t), newCanary(t)
	store, err := auth.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(auth.Key{Project: "throwaway", Name: "STRIPE_SECRET_KEY"}, old); err != nil {
		t.Fatal(err)
	}

	// Act
	unconfirmed := b.post(b.handler, b.body(map[string]string{"STRIPE_SECRET_KEY": replacement}), nil)
	afterRefusal, _ := b.vaultValue("STRIPE_SECRET_KEY")
	stillOpen := b.stored()
	confirmed := b.post(b.handler, b.body(map[string]string{"STRIPE_SECRET_KEY": replacement}, "STRIPE_SECRET_KEY"), nil)

	// Assert
	if unconfirmed.Code != http.StatusConflict || !strings.Contains(unconfirmed.Body.String(), `"existing":["STRIPE_SECRET_KEY"]`) {
		t.Fatalf("unconfirmed save = %d %s, want it refused naming the stored name", unconfirmed.Code, unconfirmed.Body)
	}
	if afterRefusal != old || stillOpen.State != "open" || !slices.Equal(stillOpen.Existing, []string{"STRIPE_SECRET_KEY"}) {
		t.Fatalf("after the refusal the store changed or the request is %+v", stillOpen)
	}
	if confirmed.Code != http.StatusOK {
		t.Fatalf("confirmed save = %d %s", confirmed.Code, confirmed.Body)
	}
	if got, _ := b.vaultValue("STRIPE_SECRET_KEY"); got != replacement {
		t.Fatal("the confirmed save did not replace the value")
	}
	if request := b.stored(); !slices.Equal(request.Replaced, []string{"STRIPE_SECRET_KEY"}) {
		t.Fatalf("request = %+v, want the name recorded as replaced", request)
	}
}

// After a save the project's running goblins are refreshed, and the CFO hears
// which names were stored for which project and who was told, never a value.
func TestCredentialSaveRefreshesTheProjectAndTellsTheCFOTheNamesOnly(t *testing.T) {
	// Arrange
	b := newCredentialBoard(t, "STRIPE_SECRET_KEY")
	canary := newCanary(t)

	// Act
	response := b.post(b.handler, b.body(map[string]string{"STRIPE_SECRET_KEY": canary}), nil)
	b.service.credentialWork.Wait()

	// Assert
	if response.Code != http.StatusOK {
		t.Fatalf("save = %d %s", response.Code, response.Body)
	}
	if !slices.Equal(b.refreshed, []string{"throwaway"}) {
		t.Fatalf("refreshed = %v, want the throwaway project once", b.refreshed)
	}
	if request := b.stored(); !slices.Equal(request.Told, []string{"task-1"}) {
		t.Fatalf("request = %+v, want the goblin it told recorded", request)
	}
	pending, err := wake.Pending(b.home.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Kind != "review" || !strings.Contains(pending[0].Detail, "STRIPE_SECRET_KEY stored for throwaway") || !strings.Contains(pending[0].Detail, "task-1") {
		t.Fatalf("wakes = %+v, want one telling the CFO the name, the project and the goblin told", pending)
	}
	if strings.Contains(pending[0].Detail, canary) {
		t.Fatal("the CFO's wake holds the value")
	}
}

// A panic while a save is handled answers with a fixed message and logs
// nothing: the value it held never reaches the response or the server's log.
func TestCredentialSavePanicLeavesNoTraceOfTheValue(t *testing.T) {
	// Arrange
	b := newCredentialBoard(t, "STRIPE_SECRET_KEY")
	canary := newCanary(t)
	b.service.Options.Credentials = func() (auth.Store, error) { return panickingStore{}, nil }
	var logged bytes.Buffer
	server := httptest.NewUnstartedServer(b.handler)
	server.Config.ErrorLog = log.New(&logged, "", 0)
	server.Start()
	defer server.Close()
	b.handler.Host = strings.TrimPrefix(server.URL, "http://")
	request, err := http.NewRequest("POST", server.URL+"/api/credentials/save", strings.NewReader(b.body(map[string]string{"STRIPE_SECRET_KEY": canary})))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", server.URL)
	request.Header.Set("X-CFO-Token", "test-instance")
	request.Header.Set("Content-Type", "application/json")

	// Act
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()

	// Assert
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("save that panicked = %d %s", response.StatusCode, data)
	}
	if bytes.Contains(data, []byte(canary)) || strings.Contains(logged.String(), canary) {
		t.Fatal("the panic put the value in the response or the server's log")
	}
	if request := b.stored(); request.State != "open" {
		t.Fatalf("a save that stored nothing left the request %s", request.State)
	}
}

// panickingStore fails the way a buggy store could at its worst: panicking
// with the value it was handed.
type panickingStore struct{}

func (panickingStore) Get(auth.Key) (string, bool, error) { return "", false, nil }
func (panickingStore) Set(_ auth.Key, value string) error { panic(value) }
func (panickingStore) Keys() ([]auth.Key, error)          { return nil, nil }
func (panickingStore) Describe() string                   { return "panicking store" }

// After a save and every kind of refusal, the value is in the credential
// store and nowhere in the home, the board's snapshot or its event stream.
func TestCredentialValueReachesNoRecordSnapshotOrStream(t *testing.T) {
	// Arrange
	_, h := testStore(t)
	vault := t.TempDir()
	t.Setenv(auth.StoreDirEnv, vault)
	s, err := Start(context.Background(), h, Options{Credentials: auth.OpenStore})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	request, err := s.acceptCredentialRequest(CredentialRequest{ID: "cred-0123456789abcdef", Identity: strings.Repeat("c", 64), By: "cfo", Project: "throwaway", Names: []string{"STRIPE_SECRET_KEY"}, Why: "Charge test cards"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHTTP(s, "", nil))
	defer server.Close()
	handler := server.Config.Handler.(*HTTP)
	handler.Host = strings.TrimPrefix(server.URL, "http://")
	canary := newCanary(t)
	save := func(values string) int {
		t.Helper()
		body := `{"id":"` + request.ID + `","generation":"` + request.Generation + `","values":` + values + `}`
		req, _ := http.NewRequest("POST", server.URL+"/api/credentials/save", strings.NewReader(body))
		req.Header.Set("Origin", server.URL)
		req.Header.Set("X-CFO-Token", s.Instance)
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}

	// Act
	refusedName := save(`{"OTHER_SECRET":"` + canary + `"}`)
	saved := save(`{"STRIPE_SECRET_KEY":"` + canary + `"}`)
	replayed := save(`{"STRIPE_SECRET_KEY":"` + canary + `"}`)
	s.credentialWork.Wait()
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	published, _ := json.Marshal(snapshot)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	streamRequest, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/events", nil)
	stream, err := http.DefaultClient.Do(streamRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	lines := bufio.NewReader(stream.Body)
	event := ""
	for !strings.HasPrefix(event, "data: ") {
		if event, err = lines.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	if refusedName != http.StatusBadRequest || saved != http.StatusOK || replayed != http.StatusConflict {
		t.Fatalf("saves = %d, %d, %d; want the stray name refused, the save taken and the replay refused", refusedName, saved, replayed)
	}
	if data, err := os.ReadFile(filepath.Join(vault, "throwaway", "STRIPE_SECRET_KEY")); err != nil || string(data) != canary {
		t.Fatal("the credential store does not hold the value")
	}
	if found := filesHolding(t, h.Root, canary); len(found) != 0 {
		t.Fatalf("the value is in %d files of the home: %v", len(found), found)
	}
	if bytes.Contains(published, []byte(canary)) || !bytes.Contains(published, []byte("STRIPE_SECRET_KEY")) {
		t.Fatal("the snapshot holds the value, or does not name the request")
	}
	if strings.Contains(event, canary) {
		t.Fatal("the event stream holds the value")
	}
}

// A request takes names only: a name, a reason or a link shaped like a value
// is refused, and so is a link that is not a plain https page.
func TestCredentialRequestTakesNamesNeverValues(t *testing.T) {
	random := "q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc5Fg0Js"
	base := CredentialRequest{ID: "cred-0123456789abcdef", Identity: strings.Repeat("c", 64), By: "cfo", Project: "throwaway", Names: []string{"STRIPE_SECRET_KEY"}, Why: "Charge test cards", Link: "https://dashboard.stripe.com/apikeys"}
	t.Run("the request as written is taken", func(t *testing.T) {
		store, _ := testStore(t)
		t.Setenv(auth.StoreDirEnv, t.TempDir())
		s := &Service{Store: store, Options: Options{Credentials: auth.OpenStore}}
		if _, err := s.acceptCredentialRequest(base); err != nil || len(store.Snapshot().Credentials) != 1 {
			t.Fatalf("the base request = %v, want it on the board", err)
		}
	})
	for _, test := range []struct {
		name   string
		change func(*CredentialRequest)
	}{
		{"a name shaped like a key", func(r *CredentialRequest) { r.Names = []string{"sk" + "_live_" + random} }},
		{"a name with a value assigned", func(r *CredentialRequest) { r.Names = []string{"STRIPE_SECRET_KEY=" + "x"} }},
		{"a name of long random text", func(r *CredentialRequest) { r.Names = []string{random} }},
		{"a name that is no variable", func(r *CredentialRequest) { r.Names = []string{"STRIPE SECRET"} }},
		{"no names", func(r *CredentialRequest) { r.Names = nil }},
		{"a name twice", func(r *CredentialRequest) { r.Names = []string{"STRIPE_SECRET_KEY", "STRIPE_SECRET_KEY"} }},
		{"eleven names", func(r *CredentialRequest) {
			r.Names = nil
			for i := range 11 {
				r.Names = append(r.Names, "NAME_"+string(rune('A'+i)))
			}
		}},
		{"a reason holding a key", func(r *CredentialRequest) { r.Why = "use " + "ghp" + "_" + random + " for now" }},
		{"no reason", func(r *CredentialRequest) { r.Why = " " }},
		{"a reason over two lines", func(r *CredentialRequest) { r.Why = "Charge\ntest cards" }},
		{"a link holding a key", func(r *CredentialRequest) { r.Link = "https://example.com/keys/" + random }},
		{"a link with a query", func(r *CredentialRequest) { r.Link = "https://example.com/keys?token=abc" }},
		{"a link with credentials", func(r *CredentialRequest) { r.Link = "https://user:pass@example.com/keys" }},
		{"a plain http link", func(r *CredentialRequest) { r.Link = "http://example.com/keys" }},
		{"a scope that walks", func(r *CredentialRequest) { r.Project = ".." }},
		{"a task that is no task ID", func(r *CredentialRequest) { r.Task = "../task" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			t.Setenv(auth.StoreDirEnv, t.TempDir())
			s := &Service{Store: store, Options: Options{Credentials: auth.OpenStore}}
			request := base
			request.Names = slices.Clone(base.Names)
			test.change(&request)

			// Act
			_, err := s.acceptCredentialRequest(request)

			// Assert
			if err == nil {
				t.Fatal("the request was taken")
			}
			if len(store.Snapshot().Credentials) != 0 {
				t.Fatal("a refused request reached the board")
			}
			if strings.Contains(err.Error(), random) {
				t.Fatal("the refusal repeats the value")
			}
		})
	}
}

// Each row shows where its value goes: the repository the request is for,
// the credential scope, and every service of the project's auth.json that
// reads the name, beside the project's goblins, who all get the scope.
func TestCredentialRequestShowsWhereEachValueGoes(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	t.Setenv(auth.StoreDirEnv, t.TempDir())
	manifest := auth.ManifestPath(h.Data, "throwaway")
	if err := os.MkdirAll(filepath.Dir(manifest), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"project":"throwaway","services":[
		{"name":"stripe","method":"env","env":["STRIPE_SECRET_KEY"]},
		{"name":"billing","method":"env","env":["STRIPE_SECRET_KEY","STRIPE_WEBHOOK_SECRET"]},
		{"name":"mail","method":"env","env":["MAIL_KEY"],"aliases":{"MAIL_KEY":["RESEND_API_KEY"]}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(t.TempDir(), "throwaway")
	s := &Service{Store: store, Options: Options{Credentials: auth.OpenStore}}
	base := CredentialRequest{ID: "cred-0123456789abcdef", Identity: strings.Repeat("c", 64), By: "cfo", Project: "throwaway", Repository: repository, Names: []string{"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET", "RESEND_API_KEY", "OTHER_KEY"}, Why: "Charge test cards"}

	// Act
	request, err := s.acceptCredentialRequest(base)
	elsewhere := base
	elsewhere.ID, elsewhere.Repository = "cred-1111111111111111", filepath.Join(t.TempDir(), "another")
	_, elsewhereErr := s.acceptCredentialRequest(elsewhere)
	relative := base
	relative.ID, relative.Repository = "cred-2222222222222222", "throwaway"
	_, relativeErr := s.acceptCredentialRequest(relative)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if request.Repository != repository {
		t.Fatalf("repository = %q, want %q", request.Repository, repository)
	}
	want := map[string][]string{"STRIPE_SECRET_KEY": {"stripe", "billing"}, "STRIPE_WEBHOOK_SECRET": {"billing"}, "RESEND_API_KEY": {"mail"}}
	if len(request.Services) != len(want) {
		t.Fatalf("services = %v, want %v", request.Services, want)
	}
	for name, services := range want {
		if !slices.Equal(request.Services[name], services) {
			t.Fatalf("services = %v, want %v", request.Services, want)
		}
	}
	if elsewhereErr == nil || relativeErr == nil {
		t.Fatalf("a repository of another scope = %v, a relative one = %v; want both refused", elsewhereErr, relativeErr)
	}
}

// The card shows each name's format hint from the project's auth.json and
// which names the scope already holds, so it can warn and ask before
// replacing; it never shows a value.
func TestCredentialRequestCarriesFormatHintsAndStoredNames(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	vault := t.TempDir()
	t.Setenv(auth.StoreDirEnv, vault)
	writeManifest := auth.ManifestPath(h.Data, "throwaway")
	if err := os.MkdirAll(filepath.Dir(writeManifest), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(writeManifest, []byte(`{"project":"throwaway","services":[],"formats":[{"names":["STRIPE_*"],"prefixes":["sk_","rk_"],"warn":[{"prefix":"sk_live_","say":"Use a restricted rk_live_ key."}]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	vaultStore, err := auth.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	stored := newCanary(t)
	if err := vaultStore.Set(auth.Key{Project: "throwaway", Name: "STRIPE_WEBHOOK_SECRET"}, stored); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store, Options: Options{Credentials: auth.OpenStore}}

	// Act
	request, err := s.acceptCredentialRequest(CredentialRequest{ID: "cred-0123456789abcdef", Identity: strings.Repeat("c", 64), By: "cfo", Project: "throwaway", Names: []string{"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET", "RESEND_API_KEY"}, Why: "Charge test cards"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	published, _ := json.Marshal(snapshot)

	// Assert
	if len(request.Generation) != 32 || request.State != "open" || !request.ExpiresAt.After(time.Now().Add(23*time.Hour)) {
		t.Fatalf("request = %+v, want it open for a day under a fresh generation", request)
	}
	if !slices.Equal(request.Existing, []string{"STRIPE_WEBHOOK_SECRET"}) {
		t.Fatalf("existing = %v, want the one name the scope holds", request.Existing)
	}
	hinted := map[string]CredentialHint{}
	for _, hint := range request.Hints {
		hinted[hint.Name] = hint
	}
	if hint := hinted["STRIPE_SECRET_KEY"]; !slices.Equal(hint.Prefixes, []string{"sk_", "rk_"}) || len(hint.Warn) != 1 || hint.Warn[0].Prefix != "sk_live_" {
		t.Fatalf("hints = %+v, want STRIPE_SECRET_KEY's from STRIPE_*", request.Hints)
	}
	if _, found := hinted["RESEND_API_KEY"]; found {
		t.Fatal("a name no format names got a hint")
	}
	if len(snapshot.Credentials) != 1 || snapshot.Credentials[0].ID != request.ID || bytes.Contains(published, []byte(stored)) {
		t.Fatal("the snapshot does not show the request, or shows a stored value")
	}
}
