package supervisor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/keypages"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	// browserFetchTimeout bounds one fetch: a Chrome start, a few page
	// loads, and the keys hidden again.
	browserFetchTimeout = 2 * time.Minute
	// signInCloseWait bounds how long a fetch that carries on after a
	// sign-in waits for the sign-in window's Chrome to be gone, since Chrome
	// opens a profile once.
	signInCloseWait = 90 * time.Second
	// fetchSeal is how long a fetch in a goblin's own browser seals it at
	// most: the read is bounded by browserFetchTimeout and hiding the keys
	// after it takes seconds, so it lifts by itself only when a supervisor
	// stopped mid-fetch.
	fetchSeal = browserFetchTimeout + time.Minute
)

// errNeedsSignIn is a fetch whose login has no session for its page: none
// was ever signed in, or the page asked for a sign-in.
var errNeedsSignIn = errors.New("the login needs a sign-in")

// fetchCredential reads the keys a credential request still needs from its
// reviewed key page, in a Chrome with no window that the broker starts on
// the project's login of the page's provider for this fetch alone, and
// stores them through the request card's own save, which refreshes the
// project's goblins and tells the CFO the names. No value leaves the
// supervisor. A page that asks for a sign-in files his sign-in card
// instead, and the fetch carries on once he has signed in.
func (s *Service) fetchCredential(ctx context.Context, request BrowserRequest) (BrowserLease, error) {
	if err := state.ValidTaskID(request.Task); err != nil {
		return BrowserLease{}, err
	}
	credential, found := s.Store.credential(request.Request)
	switch {
	case !found:
		return BrowserLease{}, fmt.Errorf("credential request %q is not on the board", request.Request)
	case credential.State != "open" || !time.Now().Before(credential.ExpiresAt):
		return BrowserLease{}, fmt.Errorf("credential request %s is no longer open", credential.ID)
	case credential.Task != "" && credential.Task != request.Task:
		return BrowserLease{}, fmt.Errorf("credential request %s asks for task %s, not %s", credential.ID, credential.Task, request.Task)
	}
	needed := slices.DeleteFunc(slices.Clone(credential.Names), func(name string) bool {
		return slices.Contains(credential.Saved, name) || slices.Contains(credential.Existing, name)
	})
	provider, keys, err := s.browsers.recipes.Fetch(credential.Link, needed)
	if err != nil {
		return BrowserLease{}, err
	}
	login, values, err := s.browsers.fetch(ctx, request.Task, credential.Project, provider, keys)
	if errors.Is(err, errNeedsSignIn) {
		return s.requestBrowserSignIn(BrowserRequest{Action: "sign-in", Task: request.Task, Provider: provider.ID, Request: credential.ID})
	}
	if err != nil {
		return BrowserLease{}, err
	}
	outcome, err := s.saveCredentials(credentialSave{ID: credential.ID, Generation: credential.Generation, Values: values, isFetched: true})
	if err != nil {
		return BrowserLease{}, err
	}
	return BrowserLease{Login: login.ID, Provider: provider.Name, Account: login.Account, Fetched: outcome.Saved}, nil
}

// continueFetch carries on the fetch that filed card, now that he signed
// in: once the sign-in window's Chrome is gone it fetches again, and tells
// the CFO when that fetch stopped short. A stored key tells the CFO itself.
func (s *Service) continueFetch(card BrowserSignIn) {
	defer s.credentialWork.Done()
	ctx, cancel := context.WithTimeout(context.Background(), signInCloseWait+browserFetchTimeout)
	defer cancel()
	tell := func(detail string) {
		if credential, found := s.Store.credential(card.Fetch); found {
			s.credentialNotice(credential, detail)
		}
	}
	closed, stop := context.WithTimeout(ctx, signInCloseWait)
	defer stop()
	for s.browsers.busy(card.Login) {
		if err := wait(closed, time.Second); err != nil {
			tell("the fetch for " + card.Fetch + " did not carry on after the " + card.Provider + " sign-in, since the sign-in window's browser did not close within " + signInCloseWait.String() + "; the card still takes the keys pasted")
			return
		}
	}
	lease, err := s.fetchCredential(ctx, BrowserRequest{Action: "fetch", Task: card.Task, Request: card.Fetch})
	switch {
	case err != nil:
		tell("the fetch for " + card.Fetch + " after the " + card.Provider + " sign-in stopped: " + bounded(err.Error(), 300) + "; the card still takes the keys pasted")
	case lease.SignIn != "":
		tell("the fetch for " + card.Fetch + " met a " + card.Provider + " page that asks for a sign-in again; his sign-in card is back on the board")
	}
}

