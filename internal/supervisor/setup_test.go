package supervisor

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/onboarding"
)

func firstRunFixture(t *testing.T) *FirstRun {
	t.Helper()
	return &FirstRun{
		Home: t.TempDir(), DefaultAgent: func() (string, error) { return "codex", nil },
		Detect: func(_ context.Context, name string) onboarding.Agent {
			return onboarding.Agent{ID: name, Name: name, Installed: true, SignedIn: true}
		},
		CFORuns: func() bool { return false }, StartCFO: func(string) error { return nil }, Save: func(string) error { return nil },
		Memory: func() (uint64, uint64, error) { return 8 << 30, 32 << 30, nil },
	}
}

func TestFirstRunShowsHomeSavedDefaultAndVerifiedAgents(t *testing.T) {
	run := firstRunFixture(t)
	setup := run.Setup(context.Background())
	if setup.Home != run.Home || setup.DefaultAgent != "codex" || len(setup.Agents) != 3 || setup.CFORuns {
		t.Fatalf("setup=%+v", setup)
	}
	for index, name := range onboarding.Agents {
		if setup.Agents[index].ID != name || !setup.Agents[index].SignedIn {
			t.Fatalf("agent=%+v", setup.Agents[index])
		}
	}
}

func TestFirstRunReportsAnUnreadableDefault(t *testing.T) {
	run := firstRunFixture(t)
	run.DefaultAgent = func() (string, error) { return "", errors.New("unreadable default") }
	if got := run.Setup(context.Background()); got.Problem != "unreadable default" {
		t.Fatalf("setup=%+v", got)
	}
}

func TestFirstRunRecordsAndStartsTheSelectedAgent(t *testing.T) {
	for _, agent := range onboarding.Agents {
		run := firstRunFixture(t)
		saved, started := "", ""
		run.Save = func(name string) error { saved = name; return nil }
		run.StartCFO = func(name string) error { started = name; return nil }
		if err := run.Start(context.Background(), agent); err != nil || saved != agent || started != agent {
			t.Fatalf("agent=%s saved=%s started=%s err=%v", agent, saved, started, err)
		}
	}
}

func TestFirstRunRefusesUnknownUnverifiedLiveOrMemoryBlockedStarts(t *testing.T) {
	for _, name := range []string{"unknown", "unsigned", "absent", "running", "memory", "memory unreadable", "save failed"} {
		t.Run(name, func(t *testing.T) {
			run := firstRunFixture(t)
			agent := "claude"
			run.StartCFO = func(string) error { t.Fatal("started a refused CFO"); return nil }
			switch name {
			case "unknown":
				agent = "unknown"
			case "unsigned", "absent":
				run.Detect = func(_ context.Context, agent string) onboarding.Agent {
					return onboarding.Agent{ID: agent, Installed: name == "unsigned", Reason: "Sign-in needed"}
				}
			case "running":
				run.CFORuns = func() bool { return true }
			case "memory":
				run.Memory = func() (uint64, uint64, error) { return 3 << 30, 32 << 30, nil }
			case "memory unreadable":
				run.Memory = func() (uint64, uint64, error) { return 0, 0, errors.New("unreadable") }
			case "save failed":
				run.Save = func(string) error { return errors.New("read-only state") }
			}
			if err := run.Start(context.Background(), agent); err == nil {
				t.Fatal("accepted refused start")
			}
		})
	}
}

func TestBoardSetupStartsCFOAndRejectsFailureAndForeignOrigin(t *testing.T) {
	for _, test := range []struct {
		name, origin, agent string
		want                int
	}{
		{"ready", "http://board.local", "codex", 200},
		{"unknown", "http://board.local", "unknown", 409},
		{"foreign", "http://other.local", "claude", 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := firstRunFixture(t)
			wasStarted := false
			run.StartCFO = func(string) error { wasStarted = true; return nil }
			service := &Service{Instance: "instance", Options: Options{FirstRun: run}, subscribers: map[chan struct{}]struct{}{}}
			handler := NewHTTP(service, "board.local", nil)
			req := httptest.NewRequest("POST", "http://board.local/api/setup/start", strings.NewReader(`{"agent":"`+test.agent+`"}`))
			req.Header.Set("Origin", test.origin)
			req.Header.Set("X-CFO-Token", "instance")
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != test.want || wasStarted != (test.want == 200) {
				t.Fatalf("status=%d started=%t body=%s", response.Code, wasStarted, response.Body.String())
			}
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/setup", nil))
			if response.Code != 200 || !strings.Contains(response.Body.String(), `"default_agent":"codex"`) {
				t.Fatalf("setup=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestBoardWithoutSetupRefusesStart(t *testing.T) {
	handler := NewHTTP(&Service{Instance: "instance"}, "board.local", nil)
	req := httptest.NewRequest("POST", "http://board.local/api/setup/start", strings.NewReader(`{"agent":"claude"}`))
	req.Header.Set("Origin", "http://board.local")
	req.Header.Set("X-CFO-Token", "instance")
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 409 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

// With no CFO registered and no native terminal cfo up, the snapshot says no
// CFO runs, so the board shows its first-run page.
func TestASnapshotSaysWhenNoCFORuns(t *testing.T) {
	// Arrange
	store, _ := testStore(t)

	// Act
	snapshot, err := (&Service{Store: store}).Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CFORuns {
		t.Fatal("the snapshot says a CFO runs with none registered or starting")
	}
}
