package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	// taskBrowserIdle is how long a goblin's browser stays open once its
	// session has let go of it and nobody watches or drives it, which covers
	// a session's stop and start again.
	taskBrowserIdle = 2 * time.Minute
	// taskBrowserUnused is how long it stays open while its session still
	// holds it but sends it no command, and nobody watches or drives it: a
	// goblin's chrome-devtools-axi bridge holds its DevTools connection for
	// as long as it runs, so a held connection alone is no use (#2537).
	taskBrowserUnused = 10 * time.Minute
	// taskBrowserInUse is how long after its goblin's last command a browser
	// reads as in use, which its card's Browser active shows (#2537).
	taskBrowserInUse = time.Minute
	// taskBrowserStart bounds a goblin's browser's start, as a login's is.
	taskBrowserStart = 90 * time.Second
	// unwatchedControl is how long a person keeps control of a browser that
	// no view shows, after which it goes back to its goblin, so a tab closed
	// mid-step never holds the goblin's browser for good.
	unwatchedControl = 30 * time.Minute
	// taskBrowserWidth and taskBrowserHeight size a goblin's browser's
	// window, a desktop page the Screen view fits to its width.
	taskBrowserWidth, taskBrowserHeight = 1280, 800
)

// drivingRefusal is what a goblin's browser command is answered with while a
// person drives its browser on the board.
const drivingRefusal = "A person is driving this browser from the Code Goblins board. Wait until they hand it back, then try again."

// setupMethods are the DevTools commands a goblin's browser session sends to
// set up a page it attaches to, which run while a person drives, since the
// page they open, such as a sign-in's pop-up, must be one the goblin can
// drive once they hand it back. Every other command is refused then.
var setupMethods = []string{
	"Browser.getVersion",
	"Page.addScriptToEvaluateOnNewDocument",
	"Page.createIsolatedWorld",
	"Page.getFrameTree",
	"Page.setLifecycleEventsEnabled",
	"Runtime.addBinding",
	"Runtime.runIfWaitingForDebugger",
	"Target.attachToTarget",
	"Target.detachFromTarget",
	"Target.getBrowserContexts",
	"Target.getTargetInfo",
	"Target.getTargets",
	"Target.setAutoAttach",
	"Target.setDiscoverTargets",
}

// refusedWhileDriven is the refusal a goblin's command method meets while
// isDriven, and empty when it runs.
func refusedWhileDriven(isDriven bool, method string) string {
	if !isDriven || isSetup(method) {
		return ""
	}
	return drivingRefusal
}

// isSetup says method only sets up a page the goblin's session attached to,
// as its tool does on its own, which is no use of the browser.
func isSetup(method string) bool {
	return strings.HasSuffix(method, ".enable") || strings.HasSuffix(method, ".disable") || slices.Contains(setupMethods, method)
}

// screenSource is a browser a goblin's session drives and the board shows.
type screenSource interface {
	// ServeClient relays a DevTools WebSocket client to the browser,
	// hearing each command first and answering each one hear gives a
	// reason for with that reason.
	ServeClient(w http.ResponseWriter, r *http.Request, hear func(browserlogin.Command) string)
	// Open attaches a DevTools client of the supervisor's own.
	Open(ctx context.Context) (screenClient, error)
	// IsRunning says the browser has not exited.
	IsRunning() bool
}

// screenClient is the supervisor's own DevTools connection to a browser.
type screenClient interface {
	Call(ctx context.Context, session, method string, params, result any) error
	Events() <-chan browserlogin.Event
	Close()
}

// chromeScreen is a Chrome the supervisor started.
type chromeScreen struct{ process browserlogin.Process }

func (c chromeScreen) ServeClient(w http.ResponseWriter, r *http.Request, hear func(browserlogin.Command) string) {
	c.process.ServeClient(w, r, hear)
}

func (c chromeScreen) Open(ctx context.Context) (screenClient, error) {
	return c.process.Open(ctx)
}

