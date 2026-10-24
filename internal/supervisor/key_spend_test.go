package supervisor

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/openrouter"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// writtenAt is when the fixture profiles' auth files were written, before any
// session in these tests started.
var writtenAt = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

// piProfile writes a Pi profile with one OpenRouter key, dates its auth file,
// and returns the folder.
func piProfile(t *testing.T, key string, written time.Time) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	writeFile(t, path, `{"openrouter":{"type":"api_key","key":"`+key+`"}}`)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	return dir
}

// userEnvironment names one Pi profile, as the machine's user environment
// would, with the variables given after it.
func userEnvironment(dir string, variables ...string) func() ([]string, error) {
	entries := append([]string{"PI_CODING_AGENT_DIR=" + dir}, variables...)
	return func() ([]string, error) { return entries, nil }
}

// keySpendReading is one available reading of a key's dollars against its
// limit, written at written.
func keySpendReading(keyID string, spent, limit float64, written time.Time) openrouter.Reading {
	remaining := limit - spent
	return openrouter.Reading{Status: openrouter.StatusAvailable, SpentUSD: &spent, LimitUSD: &limit, RemainingUSD: &remaining, Period: openrouter.PeriodWeekly, ReadAt: time.Now().UTC(), KeyID: keyID, Profile: "default", CredentialSince: written}
}

// livePiTask is a live Pi goblin on model, started at since.
func livePiTask(id, model string, since time.Time) Task {
	return Task{ID: id, Harness: "pi", Generation: "g1", Session: "session-" + id, Model: model, Since: since, Runtime: RuntimeEvidence{State: "busy", At: time.Now().UTC()}}
}

// piSnapshot is a snapshot whose given tasks are at work at now.
func piSnapshot(now time.Time, tasks ...Task) Snapshot {
	return Snapshot{At: now, Tasks: tasks}
}

// fakeKeySpend answers each credential source with one reading, and counts
// what it was asked.
type fakeKeySpend struct {
	reads   atomic.Int32
	dirs    []string
	answers func(source openrouter.Source) openrouter.Reading
}

func (f *fakeKeySpend) read(_ context.Context, source openrouter.Source) openrouter.Reading {
	f.reads.Add(1)
	f.dirs = append(f.dirs, source.Dir)
	return f.answers(source)
}

// scheduleStarts is when every fixture session started.
var scheduleStarts = time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)

// The dial shows a Pi session's OpenRouter credential only while that session
// runs on OpenRouter, in Pi's own provider/model form or in Pi's default; a
// goblin on another provider asks OpenRouter for nothing.
func TestKeySpendIsShownOnlyForAPiThatUsesOpenRouter(t *testing.T) {
	now := time.Now().UTC()
	openrouterKey := "fixture-openrouter-key-one"
	for _, test := range []struct {
		name        string
		model       string
		settings    string
		tasks       []Task
		sessions    []Session
		cfo         cfoState
		withoutTask bool
		want        int
		status      string
		detail      string
	}{
		{name: "an OpenRouter goblin", model: "openrouter/deepseek/deepseek-v4.1-flash", want: 1, status: openrouter.StatusAvailable},
		{name: "a goblin on another provider", model: "deepseek/deepseek-v4.1-flash", want: 0},
		{name: "Pi's own default provider", settings: `{"defaultProvider":"openrouter"}`, want: 1, status: openrouter.StatusAvailable},
		{name: "Pi's default on another provider", settings: `{"defaultProvider":"deepseek"}`, want: 0},
		{name: "no provider anywhere", want: 1, status: openrouter.StatusUnattributed, detail: "name no provider"},
		{name: "no Pi at all", withoutTask: true, tasks: []Task{{ID: "other", Harness: "claude", Generation: "g1", Runtime: RuntimeEvidence{State: "busy", At: now}}}, want: 0},
		{name: "a Pi CFO on OpenRouter", withoutTask: true, cfo: cfoState{registered: true, harness: "pi", session: "cfo", since: scheduleStarts},
			sessions: []Session{{ID: "cfo", Harness: "pi", Role: "cfo", Model: "openrouter/deepseek/deepseek-v4.1-flash", Phase: "active"}}, want: 1, status: openrouter.StatusAvailable},
		{name: "a Pi CFO on another provider", withoutTask: true, cfo: cfoState{registered: true, harness: "pi", session: "cfo", since: scheduleStarts},
			sessions: []Session{{ID: "cfo", Harness: "pi", Role: "cfo", Model: "deepseek/deepseek-v4.1-flash", Phase: "active"}}, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := piProfile(t, openrouterKey, writtenAt)
			if test.settings != "" {
				writeFile(t, filepath.Join(dir, "settings.json"), test.settings)
			}
			tasks := test.tasks
			if tasks == nil && !test.withoutTask {
				tasks = []Task{livePiTask("goblin", test.model, scheduleStarts)}
			}
			fake := &fakeKeySpend{answers: func(openrouter.Source) openrouter.Reading {
				return keySpendReading("key-one", 9.04, 50, writtenAt)
			}}
			service := &Service{Options: Options{KeySpend: fake.read, UserEnvironment: userEnvironment(dir)}}

			// Act
			snapshot := piSnapshot(now, tasks...)
			snapshot.Sessions = test.sessions
			service.keySpendUsage(test.cfo, snapshot)
			service.readKeySpends(context.Background(), 10*time.Second)
			dials := service.keySpendUsage(test.cfo, snapshot)

			// Assert
			if len(dials) != test.want {
				t.Fatalf("dials = %+v, want %d", dials, test.want)
			}
			if test.want == 0 {
				if got := fake.reads.Load(); got != 0 {
					t.Fatalf("OpenRouter was read %d times with no OpenRouter session", got)
				}
				return
			}
			if got := fake.reads.Load(); got != 1 {
				t.Fatalf("OpenRouter reads = %d, want one for the one source", got)
			}
			if dials[0].Status != test.status {
				t.Fatalf("dial = %+v, want %q", dials[0], test.status)
			}
			if test.detail != "" && !strings.Contains(dials[0].Detail, test.detail) {
				t.Errorf("detail = %q, want it to name %q", dials[0].Detail, test.detail)
			}
			if dials[0].Profile != "default" {
				t.Errorf("profile = %q", dials[0].Profile)
			}
		})
	}
}

