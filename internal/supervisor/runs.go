package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// maxRuns bounds the run list. An item still waiting or running is never
// dropped to make room: a new one waits in the inbox.
const maxRuns = 64

// runLifetime is how long an item waits for the Overlord to run it.
const runLifetime = 24 * time.Hour

// finishedRunRetention is how long a finished or expired item stays listed.
const finishedRunRetention = 7 * 24 * time.Hour

// maxRunCommand bounds the command an item carries, and maxRunOutput the
// output it keeps: the end, which says how the run went.
const (
	maxRunCommand = 64 << 10
	maxRunOutput  = 64 << 10
)

// runShells are the shells an item can name: Windows PowerShell 5.1,
// PowerShell 7 and Git Bash. Run launches exactly the one named.
var runShells = []string{"powershell", "pwsh", "bash"}

// Run is a command the CFO needs the Overlord to run, which he runs with one
// click from the Command Center. Only the registered primary CFO creates one.
// Its command is the exact text of a script file under state/runs, and Run
// executes that file, never anything the browser sends.
type Run struct {
	ID       string `json:"id"`
	Identity string `json:"identity"`
	Title    string `json:"title"`
	Shell    string `json:"shell"`
	Admin    bool   `json:"admin"`
	Command  string `json:"command"`
	Cwd      string `json:"cwd"`
	// ScriptSum is the SHA-256 of the script file Run executes: Run refuses
	// a file that changed, and the audit line records it.
	ScriptSum string `json:"script_sum,omitempty"`
	State     string `json:"state"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	Output    string `json:"output,omitempty"`
	Reason    string `json:"reason,omitempty"`
	// RunAction is the board action that ran the item; PID and Started name
	// the process it runs under, so a window closed early ends the item.
	RunAction  string     `json:"run_action,omitempty"`
	PID        int        `json:"pid,omitempty"`
	Started    *time.Time `json:"started,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RanAt      *time.Time `json:"ran_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// RunRequest is what cfo run-request asks for; CommandFile is read once.
type RunRequest struct {
	ID, Title, Shell, Cwd, CommandFile string
	Admin                              bool
}

// RunLauncher opens a run item's window. Launch returns once the process the
// item runs under has started; the service finishes the item from what the
// run leaves in its directory.
type RunLauncher interface {
	Launch(ctx context.Context, l RunLaunch) (RunStarted, error)
}

// RunLaunch is one item to open: its shell, whether it runs elevated, the
// script to execute, the directory the run writes output.log and exit.txt
// to, and the folder it runs in.
type RunLaunch struct {
	Shell  string
	Admin  bool
	Script string
	Dir    string
	Cwd    string
}

// RunStarted is the process a launched item runs under.
type RunStarted struct {
	PID   int
	Start time.Time
}

func validRun(r Run) error {
	switch {
	case !reviewID.MatchString(r.ID):
		return errors.New("a run item needs an ID of 8 to 128 letters, digits, dots, dashes or underscores")
	case len(r.Identity) != 64:
		return errors.New("a run item needs the CFO's identity")
	case strings.TrimSpace(r.Title) == "" || len(r.Title) > 2000:
		return errors.New("a run item needs a title of at most 2000 characters saying why it should run")
	case !slices.Contains(runShells, r.Shell):
		return errors.New("a run item's shell is powershell, pwsh or bash")
	case strings.TrimSpace(r.Command) == "" || len(r.Command) > maxRunCommand || !utf8.ValidString(r.Command) || strings.ContainsRune(r.Command, 0):
		return fmt.Errorf("a run item's command is UTF-8 text of at most %d KiB", maxRunCommand>>10)
	case !filepath.IsAbs(r.Cwd):
		return errors.New("a run item's folder is an absolute path")
	case r.CreatedAt.IsZero() || r.CreatedAt.After(time.Now().Add(5*time.Minute)):
		return errors.New("a run item needs its creation time")
	}
	return nil
}

func sameRun(a, b Run) bool {
	return a.Identity == b.Identity && a.Title == b.Title && a.Shell == b.Shell && a.Admin == b.Admin && a.Command == b.Command && a.Cwd == b.Cwd
}

// runDir holds one item's script and what its run leaves behind. Its name is
// the item's own digest, so a later item with the same ID never shares it.
func runDir(stateDir string, r Run) string {
	sum := sha256.Sum256([]byte(r.ID + "\n" + r.Identity + "\n" + strconv.FormatInt(r.CreatedAt.UnixNano(), 10)))
	return filepath.Join(stateDir, "runs", hex.EncodeToString(sum[:]))
}

// runScript is the file Run executes: the command exactly as the CFO wrote
// it. A PowerShell script starts with a UTF-8 byte order mark, without which
// Windows PowerShell 5.1 reads it in the ANSI code page.
func runScript(r Run) (name string, data []byte) {
	if r.Shell == "bash" {
		return "command.sh", []byte(r.Command)
	}
	return "command.ps1", append([]byte("\xef\xbb\xbf"), r.Command...)
}

func runDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// PublishRun records a command for the Overlord to run, from the registered
// primary CFO process only. It reads the command file once and writes that
// text to the script file Run executes, so no quoting can change it.
func PublishRun(ctx context.Context, h home.Home, client *herdr.Client, req RunRequest) error {
	identity, release, err := (&CFOConnection{State: h.State, Herdr: client}).CallerIdentity(ctx)
	if err != nil {
		return err
	}
	defer release()
	command, err := readRunCommand(req.CommandFile)
	if err != nil {
		return err
	}
	cwd := req.Cwd
	if cwd == "" {
		cwd = h.Root
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return fmt.Errorf("the run's folder %s is not a directory", cwd)
	}
	now := time.Now().UTC()
	r := Run{ID: req.ID, Identity: identity, Title: req.Title, Shell: req.Shell, Admin: req.Admin, Command: command, Cwd: cwd, State: "ready", CreatedAt: now, ExpiresAt: now.Add(runLifetime)}
	if err := validRun(r); err != nil {
		return err
	}
	unlock, err := runPublishLock(h.State)
	if err != nil {
		return err
	}
	defer unlock()
	if err := runInboxRoom(h.State); err != nil {
		return err
	}
	prior, found, err := reportedRun(h.State, r.ID)
	if err != nil {
		return err
	}
	if found {
		if !sameRun(prior, r) {
			return errors.New("run ID already used")
		}
		return nil
	}
	dir := runDir(h.State, r)
	name, script := runScript(r)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := fsx.AtomicWriteFile(filepath.Join(dir, name), script); err != nil {
		return errors.Join(err, os.RemoveAll(dir))
	}
	r.ScriptSum = runDigest(script)
	if err := spoolRun(h.State, r); err != nil {
		return errors.Join(err, os.RemoveAll(dir))
	}
	return nil
}

func readRunCommand(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxRunCommand+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxRunCommand {
		return "", fmt.Errorf("the command file is over %d KiB", maxRunCommand>>10)
	}
	return string(data), nil
}

// runPublishLock serializes publications, waiting briefly for another one.
func runPublishLock(stateDir string) (func(), error) {
	_, err := lock.AcquireExclusiveNamed(stateDir, ".run-publish.lock")
	for deadline := time.Now().Add(2 * time.Second); err != nil && time.Now().Before(deadline); {
		time.Sleep(25 * time.Millisecond)
		_, err = lock.AcquireExclusiveNamed(stateDir, ".run-publish.lock")
	}
	if err != nil {
		return nil, err
	}
	return func() { _ = lock.ReleaseExclusiveNamed(stateDir, ".run-publish.lock") }, nil
}

// reportedRun finds an item waiting in the inbox or already recorded, reading
// the inbox first because ingest records an item before it removes the inbox
// copy. It only reads: opening a Store here would run crash recovery under a
// live supervisor.
func reportedRun(stateDir, id string) (Run, bool, error) {
	data, err := os.ReadFile(runInboxPath(stateDir, id))
	if err == nil {
		var prior Run
		if err := json.Unmarshal(data, &prior); err != nil {
			return Run{}, false, errors.New("run ID already used")
		}
		return prior, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Run{}, false, err
	}
	info, err := os.Stat(filepath.Join(stateDir, ".supervisor.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	if info.Size() > maxStateBytes {
		return Run{}, false, errors.New("supervisor state exceeds its bound")
	}
	data, err = os.ReadFile(filepath.Join(stateDir, ".supervisor.json"))
	if err != nil {
		return Run{}, false, err
	}
	var db Database
	if err := json.Unmarshal(data, &db); err != nil {
		return Run{}, false, errors.New("supervisor run history is unreadable")
	}
	if i := slices.IndexFunc(db.Runs, func(r Run) bool { return r.ID == id }); i >= 0 {
		return db.Runs[i], true, nil
	}
	return Run{}, false, nil
}

func runInboxPath(stateDir, id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(stateDir, "runs-inbox", hex.EncodeToString(sum[:])+".json")
}

// runInboxRoom refuses a publication while the inbox is full.
func runInboxRoom(stateDir string) error {
	dir := filepath.Join(stateDir, "runs-inbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) >= maxRuns {
		return errors.New("the run inbox is full")
	}
	return nil
}

func spoolRun(stateDir string, r Run) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(runInboxPath(stateDir, r.ID), data)
}

func (s *Store) ingestRuns() error {
	dir := filepath.Join(s.Home.State, "runs-inbox")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries[:min(len(entries), maxRuns)] {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var r Run
		invalid := error(nil)
		if info.Size() > 2*maxRunCommand {
			invalid = errors.New("run item exceeds its size limit")
		} else if data, err := os.ReadFile(path); err != nil {
			return err
		} else if json.Unmarshal(data, &r) != nil {
			invalid = errors.New("invalid run item JSON")
		}
		if invalid == nil {
			invalid = s.acceptRun(r)
		}
		if errors.Is(invalid, ErrStorage) {
			return invalid
		}
		if errors.Is(invalid, ErrDeferred) {
			continue
		}
		if invalid != nil {
			s.mu.Lock()
			s.db.Issues = append(s.db.Issues, "Run item rejected: "+bounded(invalid.Error(), 300))
			if len(s.db.Issues) > 20 {
				s.db.Issues = s.db.Issues[len(s.db.Issues)-20:]
			}
			err := s.save()
			s.mu.Unlock()
			if err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) acceptRun(r Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validRun(r); err != nil {
		return err
	}
	if i := slices.IndexFunc(s.db.Runs, func(prior Run) bool { return prior.ID == r.ID }); i >= 0 {
		if !sameRun(s.db.Runs[i], r) {
			return errors.New("run ID already used")
		}
		return nil
	}
	// The script on disk must be exactly the command the CFO published.
	name, script := runScript(r)
	data, err := os.ReadFile(filepath.Join(runDir(s.Home.State, r), name))
	if err != nil || r.ScriptSum != runDigest(script) || runDigest(data) != r.ScriptSum {
		return errors.New("the run item's script file is missing or changed")
	}
	if len(s.db.Runs) >= maxRuns {
		done := slices.IndexFunc(s.db.Runs, func(old Run) bool { return old.State != "ready" && old.State != "running" })
		if done < 0 {
			return ErrDeferred
		}
		if err := os.RemoveAll(runDir(s.Home.State, s.db.Runs[done])); err != nil {
			return err
		}
		s.db.Runs = slices.Delete(s.db.Runs, done, done+1)
	}
	r.State, r.ExpiresAt = "ready", r.CreatedAt.Add(runLifetime)
	r.ExitCode, r.Output, r.Reason, r.RunAction, r.PID, r.Started, r.RanAt, r.FinishedAt = nil, "", "", "", 0, nil, nil, nil
	s.db.Runs = append(s.db.Runs, r)
	return s.save()
}

// expireRuns closes each item nobody ran within its lifetime.
func (s *Store) expireRuns(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for i := range s.db.Runs {
		if r := &s.db.Runs[i]; r.State == "ready" && !now.Before(r.ExpiresAt) {
			r.State, r.Reason = "expired", "nobody ran it within 24 hours"
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.save()
}

// pruneRuns drops finished and expired items, with their directories, a set
// time after they ended.
func (s *Store) pruneRuns(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.db.Runs[:0:0]
	for _, r := range s.db.Runs {
		ended := r.ExpiresAt
		if r.FinishedAt != nil {
			ended = *r.FinishedAt
		}
		if r.State == "ready" || r.State == "running" || now.Sub(ended) < finishedRunRetention {
			kept = append(kept, r)
			continue
		}
		if err := os.RemoveAll(runDir(s.Home.State, r)); err != nil {
			return err
		}
	}
	if len(kept) == len(s.db.Runs) {
		return nil
	}
	s.db.Runs = kept
	return s.save()
}

func (s *Store) markRunStarted(id, action string, started RunStarted) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.db.Runs, func(r Run) bool { return r.ID == id && r.RunAction == action && r.State == "running" })
	if i < 0 {
		return fmt.Errorf("the running item %s is gone", id)
	}
	s.db.Runs[i].PID, s.db.Runs[i].Started = started.PID, &started.Start
	return s.save()
}

// finishRun records how a running item ended, once: it reports false when the
// item already ended.
func (s *Store) finishRun(id, action string, code *int, output, reason string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.db.Runs, func(r Run) bool { return r.ID == id && r.RunAction == action })
	if i < 0 || s.db.Runs[i].State != "running" {
		return false, nil
	}
	now := time.Now().UTC()
	r := &s.db.Runs[i]
	r.State, r.ExitCode, r.Output, r.Reason, r.FinishedAt = "failed", code, output, reason, &now
	if code != nil && *code == 0 {
		r.State = "succeeded"
	}
	return true, s.save()
}

// noteRun adds a note to how an item ended.
func (s *Store) noteRun(id, action, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.db.Runs, func(r Run) bool { return r.ID == id && r.RunAction == action })
	if i < 0 {
		return nil
	}
	if r := &s.db.Runs[i]; r.Reason == "" {
		r.Reason = note
	} else {
		r.Reason += "; " + note
	}
	return s.save()
}

// startRun opens the item the Overlord pressed Run on, in exactly the shell it
// names, and returns once it is launched; finishRuns ends it.
func (s *Service) startRun(ctx context.Context, a Action) (Evaluation, error) {
	runs := s.Store.Snapshot().Runs
	i := slices.IndexFunc(runs, func(r Run) bool { return r.ID == a.RunID && r.Identity == a.Generation && r.RunAction == a.ID })
	if i < 0 {
		return Evaluation{}, fmt.Errorf("%w: the run item changed; nothing ran", ErrRejected)
	}
	r := runs[i]
	dir := runDir(s.Store.Home.State, r)
	name, _ := runScript(r)
	script := filepath.Join(dir, name)
	var started RunStarted
	data, err := os.ReadFile(script)
	switch {
	case err != nil || runDigest(data) != r.ScriptSum:
		err = errors.New("its script file is missing or changed")
	case s.Options.Runs == nil:
		err = errors.New("this supervisor cannot open run windows")
	default:
		started, err = s.Options.Runs.Launch(ctx, RunLaunch{Shell: r.Shell, Admin: r.Admin, Script: script, Dir: dir, Cwd: r.Cwd})
	}
	if err != nil {
		reason := "it could not start: " + err.Error()
		return Evaluation{}, errors.Join(fmt.Errorf("%w: nothing ran, %s", ErrRejected, reason), s.completeRun(ctx, r, nil, reason))
	}
	if err := s.Store.markRunStarted(r.ID, r.RunAction, started); err != nil {
		return Evaluation{}, err
	}
	return Evaluation{Reason: "The run started."}, nil
}

// finishRuns ends each running item whose command finished, whose window
// closed before it did, or whose elevation Windows did not start, and one
// whose Run ended without recording a launch.
func (s *Service) finishRuns(ctx context.Context) error {
	var errs error
	d := s.Store.Snapshot()
	for _, r := range d.Runs {
		if r.State != "running" {
			continue
		}
		if r.Started == nil {
			if !slices.ContainsFunc(d.Actions, func(a Action) bool { return a.ID == r.RunAction && (a.Status == "queued" || a.Status == "running") }) {
				errs = errors.Join(errs, s.completeRun(ctx, r, nil, "its start was interrupted, so whether its window opened is unknown"))
			}
			continue
		}
		dir := runDir(s.Store.Home.State, r)
		if code, ok := readRunExit(dir); ok {
			errs = errors.Join(errs, s.completeRun(ctx, r, &code, ""))
		} else if declined, err := os.ReadFile(filepath.Join(dir, "declined.txt")); err == nil {
			errs = errors.Join(errs, s.completeRun(ctx, r, nil, "Windows did not start it elevated: "+bounded(strings.TrimSpace(string(declined)), 300)))
		} else if !runProcessAlive(r.PID, *r.Started) {
			// The run may have written its exit code just before it ended.
			if code, ok := readRunExit(dir); ok {
				errs = errors.Join(errs, s.completeRun(ctx, r, &code, ""))
			} else {
				errs = errors.Join(errs, s.completeRun(ctx, r, nil, "its window closed before the command finished"))
			}
		}
	}
	return errs
}

func runProcessAlive(pid int, start time.Time) bool {
	entries, err := proc.Ancestry(pid, 1)
	return err == nil && len(entries) == 1 && entries[0].Start.Equal(start)
}

// completeRun records how an item ended, appends its audit line and hands the
// result to the CFO as its answer, so the CFO continues without asking
// whether it worked. The full output stays on the item.
func (s *Service) completeRun(ctx context.Context, r Run, code *int, reason string) error {
	output := readRunOutput(runDir(s.Store.Home.State, r))
	ended, err := s.Store.finishRun(r.ID, r.RunAction, code, output, reason)
	if err != nil || !ended {
		return err
	}
	err = appendRunAudit(s.Store.Home.State, r, code, time.Now().UTC())
	text := fmt.Sprintf("Run item %s (%s) did not finish: %s.", r.ID, r.Title, reason)
	if code != nil {
		text = fmt.Sprintf("Run item %s (%s) finished with exit code %d.", r.ID, r.Title, *code)
	}
	if output != "" {
		text += " The output ends: " + tail(output, 1500)
	}
	if delivery := s.tellCFO(ctx, text); delivery != nil {
		err = errors.Join(err, s.Store.noteRun(r.ID, r.RunAction, "the CFO could not be told: "+bounded(delivery.Error(), 300)))
	}
	return err
}

// tellCFO sends a run's result to the CFO registered now, which may have
// restarted since it created the item.
func (s *Service) tellCFO(ctx context.Context, text string) error {
	if s.Options.CFO == nil {
		return errors.New("no CFO transport")
	}
	file, err := openPrimary(filepath.Join(s.Store.Home.State, "primary.json"))
	if err != nil {
		return errors.New("no CFO is registered")
	}
	_, identity, err := decodePrimary(file)
	_ = file.Close()
	if err != nil {
		return errors.New("the CFO registration is unreadable")
	}
	_, err = s.Options.CFO.Send(ctx, identity, text)
	return err
}

func tail(text string, n int) string {
	if len(text) <= n {
		return text
	}
	text = text[len(text)-n:]
	for len(text) > 0 && !utf8.RuneStart(text[0]) {
		text = text[1:]
	}
	return "..." + text
}

// readRunExit reads the exit code a run writes when its command finishes.
func readRunExit(dir string) (int, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "exit.txt"))
	if err != nil {
		return 0, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(string(data), "\xef\xbb\xbf")))
	return code, err == nil
}