func (c chromeScreen) IsRunning() bool {
	return browserlogin.Running(c.process)
}

// taskBrowser is one goblin's browser, for one generation of its task.
type taskBrowser struct {
	task, generation string
	// ready closes once the browser started, or failed to, which err says.
	ready  chan struct{}
	source screenSource
	end    func() error
	err    error
	// clients counts the goblin's DevTools connections, viewers each Screen
	// view's wake, and isDriven says a person drives it.
	clients  int
	viewers  map[chan struct{}]struct{}
	isDriven bool
	// startedAt is when it started, and usedAt when its goblin last sent it
	// a command that does more than set up a page; lapse tells the board once
	// that is taskBrowserInUse ago, when its card stops reading Browser active.
	startedAt, usedAt time.Time
	lapse             *time.Timer
	// pointer is where the goblin last pressed its mouse, for its views.
	pointer screenPointer
	// idle closes the browser once nobody uses it, and unwatched hands it
	// back once a person drove it with no view open for unwatchedControl.
	idle, unwatched *time.Timer
	isClosed        bool
}

// screenPointer is where a goblin pressed its mouse on a page: the page and
// the place, nothing else, with seq counting the presses.
type screenPointer struct {
	target string
	x, y   float64
	seq    int
}

// taskBrowsers runs each goblin's browser: a Chrome the supervisor starts,
// headless on its task's own profile with a private DevTools pipe, which the
// goblin's chrome-devtools-axi session attaches to through the board
// (/api/browser/devtools) and the board's Screen view shows. Holding it
// here, not in the goblin's own tool, is what lets a person watch it and
// drive it, and the goblin's commands be refused while they do.
type taskBrowsers struct {
	mu      sync.Mutex
	running map[string]*taskBrowser
	launch  func(context.Context, state.TaskMeta) (screenSource, func() error, error)
	// live says why task's goblin no longer runs, nil while it does.
	live   func(task string) error
	notify func()
	// ended hears that task's browser for generation closed.
	ended  func(task, generation string)
	report func(error)
	idle   time.Duration
	// unused is how long a browser its goblin holds but sends no command
	// stays open, and inUse how long after a command it reads as in use.
	unused, inUse time.Duration
	// unwatched is how long a person keeps control nobody watches.
	unwatched time.Duration
	work      sync.WaitGroup
	closed    bool
}

func newTaskBrowsers(stateDir string, notify func(), ended func(task, generation string), report func(error)) *taskBrowsers {
	return &taskBrowsers{
		running:   map[string]*taskBrowser{},
		launch:    launchTaskBrowser,
		live:      func(task string) error { return terminalAnswers(stateDir, task) },
		notify:    notify,
		ended:     ended,
		report:    report,
		idle:      taskBrowserIdle,
		unused:    taskBrowserUnused,
		inUse:     taskBrowserInUse,
		unwatched: unwatchedControl,
	}
}

// launchTaskBrowser starts meta's goblin's Chrome on its task's profile, the
// one a sign-in it was shown keeps.
func launchTaskBrowser(ctx context.Context, meta state.TaskMeta) (screenSource, func() error, error) {
	if meta.TaskTmp == "" {
		return nil, nil, fmt.Errorf("task %s has no scratch folder for its browser's profile", meta.ID)
	}
	executable, err := browserlogin.Executable()
	if err != nil {
		return nil, nil, err
	}
	profile := filepath.Join(meta.TaskTmp, "browser-profile")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return nil, nil, err
	}
	process, err := browserlogin.Start(ctx, browserlogin.Launch{Executable: executable, Profile: profile, Headless: true, Width: taskBrowserWidth, Height: taskBrowserHeight})
	if err != nil {
		if process.PID != 0 {
			err = fmt.Errorf("%w; its Chrome %d could not be confirmed closed", err, process.PID)
		}
		return nil, nil, err
	}
	return chromeScreen{process}, func() error { return browserlogin.End(context.Background(), process) }, nil
}

