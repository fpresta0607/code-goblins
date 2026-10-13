package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/keypages"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const browserSignInLifetime = 24 * time.Hour

// BrowserSignIn asks for a sign-in in a browser window opened by the
// person's click. It carries no browser address or page content.
type BrowserSignIn struct {
	ID             string     `json:"id"`
	Generation     string     `json:"generation"`
	Login          string     `json:"login"`
	Provider       string     `json:"provider"`
	Account        string     `json:"account"`
	Project        string     `json:"project"`
	Task           string     `json:"task"`
	TaskGeneration string     `json:"task_generation,omitempty"`
	State          string     `json:"state"`
	Reason         string     `json:"reason"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
	VerifiedAt     *time.Time `json:"verified_at,omitempty"`
}

func (s *Service) requestBrowserSignIn(request BrowserRequest) (BrowserLease, error) {
	s.browserSignins.Lock()
	defer s.browserSignins.Unlock()
	if err := state.ValidTaskID(request.Task); err != nil {
		return BrowserLease{}, err
	}
	provider, err := s.browsers.recipes.Provider(request.Provider)
	if err != nil {
		return BrowserLease{}, err
	}
	if provider.KeyPage == "" {
		return BrowserLease{}, errors.New("this provider has no reviewed sign-in page")
	}
	meta, err := s.browsers.grantedTask(request.Task, provider.ID)
	if err != nil {
		return BrowserLease{}, err
	}
	if err := s.browsers.live(request.Task); err != nil {
		return BrowserLease{}, err
	}
	project := auth.ProjectName(meta.Project)
	login, err := s.browsers.prepareSignIn(request.Task, project, provider)
	if err != nil {
		return BrowserLease{}, err
	}
	for _, card := range s.Store.Snapshot().BrowserSignIns {
		if card.Task == request.Task && card.TaskGeneration == meta.SpawnGen && card.Login == login.ID && card.ClosedAt == nil && s.browsers.now().Before(card.ExpiresAt) {
			return BrowserLease{Login: card.Login, Provider: card.Provider, Account: card.Account, SignIn: card.ID}, nil
		}
	}
	now := s.browsers.now()
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return BrowserLease{}, err
	}
	card := BrowserSignIn{ID: "signin-" + hex.EncodeToString(random[:16]), Generation: hex.EncodeToString(random[16:]), Login: login.ID, Provider: provider.Name, Account: login.Account, Project: project, Task: request.Task, TaskGeneration: meta.SpawnGen, State: "ready", CreatedAt: now, ExpiresAt: now.Add(browserSignInLifetime)}
	s.Store.mu.Lock()
	defer s.Store.mu.Unlock()
	for _, existing := range s.Store.db.BrowserSignIns {
		if existing.Login == login.ID && existing.ClosedAt == nil && now.Before(existing.ExpiresAt) {
			return BrowserLease{}, errors.New("this login already has a sign-in card for another task")
		}
	}
	kept := slices.DeleteFunc(slices.Clone(s.Store.db.BrowserSignIns), func(card BrowserSignIn) bool {
		return card.ClosedAt != nil && now.Sub(*card.ClosedAt) >= 7*24*time.Hour
	})
	if len(kept) >= 64 {
		closed := slices.IndexFunc(kept, func(card BrowserSignIn) bool { return card.ClosedAt != nil })
		if closed < 0 {
			return BrowserLease{}, errors.New("the board already holds 64 open browser sign-in cards")
		}
		kept = slices.Delete(kept, closed, closed+1)
	}
	s.Store.db.BrowserSignIns = append(kept, card)
	if err := s.Store.save(); err != nil {
		return BrowserLease{}, err
	}
	s.notify()
	return BrowserLease{Login: card.Login, Provider: card.Provider, Account: card.Account, SignIn: card.ID}, nil
}

func (b *browserBroker) prepareSignIn(task, project string, provider keypages.Provider) (browserlogin.Login, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return browserlogin.Login{}, errors.New("the supervisor is stopping")
	}
	logins, err := browserlogin.Read(b.state)
	if err != nil {
		return browserlogin.Login{}, err
	}
	index, err := projectLogin(logins, provider, project)
	if err != nil {
		return browserlogin.Login{}, err
	}
	if index < 0 {
		logins = append(logins, browserlogin.Login{ID: browserlogin.DefaultID(provider.ID, project), Provider: provider.ID, Account: project, Projects: []string{project}})
		index = len(logins) - 1
	}
	login := logins[index]
	if login.Browser != nil {
		if login.Browser.Holder != task {
			return browserlogin.Login{}, errors.New("another task holds this login's browser")
		}
		if err := b.close(logins, index); err != nil {
			return browserlogin.Login{}, err
		}
	}
	return login, browserlogin.Write(b.state, logins)
}

func (s *Store) updateBrowserSignIn(id string, change func(*BrowserSignIn) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := slices.IndexFunc(s.db.BrowserSignIns, func(card BrowserSignIn) bool { return card.ID == id })
	if index < 0 {
		return errors.New("this sign-in card is gone. Refresh the board")
	}
	if err := change(&s.db.BrowserSignIns[index]); err != nil {
		return err
	}
	return s.save()
}

func (s *Service) openBrowserSignIn(id, generation string) error {
	s.browserSignins.Lock()
	defer s.browserSignins.Unlock()
	err := s.Store.updateBrowserSignIn(id, func(card *BrowserSignIn) error {
		if card.Generation != generation || card.ClosedAt != nil {
			return errors.New("this sign-in card is no longer current. Refresh the board")
		}
		if s.browsers == nil || !s.browsers.now().Before(card.ExpiresAt) {
			return errors.New("this sign-in card expired")
		}
		if card.State != "ready" {
			return nil
		}
		if expires := s.browsers.now().Add(browserLease); expires.Before(card.ExpiresAt) {
			card.ExpiresAt = expires
		}
		card.State, card.Reason = "opening", ""
		return nil
	})
	if err == nil {
		s.notify()
	}
	return err
}

func (h *HTTP) openBrowserSignIn(w http.ResponseWriter, r *http.Request) {
	if offMachine(r, h.Host) != "" {
		apiError(w, http.StatusForbidden, "Open the sign-in window from the board on this PC")
		return
	}
	var input struct {
		ID         string `json:"id"`
		Generation string `json:"generation"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, "Send the sign-in card's ID and generation")
		return
	}
	if err := h.Service.openBrowserSignIn(input.ID, input.Generation); err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	respond(w, http.StatusOK, struct {
		ID string `json:"id"`
	}{input.ID})
}