// Two active credentials are two dials whose spend is never merged, and two
// sessions that share one credential show one key total.
func TestKeySpendKeepsEachActiveCredentialApart(t *testing.T) {
	now := time.Now().UTC()
	store, h := testStore(t)
	shared := piProfile(t, "fixture-openrouter-key-shared", writtenAt)
	other := piProfile(t, "fixture-openrouter-key-other", writtenAt)
	manifest, err := json.Marshal(worktree.Manifest{Project: "proj", Env: map[string]string{"PI_CODING_AGENT_DIR": other}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, worktree.ManifestPath(h.Data, "proj"), string(manifest))
	for _, test := range []struct {
		name string
		read func(openrouter.Source) openrouter.Reading
		want []float64
	}{
		{name: "distinct keys", read: func(source openrouter.Source) openrouter.Reading {
			if source.Dir == other {
				return keySpendReading("key-other", 4.5, 20, writtenAt)
			}
			return keySpendReading("key-shared", 9.04, 50, writtenAt)
		}, want: []float64{4.5, 9.04}},
		{name: "one key both read", read: func(openrouter.Source) openrouter.Reading {
			return keySpendReading("key-shared", 9.04, 50, writtenAt)
		}, want: []float64{9.04}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			fake := &fakeKeySpend{answers: test.read}
			service := &Service{Store: store, Options: Options{KeySpend: fake.read, UserEnvironment: userEnvironment(shared)}}
			tasks := []Task{livePiTask("first", "openrouter/deepseek/deepseek-v4.1-flash", scheduleStarts), livePiTask("second", "openrouter/deepseek/deepseek-v4.1-flash", scheduleStarts)}
			tasks[1].Project = "proj"
			for i := range tasks {
				tasks[i].Runtime = RuntimeEvidence{State: "busy", At: now}
			}

			// Act
			service.keySpendUsage(cfoState{}, piSnapshot(now, tasks...))
			service.readKeySpends(context.Background(), 10*time.Second)
			dials := service.keySpendUsage(cfoState{}, piSnapshot(now, tasks...))

			// Assert
			costs := make([]float64, 0, len(dials))
			keys := make([]string, 0, len(dials))
			for _, dial := range dials {
				if dial.SpentUSD == nil {
					t.Fatalf("dial = %+v", dial)
				}
				costs = append(costs, *dial.SpentUSD)
				keys = append(keys, dial.KeyID)
			}
			slices.Sort(costs)
			if !slices.Equal(costs, test.want) {
				t.Fatalf("spend = %v, want %v", costs, test.want)
			}
			if len(test.want) == 2 && (len(keys) != 2 || keys[0] == keys[1]) {
				t.Fatalf("distinct keys read as one credential: %v", keys)
			}
			if got := fake.reads.Load(); got != 2 {
				t.Fatalf("reads = %d, want one for each of the two profiles", got)
			}
			if fake.dirs[0] == fake.dirs[1] {
				t.Fatalf("both sessions were read from %q", fake.dirs[0])
			}
		})
	}
}

