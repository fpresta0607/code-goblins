package supervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/release"
)

// A newer release of Code Goblins reaches the Overlord as an item of its own
// in the Command Center: the board looks for one when it starts and every few
// hours, quietly, and makes the Update item, a run item of its own kind that
// runs goblins update for exactly that release. Only he presses it, from a
// board of his own (http.go), and the update it runs restarts this
// supervisor, so the item's result is finished by the one that comes back.

// releaseCheckEvery is how often the board looks again after its first look,
// and releaseCheckWait bounds one look.
var (
	releaseCheckEvery = 6 * time.Hour
	releaseCheckWait  = 30 * time.Second
)

// Releases is where the board reads Code Goblins' releases, and the version
// of the build it runs; without it the board never looks.
type Releases struct {
	Source  release.Source
	Version string
	Client  *http.Client
}

// ReleaseOffer is the release an Update item installs, as its card shows it:
// the version this board runs and the one it installs, when that was
// published, a few lines of what is new, the page of its notes, what its
// notes say of its signature, and the SHA-256 they list for cfo.exe.
type ReleaseOffer struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Page      string    `json:"page"`
	Published time.Time `json:"published"`
	Notes     []string  `json:"notes"`
	Signing   string    `json:"signing"`
	Publisher string    `json:"publisher,omitempty"`
	Sum       string    `json:"sum,omitempty"`
}

// ReleaseView is the newest release as the board's banner shows it: one newer
// than this board's build, or any, on a board built from a clone, which the
// clone updates instead.
type ReleaseView struct {
	Installed string    `json:"installed"`
	Tag       string    `json:"tag"`
	Page      string    `json:"page"`
	Published time.Time `json:"published"`
	Source    bool      `json:"source"`
}

// watchReleases looks for a newer release now and every releaseCheckEvery,
// and again whenever an update ends without installing, until ctx ends.
func (s *Service) watchReleases(ctx context.Context) {
	for {
		s.checkReleases(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(releaseCheckEvery):
		case <-s.releaseNow:
		}
	}
}

// lookAgain asks the release watch for another look now.
func (s *Service) lookAgain() {
	select {
	case s.releaseNow <- struct{}{}:
	default:
	}
}

// checkReleases reads the newest release and keeps the board's Update item
// and banner to it: one item while a newer release than this build waits,
// for that release, and none once this build is it or the look is off. A
// look that fails keeps what the last one found, and says nothing.
func (s *Service) checkReleases(ctx context.Context) {
	releases := s.Options.Releases
	settings, err := fleetconfig.Read(s.Store.Home.Root)
	if err != nil {
		return
	}
	if !settings.CheckForUpdates {
		s.keepRelease(nil, "", "the update check was turned off in config/fleet.json")
		return
	}
	look, cancel := context.WithTimeout(ctx, releaseCheckWait)
	latest, err := release.Latest(look, releases.Client, releases.Source, s.Store.Home.State)
	cancel()
	if err != nil {
		kept, _, ok := release.Cached(s.Store.Home.State, releases.Source)
		if !ok {
			return
		}
		latest = kept
	}
	view := &ReleaseView{Installed: releases.Version, Tag: latest.Tag, Page: latest.Page, Published: latest.Published}
	switch release.StandingOf(releases.Version, latest.Tag) {
	case release.FromSource:
		view.Source = true
		s.keepRelease(view, "", "this board was built from a clone, which updates it")
	case release.Available:
		s.keepRelease(view, latest.Tag, "release "+latest.Tag+" was published since")
		if err := s.offerUpdate(latest, releases.Version); err != nil {
			s.publish(fmt.Errorf("the Update item for Code Goblins %s could not be made: %w", latest.Tag, err))
		}
	default:
		s.keepRelease(nil, "", "Code Goblins "+releases.Version+" runs now")
	}
	s.notify()
}

// keepRelease shows view in the banner and retires every Update item still
// waiting that is not for tag, with why.
func (s *Service) keepRelease(view *ReleaseView, tag, why string) {
	s.mu.Lock()
	s.release = view
	s.mu.Unlock()
	for _, r := range s.Store.Snapshot().Runs {
		if r.Update != nil && r.State == "ready" && r.Update.To != tag {
			if err := s.Store.retireUpdate(r.ID, why); err != nil {
				s.publish(err)
			}
		}
	}
}

