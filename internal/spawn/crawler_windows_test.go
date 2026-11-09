package spawn

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/crawler"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// setUpCrawler gives homeRoot the crawler connector's commands, as ceo
// crawler setup leaves them.
func setUpCrawler(t *testing.T, homeRoot string) {
	t.Helper()
	bin := makeDir(t, crawler.BinDir(homeRoot))
	writeFile(t, filepath.Join(bin, crawler.Command+".cmd"), "@node \"%~dp0..\\goblin-crawl.mjs\" %*\r\n")
}

// crawlerOnPath is the folder of launch's PATH a goblin's terminal finds
// goblin-crawl in, or "" when it finds it in none.
func crawlerOnPath(launch codexEvent) string {
	path := launch.Env["PATH"]
	if path == nil {
		return ""
	}
	for _, dir := range filepath.SplitList(*path) {
		if crawler.IsCommandIn(dir) {
			return dir
		}
	}
	return ""
}

// Every goblin carries the crawler connector whichever harness it runs, with
// no word of it in its brief: its terminal finds goblin-crawl on PATH, in the
// home's crawler folder, and its record says nothing of the crawler.
func TestAGoblinOfEveryHarnessFindsTheCrawlerOnItsTerminalsPath(t *testing.T) {
	// The test is about the terminal's PATH, not how fast a host starts: its
	// three spawns each start a host from the test binary, and on a loaded
	// machine one took past the 15 s a host has to record itself.
	usual := host.LaunchTimeout
	host.LaunchTimeout = 2 * time.Minute
	t.Cleanup(func() { host.LaunchTimeout = usual })
	// Each fake harness draws its own composer: the fake Claude Code its
	// composer only in one of its claude modes.
	for _, test := range []struct {
		kind harness.Kind
		mode string
	}{{harness.Claude, "claude-queues"}, {harness.Codex, "ready"}, {harness.Pi, "pi-ready"}} {
		kind := test.kind
		t.Run(string(kind), func(t *testing.T) {
			// Arrange
			f := newNativeFixture(t, kind, test.mode)
			setUpCrawler(t, f.service.HomeRoot)
			brief, err := os.ReadFile(f.brief)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(brief), "connectors") || strings.Contains(string(brief), "crawl") {
				t.Fatalf("the brief names the crawler, so it proves nothing of a brief that does not:\n%s", brief)
			}

			// Act
			result, err := f.service.Spawn(context.Background(), f.request)

			// Assert
			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			if got, want := crawlerOnPath(named(f.events(t), "env")[0]), crawler.BinDir(f.service.HomeRoot); !strings.EqualFold(got, want) {
				t.Errorf("the %s goblin's terminal finds goblin-crawl in %q, want %q", kind, got, want)
			}
			if result.Meta.IsCrawlerLeftOut {
				t.Error("the task's record leaves the crawler out though its brief did not")
			}
		})
	}
}

// A brief that leaves the crawler out with connectors: none gets a terminal
// that finds no goblin-crawl, and its record keeps the choice, so a switch or
// a resume of the task leaves it out too.
func TestABriefThatLeavesTheCrawlerOutGetsATerminalWithoutIt(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	setUpCrawler(t, f.service.HomeRoot)
	briefAsking(t, f.fixture, "credentials: none\nconnectors: none")

	// Act
	result, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := crawlerOnPath(named(f.events(t), "env")[0]); got != "" {
		t.Errorf("the terminal finds goblin-crawl in %s, want it nowhere on its PATH", got)
	}
	recorded, err := state.ReadTaskMeta(f.stateDir, f.request.ID)
	if err != nil || !result.Meta.IsCrawlerLeftOut || !recorded.IsCrawlerLeftOut {
		t.Errorf("the record leaves the crawler out = %v (read back %v, %v), want true", result.Meta.IsCrawlerLeftOut, recorded.IsCrawlerLeftOut, err)
	}
}

// A brief whose connectors line names no default connector is refused before
// anything starts, rather than read as one choice or the other.
func TestASpawnRefusesABriefNamingAConnectorThatIsNone(t *testing.T) {
	// Arrange
	f := newFixture(t)
	briefAsking(t, f, "credentials: none\nconnectors: firecrawl")

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err == nil || !strings.Contains(err.Error(), `connectors line says "firecrawl"`) {
		t.Fatalf("Spawn = %v, want the connectors line refused", err)
	}
	if _, readErr := state.ReadTaskMeta(f.stateDir, f.request.ID); !os.IsNotExist(readErr) {
		t.Errorf("the refused start left a task record: %v", readErr)
	}
}

// A helper never carries more than its parent: the helper of a goblin whose
// brief left the crawler out goes without it, though its own brief has no
// connectors line.
func TestAHelperOfAGoblinWithoutTheCrawlerGoesWithoutIt(t *testing.T) {
	for _, test := range []struct {
		name            string
		isParentLeftOut bool
		helperBrief     string
		shouldBeLeftOut bool
	}{
		{name: "a parent that carries it", helperBrief: "## Authentication\ncredentials: none\nmcp: none\n"},
		{name: "a parent that carries it and a helper brief that leaves it out", helperBrief: "## Authentication\nconnectors: none\n", shouldBeLeftOut: true},
		{name: "a parent without it", isParentLeftOut: true, helperBrief: "## Authentication\ncredentials: none\nmcp: none\n", shouldBeLeftOut: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "parent-1", Kind: "ship", Mode: "no-mistakes", IsCrawlerLeftOut: test.isParentLeftOut}); err != nil {
				t.Fatal(err)
			}

			// Act
			isLeftOut, err := Service{StateDir: stateDir}.crawlerLeftOut(Request{ID: "helper-1", Parent: "parent-1"}, []byte(test.helperBrief))

			// Assert
			if err != nil || isLeftOut != test.shouldBeLeftOut {
				t.Errorf("crawlerLeftOut = %v, %v, want %v", isLeftOut, err, test.shouldBeLeftOut)
			}
		})
	}
}

// The home's crawler folder follows a goblin's codex guard on its PATH and
// comes ahead of the user's own PATH, and a launch that names no crawler
// folder, as one whose brief left it out, adds nothing.
func TestTheCrawlerFolderFollowsTheCodexGuardOnPath(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	t.Setenv("CEO_CODEX_GUARD", "")
	service := Service{StateDir: stateDir, HomeRoot: filepath.Dir(stateDir)}
	userPath := `C:\Users\someone\AppData\Roaming\npm;C:\Windows\System32`
	userEnv := []string{"Path=" + userPath}
	guard := filepath.Join(stateDir, "codex-guard", "g1")
	bin := crawler.BinDir(service.HomeRoot)

	// Act
	carrying := service.nativeHostEnvironment(userEnv, harness.Launch{Env: map[string]string{"CEO_TASK_ID": "g1", crawler.PathVariable: bin}}, auth.Result{})
	leftOut := service.nativeHostEnvironment(userEnv, harness.Launch{Env: map[string]string{"CEO_TASK_ID": "g1"}}, auth.Result{})

	// Assert
	if got, want := nativeVariable(carrying, "PATH"), strings.Join([]string{guard, bin, userPath}, ";"); got != want {
		t.Errorf("PATH = %q, want %q", got, want)
	}
	if got := nativeVariable(leftOut, "PATH"); slices.Contains(filepath.SplitList(got), bin) {
		t.Errorf("PATH = %q, want no crawler folder", got)
	}
}
