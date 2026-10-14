package supervisor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/keypages"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	// browserLease is how long a task keeps a login's browser before the
	// broker closes it: a fetch takes a few page loads, so a task that
	// forgets to give one back does not hold the login's profile all day.
	browserLease = 30 * time.Minute
	// browserCheck is how often the broker looks for browsers to close.
	browserCheck = 30 * time.Second
)

// BrowserRequest is a goblin's ask for its project's login of a provider:
// lend me its browser, or take it back, or fetch the keys a credential
// request names from its key page.
type BrowserRequest struct {
	Action   string `json:"action"`
	Task     string `json:"task"`
	Provider string `json:"provider"`
	Request  string `json:"request,omitempty"`
}

// BrowserLease is a login's browser lent to a task: the login and the
// DevTools address of its Chrome on this machine. A fetch's answer names
// the keys it stored instead, or the sign-in card it filed.
type BrowserLease struct {
	Login    string   `json:"login"`
	Provider string   `json:"provider"`
	Account  string   `json:"account"`
	URL      string   `json:"url"`
	SignIn   string   `json:"sign_in,omitempty"`
	Fetched  []string `json:"fetched,omitempty"`
}

// browserBroker lends each login's Chrome to one task at a time. It is the
// registry's one writer: it starts a Chrome on the login's profile, headless
// and reaching only its provider's hosts, records it, and closes it when the
// task gives it back, leaves, or keeps it past its lease. A Chrome closes in
// the background, since one took up to half a minute to be gone here, and
// its login is lent again only once it has.
type browserBroker struct {
	mu      sync.Mutex
	state   string
	data    string
	recipes keypages.Recipes
	// memory reads the machine's free memory; a browser starts only above
	// the fleet's floor.
	memory func() (Memory, error)
	// live reports why task cannot hold a browser, nil while it can.
	live       func(task string) error
	executable func() (string, error)
	start      func(context.Context, browserlogin.Launch) (browserlogin.Process, error)
	end        func(context.Context, browserlogin.Process) error
	alive      func(browserlogin.Process) bool
	now        func() time.Time
	// report tells of a Chrome that could not be closed.
	report func(error)
	// processes are the Chromes this broker started, by login, and closing
	// the logins whose Chrome is closing now, which work waits for.
	processes map[string]browserlogin.Process
	closing   map[string]bool
	signing   map[string]string
	// fetching are the logins whose Chrome a fetch drives now, by the task
	// it fetches for, which no task is lent while it does.
	fetching map[string]string
	signedIn func(context.Context, string, keypages.Provider) (bool, string, error)
	readKeys func(context.Context, string, keypages.Provider, string, []keypages.Key) (map[string]string, error)
	work     sync.WaitGroup
	// closed says the supervisor is stopping: its browsers are closed and
	// no other starts.
	closed bool
}

// newBrowserBroker is the broker of the home whose state is stateDir, on
// this machine's Chrome and memory, telling report of a Chrome it could not
// close.
func newBrowserBroker(stateDir, dataDir string, options Options, report func(error)) *browserBroker {
	memory := func() (Memory, error) { return Memory{}, errors.New("this supervisor cannot read memory") }
	if options.Dispatch != nil && options.Dispatch.Memory != nil {
		memory = options.Dispatch.Memory
	}
	return &browserBroker{
		state:      stateDir,
		data:       dataDir,
		recipes:    options.KeyPages,
		memory:     memory,
		live:       func(task string) error { return terminalAnswers(stateDir, task) },
		executable: browserlogin.Executable,
		start:      browserlogin.Start,
		end:        browserlogin.End,
		alive:      browserlogin.Running,
		now:        time.Now,
		report:     report,
		processes:  map[string]browserlogin.Process{},
		closing:    map[string]bool{},
		signing:    map[string]string{},
		fetching:   map[string]string{},
		signedIn:   browserlogin.SignedIn,
		readKeys:   browserlogin.ReadKeys,
	}
}