// wait sleeps for d, or returns ctx's error once it ends first.
func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// busy reports whether a Chrome still runs or closes on login's profile.
func (b *browserBroker) busy(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, isRunning := b.processes[id]
	return isRunning || b.closing[id]
}

// fetch reads keys from provider's key page for task, a goblin of project,
// on its login's account, in a Chrome with no window that reaches only the
// provider's hosts: the one lent to the task, which the fetch seals while it
// reads, or one the broker starts for the fetch and closes after. No task is
// lent the login while the fetch drives it. A login he never signed in to,
// or a page that asks for a sign-in, is errNeedsSignIn.
func (b *browserBroker) fetch(ctx context.Context, task, project string, provider keypages.Provider, keys []keypages.Key) (browserlogin.Login, map[string]string, error) {
	login, process, isLent, err := b.startFetch(ctx, task, project, provider)
	if err != nil {
		return login, nil, err
	}
	read, cancel := context.WithTimeout(ctx, browserFetchTimeout)
	values, err := b.readKeys(read, process.URL, provider, login.AccountID, keys)
	cancel()
	if endErr := b.endFetch(login.ID, task, isLent); err == nil {
		err = endErr
	}
	if errors.Is(err, browserlogin.ErrSignedOut) {
		if err := b.recordSignedOut(login.ID); err != nil {
			b.report(fmt.Errorf("login %s met a sign-in page but could not be recorded signed out: %w", login.ID, err))
		}
		return login, nil, errNeedsSignIn
	}
	if err != nil {
		return login, nil, fmt.Errorf("the %s key page: %w", provider.Name, err)
	}
	return login, values, nil
}

