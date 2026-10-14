package cirunner

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// asked is one request the fake GitHub was sent.
type asked struct {
	call          string
	authorization string
	body          []byte
}

// fakeGitHub answers the App's calls and records every request it is sent.
type fakeGitHub struct {
	mu            sync.Mutex
	asked         []asked
	runners       []map[string]any
	jobs          []map[string]any
	runs          map[string][]int64
	expires       time.Time
	deleteStatus  int
	isRedirecting bool
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	f.asked = append(f.asked, asked{call: r.Method + " " + r.URL.Path, authorization: r.Header.Get("Authorization"), body: body})
	switch {
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/access_tokens"):
		expires := f.expires
		if expires.IsZero() {
			expires = time.Now().Add(time.Hour)
		}
		json.NewEncoder(w).Encode(map[string]any{"token": fmt.Sprintf("installation-token-%d", len(f.asked)), "expires_at": expires})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/actions/runs") && f.isRedirecting:
		http.Redirect(w, r, "/user", http.StatusMovedPermanently)
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/actions/runs"):
		runs := []map[string]any{{"id": 7}}
		if f.runs != nil {
			runs = nil
			for _, id := range f.runs[r.URL.Query().Get("status")] {
				runs = append(runs, map[string]any{"id": id})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"workflow_runs": runs})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/actions/runs/7/jobs"):
		json.NewEncoder(w).Encode(map[string]any{"jobs": f.jobs})
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/actions/runners/generate-jitconfig"):
		json.NewEncoder(w).Encode(map[string]any{"runner": map[string]any{"id": 41, "name": "cg-desktop-1"}, "encoded_jit_config": "abc"})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/actions/runners"):
		json.NewEncoder(w).Encode(map[string]any{"runners": f.runners})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/actions/runners/41"):
		json.NewEncoder(w).Encode(map[string]any{"id": 41, "status": "online", "busy": true})
	case r.Method == "DELETE" && f.deleteStatus != 0:
		w.WriteHeader(f.deleteStatus)
	case r.Method == "DELETE":
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// calls is every call the fake was sent, in order.
func (f *fakeGitHub) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var calls []string
	for _, request := range f.asked {
		calls = append(calls, request.call)
	}
	return calls
}

func testGitHub(t *testing.T, fake *fakeGitHub) *GitHub {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return &GitHub{Client: server.Client(), Base: server.URL, AppID: 1, InstallationID: 2, Key: key, Owner: "fpresta0607", Repo: "PrecisionDocs-AI", Now: time.Now}
}

// desktopJob is a job of run 7 with labels and status.
func desktopJob(name, status string, labels []string, created time.Time) map[string]any {
	return map[string]any{"id": 9, "name": name, "status": status, "labels": labels, "created_at": created}
}

// The service calls nothing but the paths it is pinned to, whatever it does,
// and each pinned call is one it makes.
func TestGitHubCallsOnlyItsPinnedPaths(t *testing.T) {
	// Arrange
	fake := &fakeGitHub{
		jobs:    []map[string]any{desktopJob("desktop-backend-shard (1)", "queued", Labels, time.Now())},
		runners: []map[string]any{{"id": 41, "name": RunnerPrefix + "1"}},
	}
	g := testGitHub(t, fake)
	ctx := context.Background()

	// Act
	_, queuedErr := g.QueuedJob(ctx)
	runner, registerErr := g.RegisterRunner(ctx, RunnerPrefix+"1")
	_, _, stateErr := g.RunnerState(ctx, runner.ID)
	deleteErr := g.DeleteRunner(ctx, runner.ID)
	ownErr := g.DeleteOwnRunners(ctx)

	// Assert
	if err := errors.Join(queuedErr, registerErr, stateErr, deleteErr, ownErr); err != nil {
		t.Fatalf("the calls failed: %v", err)
	}
	made := map[string]bool{}
	for _, call := range fake.calls() {
		pattern, isPinned := g.pinned(call)
		if !isPinned {
			t.Errorf("the service asked for %q, which is not among its pinned calls", call)
		}
		made[pattern] = true
	}
	for _, pattern := range allowedCalls {
		if !made[pattern] {
			t.Errorf("the pinned call %q was never made; want every pin used", pattern)
		}
	}
}

// A call outside the pins is refused before anything is sent, not even the
// request for a token.
func TestGitHubRefusesACallOutsideItsPins(t *testing.T) {
	for name, call := range map[string][2]string{
		"another endpoint":            {"GET", "/user"},
		"another repository":          {"GET", "/repos/someone/else/actions/runners"},
		"a runner named by no id":     {"GET", "/repos/fpresta0607/PrecisionDocs-AI/actions/runners/abc"},
		"a signed runner id":          {"GET", "/repos/fpresta0607/PrecisionDocs-AI/actions/runners/+41"},
		"a pinned path, another verb": {"DELETE", "/repos/fpresta0607/PrecisionDocs-AI/actions/runs/7"},
		"a path that climbs out":      {"GET", "/repos/fpresta0607/PrecisionDocs-AI/actions/runners/1/../../../../user"},
		"another installation":        {"POST", "/app/installations/3/access_tokens"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			fake := &fakeGitHub{}
			g := testGitHub(t, fake)

			// Act
			err := g.do(context.Background(), call[0], call[1], nil, nil, nil)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "not among the service's pinned GitHub calls") {
				t.Errorf("do(%s %s) = %v; want it refused as unpinned", call[0], call[1], err)
			}
			if sent := fake.calls(); len(sent) != 0 {
				t.Errorf("sent %q; want nothing sent", sent)
			}
		})
	}
}

