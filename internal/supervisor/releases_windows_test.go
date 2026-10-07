package supervisor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/release"
)

// releaseSource stands in for GitHub's latest release, as the release
// workflow publishes it: its tag, its notes opening with whether it is signed
// and the table of sums, and how often the board asked.
type releaseSource struct {
	*httptest.Server
	mu    sync.Mutex
	tag   string
	asked atomic.Int32
}

func newReleaseSource(t *testing.T, tag string) *releaseSource {
	t.Helper()
	r := &releaseSource{tag: tag}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r.asked.Add(1)
		r.mu.Lock()
		tag := r.tag
		r.mu.Unlock()
		notes := "**This release is unsigned.**\n\n| File | SHA-256 |\n| --- | --- |\n| `cfo.exe` | `" + strings.Repeat("3f", 32) + "` |\n\n## What's Changed\n* feat(board): an update arrives in the Command Center by @fpresta0607 in https://example.invalid/pull/1\n"
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "html_url": r.URL + "/tag/" + tag, "published_at": "2026-10-06T14:02:00Z", "body": notes})
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *releaseSource) publish(tag string) {
	r.mu.Lock()
	r.tag = tag
	r.mu.Unlock()
}

// releaseService is a board on version, reading releases from source.
func releaseService(t *testing.T, source *releaseSource, version string) *Service {
	t.Helper()
	store, _ := testStore(t)
	return &Service{Store: store, Instance: "test-instance", subscribers: map[chan struct{}]struct{}{}, work: make(chan struct{}, 1), releaseNow: make(chan struct{}, 1),
		Options: Options{Releases: &Releases{Source: release.Source{API: source.URL + "/latest", Site: source.URL + "/", Downloads: source.URL + "/"}, Version: version, Client: http.DefaultClient}}}
}

func updateItems(s *Service) []Run {
	var items []Run
	for _, r := range s.Store.Snapshot().Runs {
		if r.Update != nil {
			items = append(items, r)
		}
	}
	return items
}

// A newer release arrives as one Update item of its own, for exactly that
// release, with what its card shows: the two versions, what is new, what the
// notes say of the signature and the SHA-256 of cfo.exe. Looking again makes
// no second item; a newer release replaces it, and the build becoming that
// release retires it, each with why.
func TestTheBoardOffersANewerReleaseAsItsOwnUpdateItem(t *testing.T) {
	// Arrange
	source := newReleaseSource(t, "v0.5.0")
	s := releaseService(t, source, "v0.4.2")

	// Act
	s.checkReleases(context.Background())
	s.checkReleases(context.Background())

	// Assert
	items := updateItems(s)
	if len(items) != 1 {
		t.Fatalf("the board made %d Update items, want one: %+v", len(items), items)
	}
	item := items[0]
	want := ReleaseOffer{From: "v0.4.2", To: "v0.5.0", Page: source.URL + "/tag/v0.5.0", Published: time.Date(2026, 10, 6, 14, 2, 0, 0, time.UTC), Notes: []string{"An update arrives in the Command Center"}, Signing: "unsigned", Sum: strings.Repeat("3f", 32)}
	if got := *item.Update; got.From != want.From || got.To != want.To || got.Page != want.Page || !got.Published.Equal(want.Published) || strings.Join(got.Notes, "|") != strings.Join(want.Notes, "|") || got.Signing != want.Signing || got.Sum != want.Sum {
		t.Fatalf("the item offers %+v, want %+v", got, want)
	}
	if item.State != "ready" || item.Title != "Update Code Goblins from v0.4.2 to v0.5.0" || !strings.Contains(item.Command, "goblins.exe' update --to v0.5.0 --run "+item.ID) {
		t.Fatalf("the item is %+v", item)
	}
	if view := s.release; view == nil || view.Tag != "v0.5.0" || view.Installed != "v0.4.2" || view.Source {
		t.Fatalf("the banner shows %+v", view)
	}

	// Act: a newer release is published.
	source.publish("v0.5.1")
	s.checkReleases(context.Background())

	// Assert
	items = updateItems(s)
	if len(items) != 2 || items[0].State != "withdrawn" || items[0].Reason != "release v0.5.1 was published since" || items[1].State != "ready" || items[1].Update.To != "v0.5.1" {
		t.Fatalf("after v0.5.1 the items are %+v", items)
	}

	// Act: the board now runs that release.
	s.Options.Releases.Version = "v0.5.1"
	s.checkReleases(context.Background())

	// Assert
	items = updateItems(s)
	if items[1].State != "withdrawn" || items[1].Reason != "Code Goblins v0.5.1 runs now" || s.release != nil {
		t.Fatalf("on v0.5.1 the items are %+v and the banner %+v", items, s.release)
	}
}

