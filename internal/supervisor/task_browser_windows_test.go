package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A goblin's browser is the supervisor's own headless Chrome, its window 1280
// by 800, on a profile in its task's scratch folder; it closes once its
// goblin's terminal no longer answers, after two minutes unused, and hands
// back control nobody watches after 30 minutes, as the board's docs say.
func TestAGoblinsBrowserIsAHeadlessChromeOf1280By800OnItsTasksProfile(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	taskTmp := t.TempDir()
	browsers := newTaskBrowsers(store.Home.State, func() {}, func(string, string) {}, func(err error) { t.Errorf("a browser reported %v", err) })
	t.Cleanup(browsers.closeAll)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Act
	b, err := browsers.ensure(ctx, state.TaskMeta{ID: "task-1", SpawnGen: "g1", TaskTmp: taskTmp})
	if err != nil {
		t.Fatal(err)
	}
	client, err := b.source.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var version struct {
		UserAgent string `json:"userAgent"`
	}
	versionErr := client.Call(ctx, "", "Browser.getVersion", nil, &version)
	var targets struct {
		Infos []targetInfo `json:"targetInfos"`
	}
	targetsErr := client.Call(ctx, "", "Target.getTargets", nil, &targets)
	page := ""
	for _, info := range targets.Infos {
		if info.Type == "page" {
			page = info.ID
		}
	}
	var window struct {
		Bounds struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"bounds"`
	}
	windowErr := client.Call(ctx, "", "Browser.getWindowForTarget", map[string]string{"targetId": page}, &window)
	client.Close()
	browsers.sweep(store.Home.State)
	isRunningAfterSweep := browsers.isRunning("task-1", "g1")
	browsers.closeAll()

	// Assert
	if err := errors.Join(versionErr, targetsErr, windowErr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(version.UserAgent, "HeadlessChrome") {
		t.Fatalf("the browser is %q, want a headless Chrome", version.UserAgent)
	}
	if window.Bounds.Width != 1280 || window.Bounds.Height != 800 {
		t.Fatalf("its window is %dx%d, want 1280x800", window.Bounds.Width, window.Bounds.Height)
	}
	if entries, err := os.ReadDir(filepath.Join(taskTmp, "browser-profile")); err != nil || len(entries) == 0 {
		t.Fatalf("the task's profile holds %d entries, %v; want Chrome's profile there", len(entries), err)
	}
	if isRunningAfterSweep {
		t.Fatal("the browser of a goblin whose terminal does not answer still shows after the sweep")
	}
	if b.source.IsRunning() {
		t.Fatal("Chrome still runs after the supervisor closed it")
	}
	if browsers.idle != 2*time.Minute || browsers.unwatched != 30*time.Minute {
		t.Fatalf("the browser closes after %v unused and hands back after %v unwatched, want 2m and 30m", browsers.idle, browsers.unwatched)
	}
}
