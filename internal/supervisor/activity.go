package supervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const maxBoardActivity = 128

// Small evidence receipts, never tool arguments, prompt text or browser history.
// Presentation status observes work and cannot enqueue a task action.
type BoardActivity struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	TaskID      string    `json:"task_id,omitempty"`
	Generation  string    `json:"generation,omitempty"`
	CFOIdentity string    `json:"cfo_identity,omitempty"`
	Live        bool      `json:"live,omitempty"` // Snapshot evidence, never trusted on admission.
	Source      string    `json:"source,omitempty"`
	Target      string    `json:"target"`
	State       string    `json:"state"`
	URL         string    `json:"url,omitempty"`
	At          time.Time `json:"at"`
	Until       time.Time `json:"until,omitempty"`
	// Watch is the one line a goblin writes when it asks the Overlord to
	// watch a presentation, such as a walkthrough it runs for him; only such
	// a presentation reaches his Command Center.
	Watch string `json:"watch,omitempty"`
	// Digest is, on the receipt of a message typed into a goblin in a turn,
	// the monitor.TextDigest of the text, which the supervisor holds the
	// receipt for until the goblin's record shows it taken.
	Digest string `json:"digest,omitempty"`
}

// PresentationURLProblem names the rule raw breaks, or returns "" for a URL
// the board may link to. Plain http is accepted only where it never crosses
// an untrusted network: this machine's loopback, or the tailnet, whose
// traffic Tailscale encrypts. So a Lavish link is kept exactly as Lavish
// returns it, under its tailnet name, and opens on the Overlord's phone too.
func PresentationURLProblem(raw string) string {
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\x00\\") {
		return "must be one line of at most 2048 characters"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" {
		return "must be an absolute URL with a host"
	}
	if u.User != nil {
		return "must not carry credentials"
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "must not carry a query or fragment"
	}
	host := strings.ToLower(u.Hostname())
	tailnet := strings.HasSuffix(host, ".ts.net")
	if ip := net.ParseIP(host).To4(); ip != nil && !strings.Contains(host, ":") {
		tailnet = ip[0] == 100 && ip[1]&0xc0 == 64
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (host == "127.0.0.1" || host == "localhost" || host == "::1" || tailnet)) {
		return "must be https, or plain http on this machine (127.0.0.1, localhost, ::1) or the tailnet (*.ts.net, 100.64.0.0/10)"
	}
	path := strings.ToLower(u.Path)
	for _, key := range []string{"token", "secret", "credential", "password", "signature", "github_pat_", "ghp_", "api_key", "apikey"} {
		if strings.Contains(path, key) {
			return "must not name a credential (" + key + ") in its path"
		}
	}
	return ""
}

func (s *Store) retainActivity(a BoardActivity) error {
	if len(a.ID) < 8 || len(a.ID) > 128 || strings.ContainsAny(a.ID, "\x00\r\n") || a.At.IsZero() || a.At.After(time.Now().Add(5*time.Minute)) {
		return errors.New("invalid activity identity or timestamp")
	}
	a.Live = false
	node, ok := s.db.Sessions[a.Target]
	// A goblin proves its presentation by its own terminal when it
	// publishes, so the report names no native session.
	paneProven := a.Target == "" && a.TaskID != "" && (a.Kind == "browser" || a.Kind == "review")
	if a.CFOIdentity != "" {
		if a.TaskID != "" || a.Generation != "" || a.Source != "" || (a.Kind != "browser" && a.Kind != "review") || (a.Target != "primary-cfo" && (!ok || node.Role != "cfo")) {
			return errors.New("invalid primary presentation identity")
		}
		file, err := openPrimary(filepath.Join(s.Home.State, "primary.json"))
		if err != nil {
			return errors.New("primary presentation registration unavailable")
		}
		_, identity, err := decodePrimary(file)
		_ = file.Close()
		if err != nil || identity != a.CFOIdentity {
			return errors.New("primary presentation recipient changed")
		}
	} else if !paneProven && (!ok || node.TaskID != a.TaskID || node.Generation != a.Generation) {
		return errors.New("activity recipient changed or is unreported")
	}
	if a.TaskID != "" {
		meta, err := state.ReadTaskMeta(s.Home.State, a.TaskID)
		if err != nil || meta.SpawnGen != a.Generation {
			return errors.New("activity belongs to an obsolete task")
		}
	}
	if a.Source != "" && (node.Parent != a.Source || s.db.Sessions[a.Source].ID == "") {
		return errors.New("activity has no matching reported parent")
	}
	switch a.Kind {
	case "created", "message":
		queued := a.Kind == "message" && a.State == "queued"
		if (a.State != "accepted" && !queued) || a.URL != "" || !a.Until.IsZero() || queued != isTextDigest(a.Digest) {
			return errors.New("invalid native activity receipt")
		}
		if queued {
			s.holdQueuedSend(a)
			return nil
		}
	case "browser", "review":
		if a.State != "active" && a.State != "ended" {
			return errors.New("presentation state must be active or ended")
		}
		if problem := PresentationURLProblem(a.URL); problem != "" {
			return errors.New("presentation URL " + problem)
		}
		if !a.Until.After(a.At) || a.Until.Sub(a.At) > 30*time.Minute {
			return errors.New("presentation expiry must be later than its report and within thirty minutes")
		}
		if len(a.Watch) > 300 || strings.ContainsAny(a.Watch, "\r\n\x00") {
			return errors.New("what the Overlord is asked to watch must be one line of at most 300 characters")
		}
	default:
		return errors.New("unsupported board activity")
	}
	for i, prior := range s.db.Activity {
		if prior.ID != a.ID {
			continue
		}
		replace, err := activityReplacement(prior, a)
		if err != nil || !replace {
			return err
		}
		s.db.Activity[i] = a
		return nil
	}
	if a.At.Before(time.Now().Add(-time.Hour)) {
		return nil
	}
	s.db.Activity = append(s.db.Activity, a)
	if len(s.db.Activity) > maxBoardActivity {
		s.db.Activity = s.db.Activity[len(s.db.Activity)-maxBoardActivity:]
	}
	return nil
}