// A reading is claimed for a session only when the credential's own file was
// written before that session started: a key whose value came from the
// environment, or a file another credential was written to since, is never
// said to be the one the session runs on.
func TestKeySpendIsNotClaimedForASessionThatStartedBeforeTheCredential(t *testing.T) {
	now := time.Now().UTC()
	started := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		reading openrouter.Reading
		since   time.Time
		status  string
		detail  string
	}{
		{name: "written before the session started", reading: keySpendReading("key", 9.04, 50, started.Add(-time.Hour)), since: started, status: openrouter.StatusAvailable},
		{name: "written after the session started", reading: keySpendReading("key", 9.04, 50, started.Add(time.Minute)), since: started, status: openrouter.StatusUnattributed, detail: "written after this session started"},
		{name: "a value the environment holds", reading: keySpendReading("key", 9.04, 50, time.Time{}), since: started, status: openrouter.StatusUnattributed, detail: "comes from the environment"},
		{name: "no recorded session start", reading: keySpendReading("key", 9.04, 50, writtenAt), status: openrouter.StatusUnattributed, detail: "did not record when this session started"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := piProfile(t, "fixture-openrouter-key-one", writtenAt)
			service := &Service{Options: Options{UserEnvironment: userEnvironment(dir)}}
			task := livePiTask("goblin", "openrouter/deepseek/deepseek-v4.1-flash", test.since)
			task.Runtime = RuntimeEvidence{State: "busy", At: now}
			service.mu.Lock()
			service.keySpendReadings = map[string]openrouter.Reading{keySpendSourceKey(dir, ""): test.reading}
			service.mu.Unlock()

			// Act
			dials := service.keySpendUsage(cfoState{}, piSnapshot(now, task))

			// Assert
			if len(dials) != 1 || dials[0].Status != test.status {
				t.Fatalf("dials = %+v, want %q", dials, test.status)
			}
			if test.detail != "" && !strings.Contains(dials[0].Detail, test.detail) {
				t.Errorf("detail = %q, want it to name %q", dials[0].Detail, test.detail)
			}
			if dials[0].SpentUSD == nil {
				t.Errorf("the reading's own dollars were dropped: %+v", dials[0])
			}
		})
	}

	// One session that started after the file was written proves the reading
	// is the credential it runs on, however old the other session is.
	dir := piProfile(t, "fixture-openrouter-key-one", writtenAt)
	service := &Service{Options: Options{UserEnvironment: userEnvironment(dir)}}
	older := livePiTask("older", "openrouter/deepseek/deepseek-v4.1-flash", writtenAt.Add(-time.Hour))
	newer := livePiTask("newer", "openrouter/deepseek/deepseek-v4.1-flash", writtenAt.Add(time.Hour))
	for _, task := range []*Task{&older, &newer} {
		task.Runtime = RuntimeEvidence{State: "busy", At: now}
	}
	service.mu.Lock()
	service.keySpendReadings = map[string]openrouter.Reading{keySpendSourceKey(dir, ""): keySpendReading("key", 9.04, 50, writtenAt)}
	service.mu.Unlock()
	if dials := service.keySpendUsage(cfoState{}, piSnapshot(now, older, newer)); len(dials) != 1 || dials[0].Status != openrouter.StatusAvailable {
		t.Fatalf("a proven session did not carry the dial: %+v", dials)
	}
}