// RequestBrowser asks this home's supervisor for request, from inside the
// requesting goblin's own terminal, the proof every goblin's question gives.
func RequestBrowser(h home.Home, request BrowserRequest) (BrowserLease, error) {
	if _, err := goblinAsker(h.State, request.Task); err != nil {
		return BrowserLease{}, err
	}
	reply, err := askPipe(h.State, runPipeRequest{Kind: "browser", Browser: &request})
	if err != nil {
		return BrowserLease{}, err
	}
	if request.Action == "attach" && reply.Browser == nil {
		return BrowserLease{}, errors.New("the supervisor lent the browser but did not say where it is")
	}
	if request.Action == "sign-in" && (reply.Browser == nil || reply.Browser.SignIn == "") {
		return BrowserLease{}, errors.New("the supervisor did not return a sign-in card")
	}
	if request.Action == "fetch" && (reply.Browser == nil || reply.Browser.SignIn == "" && len(reply.Browser.Fetched) == 0) {
		return BrowserLease{}, errors.New("the supervisor named neither the keys it fetched nor a sign-in card")
	}
	if reply.Browser == nil {
		return BrowserLease{}, nil
	}
	return *reply.Browser, nil
}

// take answers one request.
func (b *browserBroker) take(ctx context.Context, request BrowserRequest) (BrowserLease, error) {
	if err := state.ValidTaskID(request.Task); err != nil {
		return BrowserLease{}, err
	}
	switch request.Action {
	case "attach":
		return b.attach(ctx, request.Task, request.Provider)
	case "release":
		return BrowserLease{}, b.release(request.Task, request.Provider)
	}
	return BrowserLease{}, fmt.Errorf("a browser request attaches or releases, not %q", request.Action)
}