// ensure is meta's goblin's browser, started when none runs for its
// generation. A browser of an earlier generation closes first. A supervisor
// that was never started runs none.
func (t *taskBrowsers) ensure(ctx context.Context, meta state.TaskMeta) (*taskBrowser, error) {
	if t == nil {
		return nil, errors.New("this supervisor runs no goblin browsers")
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, errors.New("the supervisor is stopping")
	}
	b := t.running[meta.ID]
	if b != nil && b.generation != meta.SpawnGen {
		t.closeLocked(b)
		b = nil
	}
	if b == nil {
		b = &taskBrowser{task: meta.ID, generation: meta.SpawnGen, ready: make(chan struct{}), viewers: map[chan struct{}]struct{}{}}
		t.running[meta.ID] = b
		t.work.Add(1)
		go t.start(b, meta)
	}
	t.mu.Unlock()
	select {
	case <-b.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if b.err != nil {
		return nil, b.err
	}
	return b, nil
}

func (t *taskBrowsers) start(b *taskBrowser, meta state.TaskMeta) {
	defer t.work.Done()
	ctx, cancel := context.WithTimeout(context.Background(), taskBrowserStart)
	source, end, err := t.launch(ctx, meta)
	cancel()
	t.mu.Lock()
	b.source, b.end, b.err, b.startedAt = source, end, err, time.Now()
	if err != nil {
		if t.running[b.task] == b {
			delete(t.running, b.task)
		}
	} else if b.isClosed || t.closed {
		// It was closed or replaced while it started.
		t.endLocked(b)
	} else {
		t.settleLocked(b)
	}
	close(b.ready)
	t.mu.Unlock()
	if err == nil {
		t.notify()
	}
}

// shown is task's browser for generation, while it runs.
func (t *taskBrowsers) shown(task, generation string) *taskBrowser {
	t.mu.Lock()
	defer t.mu.Unlock()
	b := t.running[task]
	if b == nil || b.generation != generation || b.source == nil || b.isClosed {
		return nil
	}
	return b
}

// isRunning says task's browser runs for generation, which its card shows.
// A supervisor that was never started runs none.
func (t *taskBrowsers) isRunning(task, generation string) bool {
	return t != nil && t.shown(task, generation) != nil
}

// isInUse says task's browser runs for generation and its goblin sent it a
// command within t.inUse, which its card shows as Browser active.
func (t *taskBrowsers) isInUse(task, generation string) bool {
	if t == nil {
		return false
	}
	b := t.shown(task, generation)
	if b == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return time.Since(b.usedAt) < t.inUse
}

// use hears a command of b's goblin. One that does more than set up a page
// is use: the browser reads as in use for t.inUse, which the board hears of
// as it starts and as it lapses, and its unused close counts from it. A
// press of its mouse is where its views show its pointer.
func (t *taskBrowsers) use(b *taskBrowser, command browserlogin.Command) {
	if isSetup(command.Method) {
		return
	}
	var press struct {
		Type string  `json:"type"`
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
	}
	isPress := command.Method == "Input.dispatchMouseEvent" && command.Target != "" && json.Unmarshal(command.Params, &press) == nil && press.Type == "mousePressed"
	t.mu.Lock()
	if b.isClosed {
		t.mu.Unlock()
		return
	}
	wasInUse := time.Since(b.usedAt) < t.inUse
	b.usedAt = time.Now()
	if b.lapse != nil {
		b.lapse.Stop()
	}
	b.lapse = time.AfterFunc(t.inUse, t.notify)
	if isPress {
		b.pointer = screenPointer{target: command.Target, x: press.X, y: press.Y, seq: b.pointer.seq + 1}
		for wake := range b.viewers {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}
	t.settleLocked(b)
	t.mu.Unlock()
	if !wasInUse {
		t.notify()
	}
}

// pointerOf is where b's goblin last pressed its mouse.
func (t *taskBrowsers) pointerOf(b *taskBrowser) screenPointer {
	t.mu.Lock()
	defer t.mu.Unlock()
	return b.pointer
}

// isDriven says a person drives b now.
func (t *taskBrowsers) isDriven(b *taskBrowser) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return b.isDriven
}

// connect counts a DevTools connection of b's goblin until its returned
// release.
func (t *taskBrowsers) connect(b *taskBrowser) func() {
	t.mu.Lock()
	b.clients++
	t.settleLocked(b)
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		b.clients--
		t.settleLocked(b)
		t.mu.Unlock()
	}
}

