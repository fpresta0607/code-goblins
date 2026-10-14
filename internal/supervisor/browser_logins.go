package supervisor

import (
	"errors"
	"io/fs"
	"net/http"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
)

// browserLoginEntry is one login a goblin's Connections lists: a login its
// project may use, whether he is signed in to it, when he last signed in
// and when a browser on it last closed. It holds no browser address and
// nothing of the session.
type browserLoginEntry struct {
	ID                 string     `json:"id"`
	Provider           string     `json:"provider"`
	Account            string     `json:"account"`
	Projects           []string   `json:"projects"`
	IsSignedIn         bool       `json:"signed_in"`
	SignedInAt         *time.Time `json:"signed_in_at,omitempty"`
	UsedAt             *time.Time `json:"used_at,omitempty"`
	SignsOutAfterFetch bool       `json:"signs_out_after_fetch"`
	IsInUse            bool       `json:"in_use"`
}

// projectLogins are the logins project's goblins may use, as its goblins'
// Connections list them.
func (b *browserBroker) projectLogins(project string) ([]browserLoginEntry, error) {
	manifest, err := auth.LoadManifest(b.data, project)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	logins, err := browserlogin.Read(b.state)
	if err != nil {
		return nil, err
	}
	entries := []browserLoginEntry{}
	for _, login := range logins {
		if !slices.Contains(login.Projects, project) {
			continue
		}
		provider := login.Provider
		if recipe, err := b.recipes.Provider(login.Provider); err == nil {
			provider = recipe.Name
		}
		declared, _ := manifest.LoginFor(login.Provider)
		_, isRunning := b.processes[login.ID]
		entries = append(entries, browserLoginEntry{
			ID: login.ID, Provider: provider, Account: login.Account, Projects: slices.Clone(login.Projects),
			IsSignedIn: login.SignedInAt != nil, SignedInAt: login.SignedInAt, UsedAt: login.UsedAt,
			SignsOutAfterFetch: declared.SignsOutAfterFetch && browserlogin.DefaultID(declared.Provider, declared.Account) == login.ID,
			IsInUse:            isRunning || b.closing[login.ID] || b.signing[login.ID] != "" || b.fetching[login.ID] != "",
		})
	}
	return entries, nil
}

// endSession deletes login id's profile, which ends the session kept there,
// and records it signed out, so the next fetch brings his sign-in card. With
// isForget the login goes too. Only a login of project is touched, and only
// while no browser of it runs, closes or signs in.
func (b *browserBroker) endSession(id, project string, isForget bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, isRunning := b.processes[id]; isRunning || b.closing[id] || b.signing[id] != "" || b.fetching[id] != "" {
		return errors.New("This login's browser is in use. Try again once it closes.")
	}
	logins, err := browserlogin.Read(b.state)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(logins, func(login browserlogin.Login) bool { return login.ID == id && slices.Contains(login.Projects, project) })
	if index < 0 {
		return errors.New("This goblin's project has no such login. Refresh its connections.")
	}
	if err := browserlogin.RemoveProfile(b.state, id); err != nil {
		return err
	}
	if isForget {
		logins = slices.Delete(logins, index, index+1)
	} else {
		logins[index].SignedInAt = nil
	}
	return browserlogin.Write(b.state, logins)
}

// recordSignedOut records that login id's session ended, as a page that
// asked for a sign-in showed.
func (b *browserBroker) recordSignedOut(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	logins, err := browserlogin.Read(b.state)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(logins, func(login browserlogin.Login) bool { return login.ID == id })
	if index < 0 || logins[index].SignedInAt == nil {
		return nil
	}
	logins[index].SignedInAt = nil
	return browserlogin.Write(b.state, logins)
}

// endBrowserLogin signs a login out, or forgets it, from a goblin's
// Connections, on the board on this PC alone.
func (h *HTTP) endBrowserLogin(w http.ResponseWriter, r *http.Request, isForget bool) {
	if offMachine(r, h.Host) != "" {
		apiError(w, http.StatusForbidden, "Sign out of or forget a login from the board on this PC")
		return
	}
	var input struct {
		Task       string `json:"task"`
		Generation string `json:"generation"`
		Login      string `json:"login"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, "Choose a login from this goblin's connections.")
		return
	}
	meta, err := h.Service.connectionTask(input.Task, input.Generation)
	if err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	if h.Service.browsers == nil {
		apiError(w, http.StatusServiceUnavailable, "This board keeps no browser logins.")
		return
	}
	if err := h.Service.browsers.endSession(input.Login, auth.ProjectName(meta.Project), isForget); err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	logins, err := h.Service.browsers.projectLogins(auth.ProjectName(meta.Project))
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond(w, http.StatusOK, struct {
		BrowserLogins []browserLoginEntry `json:"browser_logins"`
	}{logins})
}