// The dials' timer reads each source the last snapshot found, keeps a reading
// for the credential it was made with, drops a source no session uses any
// more, and never reads while a board's snapshot is built.
func TestKeySpendReadsEachSourceOnTheDialsTimer(t *testing.T) {
	// Arrange
	now := time.Now().UTC()
	dir := piProfile(t, "fixture-openrouter-key-one", writtenAt)
	served := keySpendReading("key-one", 9.04, 50, writtenAt)
	failing := false
	var bounded atomic.Bool
	fake := &fakeKeySpend{answers: func(openrouter.Source) openrouter.Reading {
		if failing {
			return openrouter.Reading{Status: openrouter.StatusRateLimited, Detail: "OpenRouter rate limited the reading", KeyID: served.KeyID, ReadAt: time.Now().UTC(), Profile: "default"}
		}
		return served
	}}
	service := &Service{Options: Options{KeySpend: func(ctx context.Context, source openrouter.Source) openrouter.Reading {
		if _, has := ctx.Deadline(); has {
			bounded.Store(true)
		}
		return fake.read(ctx, source)
	}, UserEnvironment: userEnvironment(dir)}}
	task := livePiTask("goblin", "openrouter/deepseek/deepseek-v4.1-flash", scheduleStarts)
	task.Runtime = RuntimeEvidence{State: "busy", At: now}
	snapshot := piSnapshot(now, task)
	read := func() map[string]openrouter.Reading {
		service.mu.Lock()
		defer service.mu.Unlock()
		return maps.Clone(service.keySpendReadings)
	}

	// Assert: a board's snapshot asks OpenRouter for nothing.
	if dials := service.keySpendUsage(cfoState{}, snapshot); len(dials) != 0 {
		t.Fatalf("a snapshot with no reading yet showed %+v", dials)
	}
	if got := fake.reads.Load(); got != 0 {
		t.Fatalf("a snapshot read OpenRouter %d times", got)
	}

	// Act: one pass of the dials' timer.
	service.readKeySpends(context.Background(), 10*time.Second)

	// Assert
	if got := fake.reads.Load(); got != 1 {
		t.Fatalf("reads = %d, want one for the one source", got)
	}
	if !bounded.Load() {
		t.Error("the read had no deadline to end it")
	}
	if readings := read(); len(readings) != 1 || readings[keySpendSourceKey(dir, "")].SpentUSD == nil {
		t.Fatalf("readings = %+v", readings)
	}
	if dials := service.keySpendUsage(cfoState{}, snapshot); len(dials) != 1 || dials[0].Status != openrouter.StatusAvailable {
		t.Fatalf("dial after the read = %+v", dials)
	}

	// Act: a read of the same credential that fails keeps its dollars.
	failing = true
	service.readKeySpends(context.Background(), 10*time.Second)

	// Assert
	dials := service.keySpendUsage(cfoState{}, snapshot)
	if len(dials) != 1 || dials[0].Status != openrouter.StatusRateLimited || dials[0].SpentUSD == nil || *dials[0].SpentUSD != 9.04 {
		t.Fatalf("a failed read of the same key = %+v", dials)
	}

	// Act: the session goes, and the next pass keeps no reading of it.
	service.keySpendUsage(cfoState{}, piSnapshot(now))
	service.readKeySpends(context.Background(), 10*time.Second)

	// Assert
	if readings := read(); len(readings) != 0 {
		t.Fatalf("a source no session uses kept its reading: %+v", readings)
	}
}

// The dial's snapshot is a projection of the reading, field by field: no auth
// document, header, credential value or credential file time travels with it.
func TestKeySpendSnapshotProjectsOnlyTheDialFields(t *testing.T) {
	// Arrange
	now := time.Now().UTC()
	dir := piProfile(t, "fixture-openrouter-key-one", writtenAt)
	service := &Service{Options: Options{UserEnvironment: userEnvironment(dir)}}
	service.mu.Lock()
	service.keySpendReadings = map[string]openrouter.Reading{keySpendSourceKey(dir, ""): {
		Status: openrouter.StatusAvailable, SpentUSD: new(9.04), LimitUSD: new(50.0), RemainingUSD: new(40.96), Period: openrouter.PeriodWeekly,
		ReadAt: now, ByokUSD: new(1.2), Blocking: &openrouter.BlockingBudget{Scope: "member", SpentUSD: 32.5, LimitUSD: 50, Period: openrouter.PeriodWeekly},
		KeyID: "3f9c1a2b", Profile: "default", CredentialSince: writtenAt,
	}}
	service.mu.Unlock()
	task := livePiTask("goblin", "openrouter/deepseek/deepseek-v4.1-flash", scheduleStarts)
	task.Runtime = RuntimeEvidence{State: "busy", At: now}

	// Act
	dials := service.keySpendUsage(cfoState{}, piSnapshot(now, task))
	raised, err := json.Marshal(dials)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var projected []map[string]any
	if err := json.Unmarshal(raised, &projected); err != nil {
		t.Fatal(err)
	}
	want := []string{"blocking", "byok_usd", "detail", "includes_byok", "key_id", "limit_usd", "period", "profile", "provider", "read_at", "remaining_usd", "resets_at", "spent_usd", "status"}
	if fields := slices.Sorted(maps.Keys(projected[0])); !slices.Equal(fields, want) {
		t.Fatalf("projection = %v, want %v", fields, want)
	}
	if len(projected) != 1 || projected[0]["provider"] != "openrouter" {
		t.Fatalf("dial = %s", raised)
	}
	if strings.Contains(string(raised), writtenAt.Format(time.RFC3339)) {
		t.Fatalf("the credential's file time reached the snapshot: %s", raised)
	}
}