// processBrowserSignIns is run by the browser keeper. Only an opening card,
// recorded by a local click, can start a window.
func (s *Service) processBrowserSignIns(ctx context.Context) error {
	s.browserSignins.Lock()
	defer s.browserSignins.Unlock()
	var failed []error
	for _, card := range s.Store.Snapshot().BrowserSignIns {
		if card.ClosedAt != nil {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		now := s.browsers.now()
		provider, grantErr := s.browsers.signInGrant(card)
		newState, reason := card.State, card.Reason
		switch {
		case card.VerifiedAt != nil:
			s.browsers.endSignIn(card)
			newState, reason = "signed_in", ""
		case !now.Before(card.ExpiresAt) || grantErr != nil:
			s.browsers.endSignIn(card)
			newState, reason = "expired", "This sign-in request is no longer needed"
		case card.State == "opening":
			if err := s.browsers.startSignIn(ctx, card); err != nil {
				newState, reason = "ready", err.Error()
			} else {
				newState, reason = "open", ""
			}
		case card.State == "open":
			s.browsers.mu.Lock()
			process, isRunning := s.browsers.processes[card.Login]
			isRunning = isRunning && s.browsers.signing[card.Login] == card.ID && s.browsers.alive(process)
			s.browsers.mu.Unlock()
			if !isRunning {
				newState, reason = "ready", "The window closed before you signed in. Open it again, or paste the keys on their own card."
			} else {
				check, cancel := context.WithTimeout(ctx, 5*time.Second)
				isSignedIn, err := s.browsers.signedIn(check, process.URL, provider)
				cancel()
				if err != nil {
					newState, reason = "ready", "The page could not be checked. Open the sign-in window again."
				} else if isSignedIn {
					// Keep the verification before closing the window, so a failed
					// completion save can be retried without another sign-in.
					if err := s.Store.updateBrowserSignIn(card.ID, func(current *BrowserSignIn) error {
						current.VerifiedAt = &now
						return nil
					}); err != nil {
						failed = append(failed, err)
						continue
					}
					newState, reason = "signed_in", ""
				}
			}
			if newState != "open" {
				s.browsers.endSignIn(card)
			}
		}
		if newState == card.State && reason == card.Reason {
			continue
		}
		generation := card.Generation
		if newState == "ready" {
			var random [16]byte
			if _, err := rand.Read(random[:]); err != nil {
				failed = append(failed, err)
				continue
			}
			generation = hex.EncodeToString(random[:])
		}
		if err := s.Store.updateBrowserSignIn(card.ID, func(current *BrowserSignIn) error {
			current.State, current.Reason, current.Generation = newState, reason, generation
			if newState == "signed_in" || newState == "expired" {
				current.ClosedAt = &now
			}
			return nil
		}); err != nil {
			failed = append(failed, err)
		} else {
			s.notify()
		}
	}
	return errors.Join(failed...)
}

// signInGrant is the provider of card's login, and why card can no longer
// open a window, nil while it can: its project still has the login, and the
// task that asked is the same live task, still carrying the provider's
// declared service.
func (b *browserBroker) signInGrant(card BrowserSignIn) (keypages.Provider, error) {
	logins, err := browserlogin.Read(b.state)
	if err != nil {
		return keypages.Provider{}, err
	}
	index := slices.IndexFunc(logins, func(login browserlogin.Login) bool {
		return login.ID == card.Login && slices.Contains(login.Projects, card.Project)
	})
	if index < 0 {
		return keypages.Provider{}, errors.New("This project no longer has this login")
	}
	provider, err := b.recipes.Provider(logins[index].Provider)
	if err != nil {
		return keypages.Provider{}, err
	}
	meta, err := b.grantedTask(card.Task, provider.ID)
	if err != nil {
		return provider, err
	}
	if meta.SpawnGen != card.TaskGeneration {
		return provider, errors.New("The task that asked for this sign-in was replaced")
	}
	return provider, b.live(card.Task)
}

// startSignIn opens card's window: a normal Chrome on the login's profile at
// the provider's page, not held to the provider's hosts, since he signs in
// there himself and no task drives it.
func (b *browserBroker) startSignIn(ctx context.Context, card BrowserSignIn) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.closed {
		return errors.New("The supervisor is stopping")
	}
	if b.signing[card.Login] == card.ID {
		return nil
	}
	if b.closing[card.Login] || b.signing[card.Login] != "" {
		return errors.New("The login's last browser is still closing. Try again in a minute")
	}
	if _, isRunning := b.processes[card.Login]; isRunning {
		return errors.New("A task still holds this login's browser")
	}
	provider, err := b.signInGrant(card)
	if err != nil {
		return err
	}
	memory, err := b.memory()
	if err != nil {
		return errors.New("Free memory cannot be read. Try again in a minute")
	}
	if min(memory.Available, memory.CommitAvailable) < memoryFloor {
		return errors.New("The sign-in window needs 4 GB of free memory. Try again in a minute")
	}
	profile, err := browserlogin.MakeProfile(b.state, card.Login)
	if err != nil {
		return err
	}
	executable, err := b.executable()
	if err != nil {
		return err
	}
	process, err := b.start(ctx, browserlogin.Launch{Executable: executable, Profile: profile, Page: provider.KeyPage})
	if process.PID != 0 {
		b.processes[card.Login] = process
	}
	if err != nil {
		b.closeProcess(card.Login)
		return fmt.Errorf("The sign-in window could not open: %w", err)
	}
	if _, err = b.signInGrant(card); err == nil {
		err = ctx.Err()
	}
	if err != nil {
		b.closeProcess(card.Login)
		return err
	}
	b.signing[card.Login] = card.ID
	return nil
}

// endSignIn closes card's window, if it still has one, in the background as
// the broker closes every browser: its profile stays fenced until the close
// is confirmed, and a close that cannot be confirmed is reported once and
// keeps the profile fenced.
func (b *browserBroker) endSignIn(card BrowserSignIn) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.signing[card.Login] != card.ID {
		return
	}
	b.closeProcess(card.Login)
	delete(b.signing, card.Login)
}