// A redirect is an answer, never followed, so it cannot carry a pinned call
// to a path the pins leave out.
func TestGitHubFollowsNoRedirect(t *testing.T) {
	// Arrange
	fake := &fakeGitHub{isRedirecting: true}
	g := testGitHub(t, fake)

	// Act
	_, err := g.QueuedJob(context.Background())

	// Assert
	if err == nil || !strings.Contains(err.Error(), "HTTP 301") {
		t.Errorf("QueuedJob = %v; want the redirect named as the answer", err)
	}
	if calls := fake.calls(); slices.Contains(calls, "GET /user") {
		t.Errorf("sent %q; want the redirect not followed", calls)
	}
}

// A queued job is the service's when it asks for cg-desktop and for nothing
// the desktop runner lacks, and only then.
func TestQueuedJobFindsAJobOnTheDesktopLabels(t *testing.T) {
	for name, row := range map[string]struct {
		labels      []string
		status      string
		wantWaiting bool
	}{
		"a queued desktop job":                    {labels: Labels, status: "queued", wantWaiting: true},
		"a queued desktop job named by fewer":     {labels: []string{"self-hosted", "cg-desktop"}, status: "queued", wantWaiting: true},
		"a queued desktop job in another case":    {labels: []string{"self-hosted", "Linux", "X64", "cg-desktop"}, status: "queued", wantWaiting: true},
		"a queued hosted job":                     {labels: []string{"ubuntu-latest"}, status: "queued"},
		"a queued job for any self-hosted runner": {labels: []string{"self-hosted"}, status: "queued"},
		"a queued job asking for more":            {labels: append(slices.Clone(Labels), "gpu"), status: "queued"},
		"a desktop job already runs":              {labels: Labels, status: "in_progress"},
		"a desktop job with no labels":            {labels: nil, status: "queued"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			g := testGitHub(t, &fakeGitHub{jobs: []map[string]any{desktopJob("desktop-backend-shard (1)", row.status, row.labels, time.Now())}})

			// Act
			pending, err := g.QueuedJob(context.Background())

			// Assert
			if err != nil || pending.IsWaiting != row.wantWaiting {
				t.Errorf("QueuedJob = %+v, %v; want waiting %v", pending, err, row.wantWaiting)
			}
		})
	}
}

// Of the desktop jobs that wait, the one queued first is the one named.
func TestQueuedJobNamesTheOldest(t *testing.T) {
	// Arrange
	first := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	g := testGitHub(t, &fakeGitHub{jobs: []map[string]any{
		desktopJob("desktop-backend-shard (2)", "queued", Labels, first.Add(time.Minute)),
		desktopJob("desktop-backend-shard (1)", "queued", Labels, first),
		desktopJob("desktop-backend-shard (3)", "queued", Labels, first.Add(2*time.Minute)),
	}})

	// Act
	pending, err := g.QueuedJob(context.Background())

	// Assert
	want := Pending{IsWaiting: true, RunID: 7, Job: "desktop-backend-shard (1)", Queued: first}
	if err != nil || pending != want {
		t.Errorf("QueuedJob = %+v, %v; want %+v", pending, err, want)
	}
}