// attach lends task its project's login of provider, starting the login's
// Chrome when none runs. A login lent to another task is refused until it
// is given back, since Chrome opens a profile once.
func (b *browserBroker) attach(ctx context.Context, task, providerID string) (BrowserLease, error) {
	if err := ctx.Err(); err != nil {
		return BrowserLease{}, err
	}
	provider, err := b.recipes.Provider(providerID)
	if err != nil {
		return BrowserLease{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return BrowserLease{}, err
	}
	if b.closed {
		return BrowserLease{}, errors.New("the supervisor is stopping, so no browser starts")
	}
	meta, err := b.grantedTask(task, provider.ID)
	if err != nil {
		return BrowserLease{}, err
	}
	if meta.Harness != "claude" {
		return BrowserLease{}, errors.New("only a Claude Code task can explore a login. Other harnesses have no PreToolUse hook to seal browser reads")
	}
	if err := b.live(task); err != nil {
		return BrowserLease{}, err
	}
	project := auth.ProjectName(meta.Project)
	logins, err := browserlogin.Read(b.state)
	if err != nil {
		return BrowserLease{}, err
	}
	index, err := projectLogin(logins, provider, project)
	if err != nil {
		return BrowserLease{}, err
	}
	if index < 0 {
		logins = append(logins, browserlogin.Login{ID: browserlogin.DefaultID(provider.ID, project), Provider: provider.ID, Account: project, Projects: []string{project}})
		index = len(logins) - 1
	}
	login := &logins[index]
	if b.signing[login.ID] != "" {
		return BrowserLease{}, errors.New("this login is waiting for its sign-in window to close")
	}
	if b.fetching[login.ID] != "" {
		return BrowserLease{}, fmt.Errorf("a key fetch for %s drives this login's browser now. Ask again in a minute", b.fetching[login.ID])
	}
	// A Chrome that exited on its own is closed as the sweep would, which
	// ends what is left of it before the profile is opened again.
	if process, ok := b.processes[login.ID]; ok && !b.alive(process) {
		if err := b.close(logins, index); err != nil {
			return BrowserLease{}, err
		}
	}
	if b.closing[login.ID] {
		return BrowserLease{}, fmt.Errorf("%s's %s login's last browser is still closing, and Chrome opens a profile once. Ask again in a minute", project, provider.Name)
	}
	lease := BrowserLease{Login: login.ID, Provider: provider.Name, Account: login.Account}
	now := b.now()
	if _, ok := b.processes[login.ID]; ok && login.Browser != nil {
		if login.Browser.Holder != task {
			return BrowserLease{}, fmt.Errorf("%s's %s login is lent to %s since %s, and Chrome opens a profile once. Ask again in a minute", project, provider.Name, login.Browser.Holder, login.Browser.Since.Format(time.Kitchen))
		}
		login.Browser.Since = now
		lease.URL = login.Browser.URL
		return lease, browserlogin.Write(b.state, logins)
	}
	memory, err := b.memory()
	if err != nil {
		return BrowserLease{}, fmt.Errorf("free memory cannot be read, so no browser starts: %w", err)
	}
	if low := min(memory.Available, memory.CommitAvailable); low < memoryFloor {
		return BrowserLease{}, fmt.Errorf("free memory is %.1f GB, under the %.0f GB a browser waits for. Ask again in a few minutes", gigabytes(low), gigabytes(memoryFloor))
	}
	profile, err := browserlogin.MakeProfile(b.state, login.ID)
	if err != nil {
		return BrowserLease{}, err
	}
	executable, err := b.executable()
	if err != nil {
		return BrowserLease{}, err
	}
	if err := ctx.Err(); err != nil {
		return BrowserLease{}, err
	}
	process, err := b.start(ctx, browserlogin.Launch{Executable: executable, Profile: profile, Hosts: provider.Hosts, Headless: true})
	if process.PID != 0 {
		b.processes[login.ID] = process
	}
	if err != nil {
		b.closeProcess(login.ID)
		return BrowserLease{}, fmt.Errorf("the %s login's browser did not start: %w", provider.Name, err)
	}
	if err := ctx.Err(); err != nil {
		b.closeProcess(login.ID)
		return BrowserLease{}, err
	}
	current, err := b.grantedTask(task, provider.ID)
	if err == nil && (current.SpawnGen != meta.SpawnGen || current.Project != meta.Project || current.Harness != meta.Harness) {
		err = errors.New("the task changed while its browser was starting")
	}
	if err == nil {
		err = b.live(task)
	}
	if err != nil {
		b.closeProcess(login.ID)
		return BrowserLease{}, err
	}
	login.Browser = &browserlogin.Browser{PID: process.PID, Started: process.Started, URL: process.URL, Holder: task, Since: now, Hosts: provider.Hosts}
	if err := browserlogin.Write(b.state, logins); err != nil {
		b.closeProcess(login.ID)
		return BrowserLease{}, err
	}
	lease.URL = process.URL
	return lease, nil
}

// A provider is bound to the declared service with the same ID. The live
// task record, including an explicit credentials none, controls its grant.
func (b *browserBroker) grantedTask(task, provider string) (state.TaskMeta, error) {
	meta, err := state.ReadTaskMeta(b.state, task)
	if err != nil {
		return meta, fmt.Errorf("task %s has no live record: %w", task, err)
	}
	manifest, err := auth.LoadManifest(b.data, meta.Project)
	if err != nil {
		return meta, fmt.Errorf("the project's browser service grant cannot be read: %w", err)
	}
	grant := manifest.Grant(auth.Need{Services: meta.Credentials, IsUnstated: !meta.HasCredentials})
	if !slices.Contains(grant.Services, provider) {
		return meta, fmt.Errorf("task %s does not carry the project's declared %s service", task, provider)
	}
	return meta, nil
}

// projectLogin is the index of project's login of provider, or -1 when it
// has none yet. A login whose ID the project's would take but that another
// project holds is refused rather than shared.
func projectLogin(logins []browserlogin.Login, provider keypages.Provider, project string) (int, error) {
	for index, login := range logins {
		if login.Provider == provider.ID && slices.Contains(login.Projects, project) {
			return index, nil
		}
	}
	id := browserlogin.DefaultID(provider.ID, project)
	if slices.ContainsFunc(logins, func(login browserlogin.Login) bool { return login.ID == id }) {
		return -1, fmt.Errorf("the login %s belongs to another project, so %s gets no %s login of its own", id, project, provider.Name)
	}
	return -1, nil
}

// release closes the browser task holds of provider. A task that holds none
// is answered as given back.
func (b *browserBroker) release(task, providerID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	logins, err := browserlogin.Read(b.state)
	if err != nil {
		return err
	}
	for index, login := range logins {
		if login.Provider == providerID && login.Browser != nil && login.Browser.Holder == task {
			return b.close(logins, index)
		}
	}
	return nil
}

// close records the browser of logins[index] given back and closes its
// Chrome in the background. The caller holds b.mu.
func (b *browserBroker) close(logins []browserlogin.Login, index int) error {
	login := &logins[index]
	b.closeProcess(login.ID)
	used := b.now()
	login.Browser, login.UsedAt = nil, &used
	return browserlogin.Write(b.state, logins)
}

func (b *browserBroker) closeProcess(id string) {
	process, ok := b.processes[id]
	if !ok || b.closing[id] {
		return
	}
	b.closing[id] = true
	b.work.Add(1)
	go func() {
		defer b.work.Done()
		err := b.end(context.Background(), process)
		if err != nil {
			b.report(fmt.Errorf("the browser of login %s: %w", id, err))
			return
		}
		b.mu.Lock()
		delete(b.processes, id)
		delete(b.closing, id)
		b.mu.Unlock()
	}()
}

// sweep closes each browser that exited, or whose task has left or kept it
// past its lease, and forgets a record of a browser this broker never
// started, which a supervisor that stopped left behind: its Chrome ended
// with it, since a Chrome runs in a job its supervisor holds.
func (b *browserBroker) sweep() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	logins, err := browserlogin.Read(b.state)
	if err != nil {
		return err
	}
	var failed []error
	changed := false
	for index, login := range logins {
		if login.Browser == nil {
			continue
		}
		process, ok := b.processes[login.ID]
		_, grantErr := b.grantedTask(login.Browser.Holder, login.Provider)
		switch {
		case !ok:
			logins[index].Browser = nil
			changed = true
		case !b.alive(process) || b.now().Sub(login.Browser.Since) > browserLease || b.live(login.Browser.Holder) != nil || grantErr != nil:
			if err := b.close(logins, index); err != nil {
				failed = append(failed, err)
			}
		}
	}
	if changed {
		failed = append(failed, browserlogin.Write(b.state, logins))
	}
	return errors.Join(failed...)
}