// readRunOutput reads the end of what a run printed.
func readRunOutput(dir string) string {
	f, err := os.Open(filepath.Join(dir, "output.log"))
	if err != nil {
		return ""
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > maxRunOutput {
		if _, err := f.Seek(info.Size()-maxRunOutput, io.SeekStart); err != nil {
			return ""
		}
	}
	data, _ := io.ReadAll(io.LimitReader(f, maxRunOutput))
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	for len(text) > 0 && !utf8.RuneStart(text[0]) {
		text = text[1:]
	}
	return strings.ToValidUTF8(text, "?")
}

// appendRunAudit records a run in state/runs.audit: when, the item, the
// SHA-256 of exactly the script file it ran, and its exit code, or none when
// it did not finish.
func appendRunAudit(stateDir string, r Run, code *int, now time.Time) error {
	exit := "none"
	if code != nil {
		exit = strconv.Itoa(*code)
	}
	f, err := os.OpenFile(filepath.Join(stateDir, "runs.audit"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintf(f, "%s %s %s %s\n", now.Format(time.RFC3339), r.ID, r.ScriptSum, exit)
	return errors.Join(werr, f.Close())
}

// runShellPath finds exactly the shell an item names, never another one:
// Windows PowerShell 5.1 under SystemRoot, PowerShell 7 as pwsh on PATH, and
// Git Bash as the bash.exe Git for Windows installs beside its git.exe. The
// bash.exe under SystemRoot is WSL and never runs an item.
func runShellPath(shell string, lookPath func(string) (string, error), exists func(string) bool, systemRoot string) (string, error) {
	switch shell {
	case "powershell":
		path := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if systemRoot == "" || !exists(path) {
			return "", errors.New("Windows PowerShell 5.1 is not installed")
		}
		return path, nil
	case "pwsh":
		path, err := lookPath("pwsh")
		if err != nil {
			return "", errors.New("PowerShell 7 (pwsh) is not on PATH")
		}
		return path, nil
	case "bash":
		git, err := lookPath("git")
		if err != nil {
			return "", errors.New("Git Bash is not installed: git is not on PATH")
		}
		// git.exe sits in cmd, bin or mingw64\bin under the Git folder.
		root := filepath.Dir(filepath.Dir(git))
		if strings.EqualFold(filepath.Base(root), "mingw64") {
			root = filepath.Dir(root)
		}
		path := filepath.Join(root, "bin", "bash.exe")
		inSystemRoot := systemRoot != "" && strings.HasPrefix(strings.ToLower(path), strings.ToLower(filepath.Clean(systemRoot))+`\`)
		if inSystemRoot || !exists(path) {
			return "", fmt.Errorf("Git Bash is not installed beside %s", git)
		}
		return path, nil
	}
	return "", fmt.Errorf("unknown shell %q", shell)
}