// A desktop job queued in a run already in progress, behind a job of that
// run that ran first, is found as one in a queued run is.
func TestQueuedJobFindsAJobInARunInProgress(t *testing.T) {
	for name, runs := range map[string]map[string][]int64{
		"in a run in progress": {"in_progress": {7}},
		"in a queued run":      {"queued": {7}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			g := testGitHub(t, &fakeGitHub{runs: runs, jobs: []map[string]any{desktopJob("desktop-backend-shard (1)", "queued", Labels, time.Now())}})

			// Act
			pending, err := g.QueuedJob(context.Background())

			// Assert
			if err != nil || !pending.IsWaiting || pending.RunID != 7 {
				t.Errorf("QueuedJob = %+v, %v; want run 7's job waiting", pending, err)
			}
		})
	}
}

// A runner is registered for one job with every desktop label, and its
// configuration is returned for the VM.
func TestRegisterRunnerAsksForOneJobOnTheDesktopLabels(t *testing.T) {
	// Arrange
	fake := &fakeGitHub{}
	g := testGitHub(t, fake)

	// Act
	runner, err := g.RegisterRunner(context.Background(), RunnerPrefix+"1")

	// Assert
	if err != nil || runner != (Runner{ID: 41, Name: "cg-desktop-1", Config: "abc"}) {
		t.Fatalf("RegisterRunner = %+v, %v; want runner 41 and its configuration", runner, err)
	}
	var registered struct {
		Name   string   `json:"name"`
		Group  int64    `json:"runner_group_id"`
		Labels []string `json:"labels"`
		Folder string   `json:"work_folder"`
	}
	request := fake.asked[len(fake.asked)-1]
	if err := json.Unmarshal(request.body, &registered); err != nil {
		t.Fatal(err)
	}
	if registered.Name != RunnerPrefix+"1" || registered.Group != 1 || !slices.Equal(registered.Labels, Labels) || registered.Folder != "_work" {
		t.Errorf("registered %+v; want the name asked, group 1, the desktop labels and _work", registered)
	}
}

// Only runners the service registered, by their name, are deleted.
func TestDeleteOwnRunnersSparesOthers(t *testing.T) {
	// Arrange
	fake := &fakeGitHub{runners: []map[string]any{{"id": 1, "name": RunnerPrefix + "old"}, {"id": 2, "name": "someone-elses-runner"}, {"id": 3, "name": "a-" + RunnerPrefix + "lookalike"}}}
	g := testGitHub(t, fake)

	// Act
	err := g.DeleteOwnRunners(context.Background())

	// Assert
	deleted := 0
	for _, call := range fake.calls() {
		if strings.HasPrefix(call, "DELETE ") {
			deleted++
			if !strings.HasSuffix(call, "/actions/runners/1") {
				t.Errorf("deleted %q; want only the service's own runner 1", call)
			}
		}
	}
	if err != nil || deleted != 1 {
		t.Errorf("DeleteOwnRunners = %v after %d delete(s); want one", err, deleted)
	}
}

// A runner already gone is deleted, and any other answer but success is an
// error that names it.
func TestDeleteRunnerTakesARunnerAlreadyGoneAsDeleted(t *testing.T) {
	for name, row := range map[string]struct {
		status  int
		wantErr string
	}{
		"deleted":      {status: http.StatusNoContent},
		"already gone": {status: http.StatusNotFound},
		"refused":      {status: http.StatusForbidden, wantErr: "HTTP 403"},
		"failed":       {status: http.StatusInternalServerError, wantErr: "HTTP 500"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			g := testGitHub(t, &fakeGitHub{deleteStatus: row.status})

			// Act
			err := g.DeleteRunner(context.Background(), 41)

			// Assert
			if row.wantErr == "" && err != nil || row.wantErr != "" && (err == nil || !strings.Contains(err.Error(), row.wantErr)) {
				t.Errorf("DeleteRunner = %v; want error %q", err, row.wantErr)
			}
		})
	}
}