// watch counts a Screen view of b until its returned release; wake hears of
// each change of who drives it, and closes when the browser does.
func (t *taskBrowsers) watch(b *taskBrowser) (wake chan struct{}, release func()) {
	wake = make(chan struct{}, 1)
	t.mu.Lock()
	if b.isClosed {
		close(wake)
	} else {
		b.viewers[wake] = struct{}{}
	}
	t.settleLocked(b)
	t.mu.Unlock()
	return wake, func() {
		t.mu.Lock()
		if _, isWatching := b.viewers[wake]; isWatching {
			delete(b.viewers, wake)
		}
		t.settleLocked(b)
		t.mu.Unlock()
	}
}

// drive gives control of b to a person, or with isDriven false back to its
// goblin, and says whether that changed anything.
func (t *taskBrowsers) drive(b *taskBrowser, isDriven bool) bool {
	t.mu.Lock()
	changed := !b.isClosed && b.isDriven != isDriven
	if changed {
		b.isDriven = isDriven
		for wake := range b.viewers {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
		t.settleLocked(b)
	}
	t.mu.Unlock()
	if changed {
		t.notify()
	}
	return changed
}

// handBack gives task's browser back to its goblin, whatever its generation.
// A supervisor that was never started runs none.
func (t *taskBrowsers) handBack(task string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	b := t.running[task]
	t.mu.Unlock()
	if b != nil {
		t.drive(b, false)
	}
}

// settleLocked arms b's timers for what uses it now: a browser nobody holds,
// watches or drives closes after t.idle, one its goblin holds but sends no
// command t.unused after its last, and control nobody watches goes back
// after t.unwatched.
func (t *taskBrowsers) settleLocked(b *taskBrowser) {
	if b.idle != nil {
		b.idle.Stop()
		b.idle = nil
	}
	if b.unwatched != nil {
		b.unwatched.Stop()
		b.unwatched = nil
	}
	if b.isClosed || b.source == nil {
		return
	}
	if b.isDriven && len(b.viewers) == 0 {
		b.unwatched = time.AfterFunc(t.unwatched, func() { t.drive(b, false) })
	}
	if len(b.viewers) == 0 && !b.isDriven {
		wait, usedAt := t.idle, b.usedAt
		if b.clients > 0 {
			wait = t.unused - time.Since(b.startedAt)
			if usedAt.After(b.startedAt) {
				wait = t.unused - time.Since(usedAt)
			}
		}
		b.idle = time.AfterFunc(wait, func() {
			t.mu.Lock()
			if len(b.viewers) == 0 && !b.isDriven && (b.clients == 0 || b.usedAt.Equal(usedAt)) {
				t.closeLocked(b)
			}
			t.mu.Unlock()
		})
	}
}

// closeLocked closes b: its views end now, and its Chrome and what heard it
// ended in the background.
func (t *taskBrowsers) closeLocked(b *taskBrowser) {
	if b.isClosed {
		return
	}
	b.isClosed = true
	if t.running[b.task] == b {
		delete(t.running, b.task)
	}
	t.settleLocked(b)
	for wake := range b.viewers {
		close(wake)
		delete(b.viewers, wake)
	}
	if b.source != nil {
		t.endLocked(b)
	}
	go t.notify()
	t.work.Add(1)
	go func() {
		defer t.work.Done()
		t.ended(b.task, b.generation)
	}()
}

// endLocked closes b's Chrome in the background.
func (t *taskBrowsers) endLocked(b *taskBrowser) {
	end := b.end
	t.work.Add(1)
	go func() {
		defer t.work.Done()
		if err := end(); err != nil {
			t.report(fmt.Errorf("the browser of %s: %w", b.task, err))
		}
	}()
}

// sweep closes each browser whose goblin no longer runs, or runs a later
// generation, and each that exited, which its goblin's next use starts again.
// A supervisor that was never started runs none.
func (t *taskBrowsers) sweep(stateDir string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	browsers := make([]*taskBrowser, 0, len(t.running))
	for _, b := range t.running {
		browsers = append(browsers, b)
	}
	t.mu.Unlock()
	for _, b := range browsers {
		t.mu.Lock()
		source := b.source
		t.mu.Unlock()
		if source != nil && !source.IsRunning() {
			t.mu.Lock()
			t.closeLocked(b)
			t.mu.Unlock()
			continue
		}
		meta, err := state.ReadTaskMeta(stateDir, b.task)
		if err == nil && meta.SpawnGen == b.generation {
			err = t.live(b.task)
		} else if err == nil {
			err = errors.New("its task was started again")
		}
		if err == nil {
			continue
		}
		t.mu.Lock()
		if b.source != nil {
			t.closeLocked(b)
		}
		t.mu.Unlock()
	}
}

// closeAll closes every goblin's browser, as the supervisor stops. A
// supervisor that was never started runs none.
func (t *taskBrowsers) closeAll() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.closed = true
	for _, b := range t.running {
		if b.source != nil {
			t.closeLocked(b)
		} else {
			b.isClosed = true
		}
	}
	t.mu.Unlock()
	t.work.Wait()
}