// startFetch starts the Chrome of task's fetch on project's login of
// provider, once the task carries the provider's declared service and the
// login is signed in on an account and drives no other Chrome. When the
// login's Chrome is lent to the task itself, the fetch reads in that one
// and seals the task's browser commands first, which its third result says.
func (b *browserBroker) startFetch(ctx context.Context, task, project string, provider keypages.Provider) (browserlogin.Login, browserlogin.Process, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	refuse := func(login browserlogin.Login, err error) (browserlogin.Login, browserlogin.Process, bool, error) {
		return login, browserlogin.Process{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return refuse(browserlogin.Login{}, err)
	}
	if b.closed {
		return refuse(browserlogin.Login{}, errors.New("the supervisor is stopping, so no browser starts"))
	}
	meta, manifest, err := b.grantedTask(task, provider.ID)
	if err != nil {
		return refuse(browserlogin.Login{}, err)
	}
	if auth.ProjectName(meta.Project) != project {
		return refuse(browserlogin.Login{}, fmt.Errorf("task %s is a goblin of %s, not of %s", task, auth.ProjectName(meta.Project), project))
	}
	if err := b.live(task); err != nil {
		return refuse(browserlogin.Login{}, err)
	}
	logins, err := browserlogin.Read(b.state)
	if err != nil {
		return refuse(browserlogin.Login{}, err)
	}
	logins, index, err := projectLogin(logins, provider, project, manifest)
	if err != nil {
		return refuse(browserlogin.Login{}, err)
	}
	if logins[index].SignedInAt == nil {
		return refuse(browserlogin.Login{}, errNeedsSignIn)
	}
	login := &logins[index]
	// The fetch reads on the account the project's manifest names, or else
	// on the one his last sign-in showed.
	account := login.AccountID
	declared, isDeclared := manifest.LoginFor(provider.ID)
	if isDeclared && declared.AccountID != "" {
		account = declared.AccountID
	}
	// A login the project names to sign out after each fetch keeps no
	// session once the browser the fetch reads in has closed.
	signsOut := isDeclared && declared.SignsOutAfterFetch
	reading := *login
	reading.AccountID = account
	switch {
	case account == "":
		return refuse(*login, fmt.Errorf("his last sign-in to the %s login showed no account in the page's address, so a fetch cannot check that it reads the login's own account. Paste the keys on the card", provider.Name))
	case b.signing[login.ID] != "":
		return refuse(*login, errors.New("this login is waiting for its sign-in window to close"))
	// A fetch's own browser is recorded lent to its task too, so it is
	// named before a goblin's lent one.
	case b.fetching[login.ID] != "":
		return refuse(*login, fmt.Errorf("a key fetch for %s drives this login's browser now. Ask again in a minute", b.fetching[login.ID]))
	case login.Browser != nil && login.Browser.Holder != task:
		return refuse(*login, fmt.Errorf("the %s login's browser is lent to %s, and a fetch reads keys only in a browser no other goblin drives. Ask again once %s gives it back", provider.Name, login.Browser.Holder, login.Browser.Holder))
	case login.Browser != nil:
		process, isRunning := b.processes[login.ID]
		if !isRunning || !b.alive(process) || b.closing[login.ID] {
			return refuse(*login, fmt.Errorf("the %s login's browser lent to %s has exited. Fetch again in a minute", provider.Name, task))
		}
		// The task's goblin drives this browser too, so its browser
		// commands are refused before anything is read in it.
		if err := browserlogin.Seal(b.state, task, b.now().Add(fetchSeal)); err != nil {
			return refuse(*login, fmt.Errorf("the browser lent to %s could not be sealed, so nothing was read in it: %w", task, err))
		}
		b.fetching[login.ID] = task
		if signsOut {
			b.signOuts[login.ID] = true
		}
		return reading, process, true, nil
	case b.closing[login.ID]:
		return refuse(*login, fmt.Errorf("%s's %s login's last browser is still closing, and Chrome opens a profile once. Ask again in a minute", project, provider.Name))
	}
	memory, err := b.memory()
	if err != nil {
		return refuse(*login, fmt.Errorf("free memory cannot be read, so no browser starts: %w", err))
	}
	if low := min(memory.Available, memory.CommitAvailable); low < memoryFloor {
		return refuse(*login, fmt.Errorf("free memory is %.1f GB, under the %.0f GB a browser waits for. Ask again in a few minutes", gigabytes(low), gigabytes(memoryFloor)))
	}
	profile, err := browserlogin.MakeProfile(b.state, login.ID)
	if err != nil {
		return refuse(*login, err)
	}
	executable, err := b.executable()
	if err != nil {
		return refuse(*login, err)
	}
	process, err := b.start(ctx, browserlogin.Launch{Executable: executable, Profile: profile, Hosts: provider.Hosts, Headless: true})
	if process.PID != 0 {
		b.processes[login.ID] = process
	}
	if err != nil {
		b.closeProcess(login.ID)
		return refuse(*login, fmt.Errorf("the %s login's browser did not start: %w", provider.Name, err))
	}
	current, _, err := b.grantedTask(task, provider.ID)
	if err == nil && (current.SpawnGen != meta.SpawnGen || current.Project != meta.Project) {
		err = errors.New("the task changed while its browser was starting")
	}
	if err == nil {
		err = b.live(task)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		b.closeProcess(login.ID)
		return refuse(*login, err)
	}
	login.Browser = &browserlogin.Browser{PID: process.PID, Started: process.Started, URL: process.URL, Holder: task, Since: b.now(), Hosts: provider.Hosts}
	if err := browserlogin.Write(b.state, logins); err != nil {
		b.closeProcess(login.ID)
		return refuse(*login, err)
	}
	b.fetching[login.ID] = task
	if signsOut {
		b.signOuts[login.ID] = true
	}
	return reading, process, false, nil
}

// endFetch ends task's fetch on login: it lifts the seal of the browser lent
// to the task, which stays lent, or closes the Chrome the fetch started, as
// release closes a lent one.
func (b *browserBroker) endFetch(id, task string, isLent bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.fetching, id)
	if isLent {
		return browserlogin.Unseal(b.state, task)
	}
	logins, err := browserlogin.Read(b.state)
	if err != nil {
		b.closeProcess(id)
		return err
	}
	index := slices.IndexFunc(logins, func(login browserlogin.Login) bool { return login.ID == id })
	if index < 0 || logins[index].Browser == nil {
		b.closeProcess(id)
		return nil
	}
	return b.close(logins, index)
}