// offerUpdate makes the Update item for latest, unless one for it waits or
// runs already.
func (s *Service) offerUpdate(latest release.Release, installed string) error {
	if slices.ContainsFunc(s.Store.Snapshot().Runs, func(r Run) bool {
		return r.Update != nil && r.Update.To == latest.Tag && (r.State == "ready" || r.State == "running")
	}) {
		return nil
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	now := time.Now().UTC()
	id := "update-" + latest.Tag + "-" + hex.EncodeToString(nonce[:])
	identity := sha256.Sum256([]byte("update\n" + id + "\n" + strconv.FormatInt(now.UnixNano(), 10)))
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	h := s.Store.Home
	command := "$ErrorActionPreference = 'Stop'\n$env:CFO_HOME = " + quote(h.Root) + "\n$env:CFO_STATE_OVERRIDE = " + quote(h.State) + "\n" +
		"& " + quote(filepath.Join(h.Bin(), "goblins.exe")) + " update --to " + latest.Tag + " --run " + id + "\nexit $LASTEXITCODE\n"
	signing, publisher := release.Signing(latest.Notes)
	offer := &ReleaseOffer{From: installed, To: latest.Tag, Page: latest.Page, Published: latest.Published, Notes: release.WhatsNew(latest.Notes, 3),
		Signing: signing, Publisher: publisher, Sum: release.NotedSum(latest.Notes, release.Program)}
	_, err := s.recordBoardRun(Run{ID: id, Identity: hex.EncodeToString(identity[:]), Title: "Update Code Goblins from " + installed + " to " + latest.Tag,
		Shell: "powershell", Command: command, Cwd: h.Root, State: "ready", CreatedAt: now, ExpiresAt: now.Add(runLifetime), Update: offer})
	return err
}

// retireUpdate takes an Update item nobody pressed off the board, with why:
// a newer release replaced it, this build is its release now, or the look
// was turned off. It is the board's own item, which no CFO withdraws.
func (s *Store) retireUpdate(id, why string) error {
	s.mu.Lock()
	i := slices.IndexFunc(s.db.Runs, func(r Run) bool { return r.ID == id && r.Update != nil && r.State == "ready" })
	if i < 0 {
		s.mu.Unlock()
		return nil
	}
	now := time.Now().UTC()
	r := &s.db.Runs[i]
	r.State, r.Reason, r.FinishedAt = "withdrawn", why, &now
	retired := *r
	err := s.save()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return appendRunAudit(s.Home.State, retired, "withdrawn "+why, now)
}

// UpdateGrant is what the board writes when the Overlord presses Update, and
// goblins update reads once: the item he pressed, the release it installs and
// when, so the command it runs is known for his click, which no terminal of
// his opened.
type UpdateGrant struct {
	Run       string    `json:"run"`
	Tag       string    `json:"tag"`
	GrantedAt time.Time `json:"granted_at"`
}

// updateGrantLife is how long a grant waits for the command it was written
// for, which starts within a second.
const updateGrantLife = 2 * time.Minute

func updateGrantPath(stateDir string) string {
	return filepath.Join(stateDir, "update", "grant.json")
}

func grantUpdate(stateDir string, r Run) error {
	data, err := json.Marshal(UpdateGrant{Run: r.ID, Tag: r.Update.To, GrantedAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(updateGrantPath(stateDir)), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(updateGrantPath(stateDir), data)
}

// TakeUpdateGrant reads the grant the board wrote for the Update item run and
// release tag and removes it, so it is used once. It refuses a grant for
// another item or release, one older than its life, and none at all.
func TakeUpdateGrant(stateDir, run, tag string) error {
	path := updateGrantPath(stateDir)
	data, err := fsx.ReadFile(path)
	if err != nil {
		return errors.New("no Update was pressed for this item in the Command Center")
	}
	var grant UpdateGrant
	if err := json.Unmarshal(data, &grant); err != nil {
		return fmt.Errorf("the Command Center's grant for this update cannot be read: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("the Command Center's grant for this update could not be used up: %w", err)
	}
	switch age := time.Since(grant.GrantedAt); {
	case grant.Run != run || grant.Tag != tag:
		return fmt.Errorf("the Update pressed in the Command Center was for %s (%s), not this one", grant.Run, grant.Tag)
	case age < 0 || age > updateGrantLife:
		return errors.New("the Update pressed in the Command Center is too old to run; press Update again")
	}
	return nil
}