// The dials' timer reads the sources a snapshot found on its own first pass.
func TestTheDialsTimerReadsTheKeySpendOnItsFirstPass(t *testing.T) {
	// Arrange
	now := time.Now().UTC()
	dir := piProfile(t, "fixture-openrouter-key-one", writtenAt)
	fake := &fakeKeySpend{answers: func(openrouter.Source) openrouter.Reading {
		return keySpendReading("key-one", 9.04, 50, writtenAt)
	}}
	service := &Service{Options: Options{KeySpend: fake.read, UserEnvironment: userEnvironment(dir)}}
	task := livePiTask("goblin", "openrouter/deepseek/deepseek-v4.1-flash", scheduleStarts)
	task.Runtime = RuntimeEvidence{State: "busy", At: now}
	service.keySpendUsage(cfoState{}, piSnapshot(now, task))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	service.keepSubscriptionUsage(ctx, time.Minute)

	// Assert
	if got := fake.reads.Load(); got != 1 {
		t.Fatalf("key reads = %d, want the timer's one pass", got)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.keySpendReadings[keySpendSourceKey(dir, "")].SpentUSD == nil || service.keySpendReadAt.IsZero() {
		t.Fatalf("the reading was not kept: %+v", service.keySpendReadings)
	}
}

// The dial's model and profile rules are the harness's own: the provider Pi's
// launch names, and the profile that environment names.
func TestKeySpendReadsPisProviderAndProfileRules(t *testing.T) {
	for _, test := range []struct{ model, provider string }{
		{"openrouter/deepseek/deepseek-v4.1-flash", "openrouter"},
		{"openrouter/z-ai/glm-5.3-flash", "openrouter"},
		{"deepseek/deepseek-v4.1-flash", "deepseek"},
		{"gpt-6-astra", ""},
		{"", ""},
		{"openrouter/", ""},
		{"/model", ""},
	} {
		if got := modelProvider(test.model); got != test.provider {
			t.Errorf("modelProvider(%q) = %q, want %q", test.model, got, test.provider)
		}
	}
	for _, test := range []struct{ dir, defaultDir, profile string }{
		{`C:\Users\someone\.pi\agent`, `C:\Users\someone\.pi\agent`, "default"},
		{`C:\work\pi-profiles\work`, `C:\Users\someone\.pi\agent`, "work"},
		{"", "", ""},
	} {
		if got := keySpendProfileName(test.dir, test.defaultDir); got != test.profile {
			t.Errorf("keySpendProfileName(%q, %q) = %q, want %q", test.dir, test.defaultDir, got, test.profile)
		}
	}
}

// A credential the environment names tells no file's time, so its reading is
// never claimed for a session that started before it could have.
func TestAttributeKeySpendNeedsTheCredentialsOwnWrittenTime(t *testing.T) {
	session := keySpendSession{Provider: openrouter.Provider, Since: writtenAt.Add(time.Hour)}
	for _, test := range []struct {
		name    string
		reading openrouter.Reading
		started time.Time
		proven  bool
	}{
		{name: "a key written before the session", reading: openrouter.Reading{CredentialSince: writtenAt}, started: session.Since, proven: true},
		{name: "a key the environment names", reading: openrouter.Reading{}, started: session.Since},
		{name: "a key written after the session", reading: openrouter.Reading{CredentialSince: session.Since.Add(time.Second)}, started: session.Since},
		{name: "no session start", reading: openrouter.Reading{CredentialSince: writtenAt}},
	} {
		t.Run(test.name, func(t *testing.T) {
			proven, why := attributeKeySpend(test.reading, keySpendSession{Provider: openrouter.Provider, Since: test.started})
			if proven != test.proven {
				t.Fatalf("proven = %v, want %v (%s)", proven, test.proven, why)
			}
			if !proven && why == "" {
				t.Error("an unproven credential names no reason")
			}
		})
	}
	if quota.MaxAge <= 0 {
		t.Fatal("the reading's age has no bound")
	}
}