// config/fleet.json's check_for_updates: false turns the look off: the board
// asks nothing, shows no banner and retires an item that waits.
func TestTheUpdateCheckCanBeTurnedOff(t *testing.T) {
	// Arrange
	source := newReleaseSource(t, "v0.5.0")
	s := releaseService(t, source, "v0.4.2")
	s.checkReleases(context.Background())
	asked := source.asked.Load()
	if err := os.MkdirAll(filepath.Join(s.Store.Home.Root, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Store.Home.Root, "config", "fleet.json"), []byte(`{"check_for_updates": false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	s.checkReleases(context.Background())

	// Assert
	if source.asked.Load() != asked {
		t.Fatal("the board asked for the latest release with the check turned off")
	}
	if items := updateItems(s); items[0].State != "withdrawn" || s.release != nil {
		t.Fatalf("with the check off the item is %+v and the banner %+v", items[0], s.release)
	}
}

// A board built from a clone updates from the clone: it shows the banner and
// makes no Update item. One whose look fails keeps what it knew.
func TestABoardBuiltFromACloneGetsTheBannerAndNoItem(t *testing.T) {
	// Arrange
	source := newReleaseSource(t, "v0.5.0")
	s := releaseService(t, source, "main-8c10c45b")

	// Act
	s.checkReleases(context.Background())
	source.Close()
	s.checkReleases(context.Background())

	// Assert
	if items := updateItems(s); len(items) != 0 {
		t.Fatalf("a board built from a clone got Update items %+v", items)
	}
	if view := s.release; view == nil || !view.Source || view.Tag != "v0.5.0" || view.Installed != "main-8c10c45b" {
		t.Fatalf("the banner shows %+v", view)
	}
}

// An Update item waits until a newer release or the build replaces it, never
// expiring after a day as a command does, and it is the Overlord's: the CFO
// cannot withdraw it.
func TestAnUpdateItemWaitsForHimAndIsNotTheCFOsToWithdraw(t *testing.T) {
	// Arrange
	source := newReleaseSource(t, "v0.5.0")
	s := releaseService(t, source, "v0.4.2")
	s.checkReleases(context.Background())
	item := updateItems(s)[0]

	// Act
	expireErr := s.Store.expireRuns(time.Now().Add(72 * time.Hour))
	withdrawErr := s.Store.withdrawRun(item.ID, "the CFO wants it gone")

	// Assert
	if expireErr != nil || updateItems(s)[0].State != "ready" {
		t.Fatalf("after three days the item is %s (%v), want it still ready", updateItems(s)[0].State, expireErr)
	}
	if withdrawErr == nil || !strings.Contains(withdrawErr.Error(), "not the CFO's to withdraw") {
		t.Fatalf("the CFO's withdrawal = %v, want it refused", withdrawErr)
	}
}

// Update is pressed only on a board of the Overlord's own, proven as AFK
// mode's switch is: a browser an agent started is refused, and nothing runs.
func TestOnlyABoardOfTheOverlordsOwnPressesUpdate(t *testing.T) {
	cases := []struct {
		name     string
		ancestry func(pid int) []proc.Entry
		status   int
	}{
		{"his own window", nil, http.StatusAccepted},
		{"a browser an agent started", func(pid int) []proc.Entry {
			started := time.Now().Add(-time.Hour)
			return []proc.Entry{{PID: pid, ParentPID: 5100, ExeBase: "chrome.exe", Start: started}, {PID: 5100, ParentPID: 5000, ExeBase: "node.exe", Start: started.Add(-time.Second)}, {PID: 5000, ParentPID: 900, ExeBase: "claude.exe", Start: started.Add(-time.Minute)}, {PID: 900, ExeBase: "explorer.exe", Start: started.Add(-time.Hour)}}
		}, http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			source := newReleaseSource(t, "v0.5.0")
			s := releaseService(t, source, "v0.4.2")
			s.checkReleases(context.Background())
			item := updateItems(s)[0]
			asOverlordsBoard(s)
			if c.ancestry != nil {
				s.inspectCaller = func(pid int) ([]proc.Entry, []string, error) { return c.ancestry(pid), nil, nil }
			}

			// Act
			status, body := askTheBoard(t, s, "POST", "/api/actions", `{"id":"press-1","kind":"run","run_id":"`+item.ID+`","generation":"`+item.Identity+`"}`, nil)

			// Assert
			if status != c.status {
				t.Fatalf("Update from %s answered %d %s, want %d", c.name, status, body, c.status)
			}
			if c.status == http.StatusForbidden && !strings.Contains(body, "Updating Code Goblins is the Supreme Overlord's alone") {
				t.Fatalf("the refusal says %s", body)
			}
			if c.status == http.StatusForbidden && len(s.Store.Snapshot().Actions) != 0 {
				t.Fatalf("a refused Update queued %+v", s.Store.Snapshot().Actions)
			}
		})
	}
}

// Pressing Update runs its command out of sight, with a grant for exactly
// that item and release, which the command takes once.
func TestPressingUpdateGrantsItsCommandOnceAndRunsItHidden(t *testing.T) {
	// Arrange
	source := newReleaseSource(t, "v0.5.0")
	s := releaseService(t, source, "v0.4.2")
	launcher := &fakeRunLauncher{started: liveStart(t)}
	s.Options.Runs = launcher
	s.checkReleases(context.Background())
	item := updateItems(s)[0]

	// Act
	pressRun(t, s, item, "press-1")

	// Assert
	launches := launcher.all()
	if len(launches) != 1 || !launches[0].Hidden || launches[0].Admin || launches[0].Interactive {
		t.Fatalf("Update launched %+v, want one hidden run", launches)
	}
	if err := TakeUpdateGrant(s.Store.Home.State, item.ID, "v0.5.0"); err != nil {
		t.Fatalf("the grant for the item pressed: %v", err)
	}
	if err := TakeUpdateGrant(s.Store.Home.State, item.ID, "v0.5.0"); err == nil {
		t.Fatal("the grant was taken twice")
	}
}

func TestAnUpdateItemPastItsExpiryCanStillBePressedAndLaunched(t *testing.T) {
	source := newReleaseSource(t, "v0.5.0")
	s := releaseService(t, source, "v0.4.2")
	launcher := &fakeRunLauncher{started: liveStart(t)}
	s.Options.Runs = launcher
	s.checkReleases(context.Background())
	s.Store.db.Runs[0].ExpiresAt = time.Now().Add(-48 * time.Hour)
	item := updateItems(s)[0]
	asOverlordsBoard(s)

	status, body := askTheBoard(t, s, "POST", "/api/actions", `{"id":"press-late","kind":"run","run_id":"`+item.ID+`","generation":"`+item.Identity+`"}`, nil)
	if status != http.StatusAccepted {
		t.Fatalf("Update after its expiry answered %d %s, want %d", status, body, http.StatusAccepted)
	}
	if err := s.Store.ProcessOne(context.Background(), s.execute); err != nil {
		t.Fatal(err)
	}

	launches := launcher.all()
	if len(launches) != 1 || !launches[0].Hidden {
		t.Fatalf("Update launched %+v, want one hidden run", launches)
	}
	if got := updateItems(s)[0]; got.State != "running" || got.Started == nil {
		t.Fatalf("the item is %+v, want it launched", got)
	}
}

// A grant runs only the item and release it was written for, and only
// while it is fresh, so a grant left behind never runs an update later.
func TestAnUpdateGrantRunsOnlyItsOwnFreshItem(t *testing.T) {
	cases := []struct {
		name, run, tag string
		age            time.Duration
		refused        string
	}{
		{"its own item", "update-v0.5.0-1", "v0.5.0", 0, ""},
		{"another release", "update-v0.5.0-1", "v0.5.1", 0, "not this one"},
		{"another item", "update-v0.5.0-2", "v0.5.0", 0, "not this one"},
		{"an old grant", "update-v0.5.0-1", "v0.5.0", updateGrantLife + time.Minute, "too old"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			data, err := json.Marshal(UpdateGrant{Run: "update-v0.5.0-1", Tag: "v0.5.0", GrantedAt: time.Now().Add(-c.age)})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(updateGrantPath(stateDir)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(updateGrantPath(stateDir), data, 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			err = TakeUpdateGrant(stateDir, c.run, c.tag)

			// Assert
			if c.refused == "" && err != nil || c.refused != "" && (err == nil || !strings.Contains(err.Error(), c.refused)) {
				t.Fatalf("TakeUpdateGrant = %v, want refused: %q", err, c.refused)
			}
			if _, statErr := os.Stat(updateGrantPath(stateDir)); !os.IsNotExist(statErr) {
				t.Fatalf("the grant is still there after it was read (%v)", statErr)
			}
		})
	}
}

// A hidden run has no window to keep open: its command runs, its exit code
// and output are kept, and its process ends on its own.
func TestAHiddenRunEndsWithItsCommand(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "command.ps1")
	command := "\xef\xbb\xbf" + `$ErrorActionPreference = 'Stop'
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class ConsoleVisibility {
    [DllImport("kernel32.dll")]
    public static extern IntPtr GetConsoleWindow();
    [DllImport("user32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static extern bool IsWindowVisible(IntPtr window);
}
'@
Write-Output ('console-visible=' + [ConsoleVisibility]::IsWindowVisible([ConsoleVisibility]::GetConsoleWindow()))
Write-Output 'updated'
exit 3
`
	if err := os.WriteFile(script, []byte(command), 0o600); err != nil {
		t.Fatal(err)
	}

	started, err := OSRunLauncher{}.Launch(context.Background(), RunLaunch{Shell: "powershell", Script: script, Dir: dir, Cwd: dir, Hidden: true})

	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for runProcessAlive(started.PID, started.Start) {
		if time.Now().After(deadline) {
			t.Fatal("the hidden run's process still runs a minute after its command")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if code, ok := readRunExit(dir); !ok || code != 3 {
		t.Fatalf("exit.txt = %d, %v; want 3", code, ok)
	}
	if output := readRunOutput(dir); !strings.Contains(output, "updated") || !strings.Contains(output, "console-visible=False") {
		t.Fatalf("output.log = %q", output)
	}
}