// closeAll closes every browser the broker runs, as the supervisor stops,
// and reports any cleanup that could not be confirmed.
func (b *browserBroker) closeAll() error {
	b.mu.Lock()
	b.closed = true
	logins, err := browserlogin.Read(b.state)
	failed := []error{err}
	for id := range b.processes {
		b.closeProcess(id)
	}
	for index, login := range logins {
		if login.Browser != nil {
			failed = append(failed, b.close(logins, index))
		}
	}
	b.mu.Unlock()
	b.work.Wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	for id := range b.processes {
		failed = append(failed, fmt.Errorf("the browser of login %s could not be confirmed closed", id))
	}
	return errors.Join(failed...)
}

// keepBrowsers sweeps the broker's browsers until ctx ends, then closes
// them.
func (s *Service) keepBrowsers(ctx context.Context) {
	// A supervisor that starts runs no fetch, so a seal one that stopped
	// mid-fetch left behind lifts now.
	if err := browserlogin.ClearSeals(s.browsers.state); err != nil {
		s.publish(fmt.Errorf("browser seals: %w", err))
	}
	if err := s.browsers.sweep(); err != nil {
		s.publish(fmt.Errorf("browser logins: %w", err))
	}
	ticker := time.NewTicker(browserCheck)
	defer ticker.Stop()
	signinTicker := time.NewTicker(time.Second)
	defer signinTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := s.browsers.closeAll(); err != nil {
				s.publish(fmt.Errorf("browser logins: %w", err))
			}
			return
		case <-ticker.C:
			if err := s.browsers.sweep(); err != nil {
				s.publish(fmt.Errorf("browser logins: %w", err))
			}
		case <-signinTicker.C:
			if err := s.processBrowserSignIns(ctx); err != nil {
				s.publish(fmt.Errorf("browser sign-in: %w", err))
			}
		}
	}
}