// taskDevTools relays a goblin's chrome-devtools-axi session to its browser,
// which it starts when none runs. Spawn points the session here
// (spawn.BrowserURLVariable). Only a process of this PC's own user reaches
// it, as only one reaches a login's bridge, and never a web page: a page's
// WebSocket always names its origin, which DevTools clients never do.
func (h *HTTP) taskDevTools(w http.ResponseWriter, r *http.Request) {
	if offMachine(r, h.Host) != "" || r.Header.Get("Origin") != "" {
		apiError(w, http.StatusForbidden, "A goblin's browser is reached only from its own session on this PC.")
		return
	}
	h.devToolsGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meta, err := h.Service.screenTask(r.URL.Query().Get("task"), r.URL.Query().Get("generation"))
		if err != nil {
			apiError(w, http.StatusConflict, err.Error())
			return
		}
		b, err := h.Service.screens.ensure(r.Context(), meta)
		if err != nil {
			apiError(w, http.StatusServiceUnavailable, "The goblin's browser could not start: "+err.Error())
			return
		}
		release := h.Service.screens.connect(b)
		defer release()
		b.source.ServeClient(w, r, func(command browserlogin.Command) string {
			if reason := refusedWhileDriven(h.Service.screens.isDriven(b), command.Method); reason != "" {
				return reason
			}
			h.Service.screens.use(b, command)
			return ""
		})
	})).ServeHTTP(w, r)
}

// screenTask is the task a goblin's browser or its Screen view names, while
// that generation is current.
func (s *Service) screenTask(task, generation string) (state.TaskMeta, error) {
	if state.ValidTaskID(task) != nil || generation == "" {
		return state.TaskMeta{}, errors.New("Name a task's current session to reach its browser.")
	}
	meta, err := state.ReadTaskMeta(s.Store.Home.State, task)
	if err != nil || meta.SpawnGen != generation {
		return state.TaskMeta{}, terminalEnded("This task restarted or was replaced. Open its current session.")
	}
	return meta, nil
}