// The installation's token is asked for this repository alone, with the two
// permissions the service needs, and with the App's own token.
func TestInstallationTokenAsksForTheRepositoryAndPermissionsAlone(t *testing.T) {
	// Arrange
	fake := &fakeGitHub{}
	g := testGitHub(t, fake)

	// Act
	_, _, err := g.RunnerState(context.Background(), 41)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	request := fake.asked[0]
	if request.call != "POST /app/installations/2/access_tokens" {
		t.Fatalf("first call %q; want the installation's token", request.call)
	}
	if err := verifyAppToken(strings.TrimPrefix(request.authorization, "Bearer "), &g.Key.PublicKey, g.AppID, g.Now()); err != nil {
		t.Errorf("the token request does not carry the App's token: %v", err)
	}
	var scope struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal(request.body, &scope); err != nil {
		t.Fatal(err)
	}
	wantPermissions := map[string]string{"administration": "write", "actions": "read"}
	if !slices.Equal(scope.Repositories, []string{"PrecisionDocs-AI"}) || len(scope.Permissions) != len(wantPermissions) || scope.Permissions["administration"] != "write" || scope.Permissions["actions"] != "read" {
		t.Errorf("asked for %+v; want PrecisionDocs-AI alone with %v", scope, wantPermissions)
	}
	if got := fake.asked[1].authorization; got != "Bearer installation-token-1" {
		t.Errorf("the runner call carried %q; want the installation's token", got)
	}
}

// The installation's token is kept and used again until five minutes before
// it expires, and then asked for anew.
func TestInstallationTokenIsKeptUntilNearItsExpiry(t *testing.T) {
	// Arrange
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	fake := &fakeGitHub{expires: now.Add(time.Hour)}
	g := testGitHub(t, fake)
	g.Now = func() time.Time { return now }
	ctx := context.Background()

	// Act
	_, _, firstErr := g.RunnerState(ctx, 41)
	now = now.Add(54 * time.Minute)
	_, _, keptErr := g.RunnerState(ctx, 41)
	now = now.Add(time.Minute)
	_, _, renewedErr := g.RunnerState(ctx, 41)

	// Assert
	if err := errors.Join(firstErr, keptErr, renewedErr); err != nil {
		t.Fatal(err)
	}
	var tokens []string
	for _, request := range fake.asked {
		if strings.HasSuffix(request.call, "/access_tokens") {
			tokens = append(tokens, request.call)
		}
	}
	if len(tokens) != 2 {
		t.Errorf("asked for %d token(s) over %q; want one, then one more at five minutes before it expires", len(tokens), fake.calls())
	}
}

// The App's token is signed with its key and names the App, and expires
// within ten minutes, as GitHub requires.
func TestAppTokenIsSignedWithTheAppsKey(t *testing.T) {
	// Arrange
	g := testGitHub(t, &fakeGitHub{})

	// Act
	token, err := g.appToken()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyAppToken(token, &g.Key.PublicKey, g.AppID, g.Now()); err != nil {
		t.Errorf("the App's token does not verify: %v", err)
	}
}

// verifyAppToken checks token is an RS256 token signed by key, issued by the
// App appID, valid at now and for ten minutes at most.
func verifyAppToken(token string, key *rsa.PublicKey, appID int64, now time.Time) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("%d parts; want 3", len(parts))
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return err
	}
	if string(header) != `{"alg":"RS256","typ":"JWT"}` {
		return fmt.Errorf("header %s", header)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return err
	}
	encoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	var claims struct {
		Issued  int64 `json:"iat"`
		Expires int64 `json:"exp"`
		Issuer  int64 `json:"iss"`
	}
	if err := json.Unmarshal(encoded, &claims); err != nil {
		return err
	}
	switch {
	case claims.Issuer != appID:
		return fmt.Errorf("issuer %d; want %d", claims.Issuer, appID)
	case claims.Expires-claims.Issued > 600:
		return fmt.Errorf("valid for %d seconds; want 600 at most", claims.Expires-claims.Issued)
	case now.Unix() < claims.Issued || now.Unix() >= claims.Expires:
		return fmt.Errorf("not valid at %d: issued %d, expires %d", now.Unix(), claims.Issued, claims.Expires)
	}
	return nil
}