// isTextDigest reports whether digest is a monitor.TextDigest: 64 lowercase
// hexadecimal digits.
func isTextDigest(digest string) bool {
	_, err := hex.DecodeString(digest)
	return len(digest) == 64 && err == nil && strings.ToLower(digest) == digest
}

// holdQueuedSend keeps the receipt of a message typed into a goblin in a
// turn, once, until its record shows the goblin took it. The caller holds
// the store lock.
func (s *Store) holdQueuedSend(a BoardActivity) {
	if slices.ContainsFunc(s.db.QueuedSends, func(held BoardActivity) bool { return held.ID == a.ID }) {
		return
	}
	s.db.QueuedSends = append(s.db.QueuedSends, a)
	if len(s.db.QueuedSends) > maxBoardActivity {
		s.db.QueuedSends = s.db.QueuedSends[len(s.db.QueuedSends)-maxBoardActivity:]
	}
}

// settleQueuedSends shows on the board the receipt of each message typed
// into a goblin in a turn once look finds its record shows the goblin took
// it, timed when that was seen, and drops one whose goblin restarted or
// ended, or that waited deliveryForget. look reads each goblin away from the
// store's lock.
func (s *Store) settleQueuedSends(now time.Time, look func(BoardActivity) (took, gone bool)) error {
	s.mu.Lock()
	held := slices.Clone(s.db.QueuedSends)
	s.mu.Unlock()
	type outcome struct{ took, gone bool }
	outcomes := map[string]outcome{}
	for _, a := range held {
		took, gone := look(a)
		outcomes[a.ID] = outcome{took, gone}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []BoardActivity
	changed := false
	for _, a := range s.db.QueuedSends {
		seen, looked := outcomes[a.ID]
		switch {
		case !looked:
			kept = append(kept, a)
			continue
		case seen.took:
			a.State, a.Digest, a.At = "accepted", "", now
			// A goblin that lost its native session since shows no receipt.
			_ = s.retainActivity(a)
		case !seen.gone && now.Sub(a.At) < deliveryForget:
			kept = append(kept, a)
			continue
		}
		changed = true
	}
	if !changed {
		return nil
	}
	s.db.QueuedSends = kept
	return s.save()
}

func activityReplacement(prior, next BoardActivity) (bool, error) {
	if prior.Kind != next.Kind || prior.TaskID != next.TaskID || prior.Generation != next.Generation || prior.CFOIdentity != next.CFOIdentity || prior.Source != next.Source || prior.Target != next.Target || prior.URL != next.URL {
		return false, errors.New("activity ID already used")
	}
	if prior.State == "ended" && next.State != "ended" {
		return false, errors.New("finished presentation cannot be reopened")
	}
	return next.At.After(prior.At) && next.Kind != "created" && next.Kind != "message", nil
}

func (s *Store) acceptActivity(a BoardActivity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.retainActivity(a); err != nil {
		return err
	}
	return s.save()
}

func spoolActivity(dir string, a BoardActivity) error {
	// Serve holds this lock while it ingests, every few seconds, so a report
	// waits briefly for it instead of being refused.
	_, err := lock.AcquireExclusiveNamed(dir, ".board-activity.lock")
	for deadline := time.Now().Add(2 * time.Second); err != nil && time.Now().Before(deadline); {
		time.Sleep(25 * time.Millisecond)
		_, err = lock.AcquireExclusiveNamed(dir, ".board-activity.lock")
	}
	if err != nil {
		return err
	}
	defer lock.ReleaseExclusiveNamed(dir, ".board-activity.lock")
	dir = filepath.Join(dir, "board-activity-inbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(a.ID))
	path := filepath.Join(dir, hex.EncodeToString(sum[:])+".json")
	if info, err := os.Stat(path); err == nil {
		if info.Size() > 8192 {
			return errors.New("pending activity exceeds its bound")
		}
		data, err := fsx.ReadFile(path)
		if err != nil {
			return err
		}
		var prior BoardActivity
		if err := json.Unmarshal(data, &prior); err != nil {
			return errors.New("pending activity is invalid")
		}
		replace, err := activityReplacement(prior, a)
		if err != nil || !replace {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) && len(entries) >= maxBoardActivity {
		return errors.New("board activity inbox is full")
	}
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, data)
}

func (s *Store) ingestActivity() error {
	if _, err := lock.AcquireExclusiveNamed(s.Home.State, ".board-activity.lock"); err != nil {
		return err
	}
	defer lock.ReleaseExclusiveNamed(s.Home.State, ".board-activity.lock")
	dir := filepath.Join(s.Home.State, "board-activity-inbox")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries[:min(len(entries), maxBoardActivity)] {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var a BoardActivity
		if info.Size() <= 8192 {
			data, err := fsx.ReadFile(path)
			if err != nil {
				return err
			}
			if json.Unmarshal(data, &a) == nil {
				err = s.acceptActivity(a)
				if errors.Is(err, ErrStorage) {
					return err
				}
			}
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func readBoardState(h home.Home) (*Store, error) {
	path := filepath.Join(h.State, ".supervisor.json")
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxStateBytes {
		return nil, errors.New("supervisor state exceeds its bound")
	}
	data, err := fsx.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var db Database
	if err := json.Unmarshal(data, &db); err != nil {
		return nil, err
	}
	// Read-only construction: Open would perform crash recovery in a live writer.
	return &Store{Home: h, db: db, committed: db}, nil
}

// callerOwns reports whether this process runs under process: one of its
// ancestors, or, when process is the program of native terminal hostID, a
// carrier of that terminal's proof value, which proves a caller whose chain
// of parents stops short, as a Cygwin or MSYS exec leaves it.
func callerOwns(stateDir, hostID string, process lock.Info) bool {
	same := func(entry proc.Entry) bool { return entry.PID == process.PID && entry.Start.Equal(process.Start) }
	if entries, err := proc.Ancestry(os.Getpid(), 32); err == nil && slices.ContainsFunc(entries, same) {
		return true
	}
	if hostID == "" {
		return false
	}
	record, err := host.ReadRecord(stateDir, hostID)
	if err != nil {
		return false
	}
	program, err := terminalProgram(record, os.Environ())
	return err == nil && same(program)
}

// The same explicit native caller convention used by spawn. A same-harness
// session or recent directory match cannot establish a sender relationship.
func callerSession() string {
	id, harness := os.Getenv("CFO_SESSION_ID"), os.Getenv("CFO_SESSION_HARNESS")
	if id == "" {
		id, harness = os.Getenv("CODEX_THREAD_ID"), "codex"
	}
	if id == "" || len(id) > 256 || strings.ContainsAny(id, "\x00\r\n") || (harness != "codex" && harness != "claude" && harness != "pi") {
		return ""
	}
	return harness + "/" + id
}

// PublishPresentation is an explicit report after the browser/presentation
// command succeeds. It neither invokes that tool nor controls its browser.
func PublishPresentation(h home.Home, a BoardActivity) error {
	if a.Kind != "browser" && a.Kind != "review" {
		return errors.New("presentation kind must be browser or review")
	}
	store, err := readBoardState(h)
	if err != nil {
		return err
	}
	if a.TaskID != "" {
		// A goblin proves itself the way it does for a question: this command
		// runs under the task's own terminal. That needs no native hook, so
		// a goblin can present however and whenever it was spawned.
		meta, err := goblinAsker(h.State, a.TaskID)
		if err != nil {
			return err
		}
		if a.Generation != "" && a.Generation != meta.SpawnGen {
			return fmt.Errorf("task %s is now generation %s, not %s", a.TaskID, meta.SpawnGen, a.Generation)
		}
		a.Generation, a.Target = meta.SpawnGen, ""
	} else {
		// The primary CFO proves itself as it does for a question, in its
		// native terminal.
		identity, release, err := (&CFOConnection{State: h.State}).CallerIdentity()
		if err != nil {
			return fmt.Errorf("only the registered primary CFO may report a presentation without a task: %w", err)
		}
		defer release()
		// Native session discovery can arrive later; it must not change a report's recipient.
		a.CFOIdentity, a.Target = identity, "primary-cfo"
	}
	if err := store.retainActivity(a); err != nil {
		return err
	}
	return spoolActivity(h.State, a)
}

// SendReceipt is the board's receipt of one cfo send to a native goblin,
// pinned to the goblin the send was for. One for a goblin the board shows no
// session of does nothing.
type SendReceipt struct {
	h        home.Home
	meta     state.TaskMeta
	activity BoardActivity
}

// Taken spools the receipt of a send the goblin has.
func (r SendReceipt) Taken() error {
	return r.spool(r.activity, time.Now().UTC())
}

// Queued spools the receipt of text submitted at since into a goblin in a
// turn, whose harness hands it over at the goblin's next tool call: the
// supervisor shows it once the goblin's record shows the text taken.
func (r SendReceipt) Queued(text string, since time.Time) error {
	a := r.activity
	a.State, a.Digest = "queued", monitor.TextDigest(text)
	return r.spool(a, since.UTC())
}

func (r SendReceipt) spool(a BoardActivity, at time.Time) error {
	if a.ID == "" {
		return nil
	}
	current, err := state.ReadTaskMeta(r.h.State, r.meta.ID)
	if err != nil || current.SpawnGen != r.meta.SpawnGen || current.Worktree != r.meta.Worktree || current.Backend != r.meta.Backend {
		return errors.New("message accepted but recipient changed before board receipt")
	}
	a.At = at
	return spoolActivity(r.h.State, a)
}

// PrepareSendActivity pins the observed destination before a send. The caller
// spools its receipt only after native acceptance, or as queued once the
// goblin's harness holds the text for its next tool call, never on an
// uncertain submit. Observability failure must not cause a message retry.
func PrepareSendActivity(h home.Home, target string) SendReceipt {
	none := SendReceipt{}
	meta, native := fleet.NativeTask(h.State, target)
	if !native {
		return none
	}
	store, err := readBoardState(h)
	if err != nil {
		return none
	}
	node := store.db.Sessions[store.db.TaskSessions[meta.ID]]
	if node.ID == "" || node.Generation != meta.SpawnGen {
		return none
	}
	source := ""
	parent := store.db.Sessions[node.Parent]
	file, err := openPrimary(filepath.Join(h.State, "primary.json"))
	if err == nil {
		p, _, err := decodePrimary(file)
		_ = file.Close()
		if err == nil && parent.ID == callerSession() && parent.Phase != "ended" && parent.Role == "cfo" && parent.Harness == p.Agent && callerOwns(h.State, p.Host, p.Process) && (&CFOConnection{State: h.State}).verify(p) == nil {
			source = parent.ID
		}
	}
	if parent.ID != "" && parent.ID == callerSession() && parent.Phase != "ended" && parent.Role != "cfo" && parent.TaskID != "" {
		if proven, err := goblinAsker(h.State, parent.TaskID); err == nil && proven.SpawnGen == parent.Generation {
			source = parent.ID
		}
	}
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return none
	}
	a := BoardActivity{ID: "send-" + hex.EncodeToString(bytes[:]), Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Source: source, Target: node.ID, State: "accepted"}
	return SendReceipt{h: h, meta: meta, activity: a}
}

// lookAtQueuedSend reads the goblin a queued send was typed into: gone once
// it restarted or ended, and whether its harness's record of the
// conversation shows the text taken.
func (s *Service) lookAtQueuedSend(a BoardActivity) (took, gone bool) {
	meta, err := state.ReadTaskMeta(s.Store.Home.State, a.TaskID)
	if err != nil || meta.SpawnGen != a.Generation {
		return false, true
	}
	return conversations().Took(context.Background(), goblinConversation(meta), a.Digest, a.At), false
}

// Reuse the supervisor's existing cycle. At most one bounded native identity
// check per minute while a primary presentation is live; no browser polling.
func (s *Service) reconcilePresentations() {
	now := time.Now()
	if s.Options.CFO == nil {
		return
	}
	d := s.Store.Snapshot()
	if !slices.ContainsFunc(d.Activity, func(a BoardActivity) bool { return a.CFOIdentity != "" && a.State == "active" && a.Until.After(now) }) {
		return
	}
	s.mu.Lock()
	if now.Sub(s.presentationChecked) < time.Minute {
		s.mu.Unlock()
		return
	}
	s.presentationChecked = now
	s.presentationIdentity = ""
	s.mu.Unlock()
	file, err := openPrimary(filepath.Join(s.Store.Home.State, "primary.json"))
	if err != nil {
		return
	}
	defer file.Close()
	p, identity, err := decodePrimary(file)
	if err != nil {
		return
	}
	if err := s.Options.CFO.verify(p); err != nil {
		return
	}
	s.mu.Lock()
	s.presentationIdentity = identity
	s.mu.Unlock()
}
