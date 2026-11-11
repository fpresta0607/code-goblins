package supervisor

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/doctor"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
)

// The Overlord, 2026-11-10: "the Auto-update failed notice needs a reason and
// a Retry" (#2333). A harness draws that notice for as long as its session
// runs and tells him to run a command in the terminal, which for Claude Code
// is /doctor: a prompt that starts a turn of its own in the conversation, and
// one the board typed into the CEO's running turn three times that day. The
// supervisor runs the harness's own update check outside every session
// instead, says how it ended, and no longer shows the notice once the install
// is the newest its publisher offers, which no session restarts for.

const (
	// updateCheckTimeout bounds one run of a harness's update check.
	updateCheckTimeout = 3 * time.Minute
	// updateCurrentEvery is how long a read of an install against its
	// publisher's newest stands before a screen that still shows the notice
	// has it read again.
	updateCurrentEvery = 15 * time.Minute
	// updateResultLimit bounds what a check's result says on the board.
	updateResultLimit = 300
)

// UpdateCheck is what the board knows of a harness's own update check, which
// it runs outside every session. IsRunning says a check he asked for runs
// now; Result is how the last one ended, the reason it failed when HasFailed;
// IsCurrent says the install is the newest its publisher offers, as its
// version read against the publisher's, so a notice of a failed update is out
// of date.
type UpdateCheck struct {
	IsRunning bool   `json:"running,omitempty"`
	HasFailed bool   `json:"failed,omitempty"`
	IsCurrent bool   `json:"current,omitempty"`
	Result    string `json:"result,omitempty"`
	// isReading says the install is being read against its publisher's
	// newest now, and read when it last was.
	isReading bool
	read      time.Time
}

// updateCheckView is each harness's update check for the snapshot.
func (s *Service) updateCheckView() map[string]UpdateCheck {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.updateChecks) == 0 {
		return nil
	}
	return maps.Clone(s.updateChecks)
}

// changeUpdateCheck changes what the board knows of kind's update check and
// reports whether what the board shows of it changed.
func (s *Service) changeUpdateCheck(kind harness.Kind, change func(*UpdateCheck)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updateChecks == nil {
		s.updateChecks = map[string]UpdateCheck{}
	}
	check := s.updateChecks[string(kind)]
	before := check
	change(&check)
	s.updateChecks[string(kind)] = check
	return before.IsRunning != check.IsRunning || before.HasFailed != check.HasFailed || before.IsCurrent != check.IsCurrent || before.Result != check.Result
}

// retryUpdateCheck starts kind's own update check outside every session,
// unless one runs, and returns at once: the snapshot shows it running, then
// how it ended. A check that succeeded is followed by a read of the install
// against its publisher's newest, which is what takes the notice away.
func (s *Service) retryUpdateCheck(kind harness.Kind) error {
	if release, isKnown := doctor.HarnessReleases[string(kind)]; !isKnown || release.Update == "" {
		return fmt.Errorf("%s has no update check the board can run", kind)
	}
	isStarted := false
	s.changeUpdateCheck(kind, func(check *UpdateCheck) {
		isStarted = !check.IsRunning
		check.IsRunning = true
	})
	if !isStarted {
		return nil
	}
	s.notify()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
		defer cancel()
		result, err := s.runUpdateCheck(ctx, kind)
		isCurrent := false
		if err == nil {
			// A check that says it succeeded and cannot be read against the
			// publisher is taken at its word.
			current, readErr := s.readHarnessCurrent(ctx, kind)
			isCurrent = current || readErr != nil
		}
		s.changeUpdateCheck(kind, func(check *UpdateCheck) {
			check.IsRunning, check.HasFailed, check.Result, check.IsCurrent, check.read = false, err != nil, result, isCurrent, time.Now()
			if err != nil {
				check.Result = bounded(err.Error(), updateResultLimit)
			}
		})
		s.notify()
	}()
	return nil
}

// noticeUpdateFailure has kind's install read against its publisher's newest
// when a session's screen shows its notice of a failed update and no read
// from the last updateCurrentEvery stands: an install that is the newest
// needs no update, whatever a session that started before it still shows.
func (s *Service) noticeUpdateFailure(kind harness.Kind) {
	isDue := false
	s.changeUpdateCheck(kind, func(check *UpdateCheck) {
		isDue = !check.IsRunning && !check.isReading && time.Since(check.read) >= updateCurrentEvery
		check.isReading = check.isReading || isDue
	})
	if !isDue {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		current, err := s.readHarnessCurrent(ctx, kind)
		if s.changeUpdateCheck(kind, func(check *UpdateCheck) {
			check.isReading, check.read, check.IsCurrent = false, time.Now(), err == nil && current
		}) {
			s.notify()
		}
	}()
}

// runUpdateCheck runs kind's update check, through the test's when it has
// one, and returns what it said: how it succeeded, or as its error why it
// failed.
func (s *Service) runUpdateCheck(ctx context.Context, kind harness.Kind) (string, error) {
	if s.Options.UpdateCheck != nil {
		return s.Options.UpdateCheck(ctx, kind)
	}
	command := strings.Fields(doctor.HarnessReleases[string(kind)].Update)
	program, err := exec.LookPath(command[0])
	if err != nil {
		return "", fmt.Errorf("%s is not on PATH", command[0])
	}
	check := execx.CommandContext(ctx, program, command[1:]...)
	check.Env = append(os.Environ(), "NO_COLOR=1")
	output, err := check.CombinedOutput()
	said := lastWords(string(output))
	switch {
	case ctx.Err() != nil:
		return "", fmt.Errorf("%s did not end within %s", strings.Join(command, " "), updateCheckTimeout)
	case err != nil && said == "":
		return "", fmt.Errorf("%s failed: %w", strings.Join(command, " "), err)
	case err != nil:
		return "", errors.New(said)
	}
	return bounded(said, updateResultLimit), nil
}

// readHarnessCurrent reads whether kind's install is the newest its
// publisher offers, through the test's reader when it has one: the version
// its program answers with against its release source's.
func (s *Service) readHarnessCurrent(ctx context.Context, kind harness.Kind) (bool, error) {
	if s.Options.HarnessCurrent != nil {
		return s.Options.HarnessCurrent(ctx, kind)
	}
	program, err := exec.LookPath(string(kind))
	if err != nil {
		return false, err
	}
	releases, err := doctor.ReleasesFromEnvironment()
	if err != nil {
		return false, err
	}
	versions := releases.Versions(ctx, http.DefaultClient, []doctor.HarnessProbe{doctor.ProbeHarness(ctx, string(kind), program)})
	if len(versions) != 1 || versions[0].Problem != "" {
		return false, fmt.Errorf("the newest %s could not be read", kind)
	}
	return versions[0].Update == "", nil
}

// terminalColours matches the escape sequences a program colours its words
// with.
var terminalColours = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// lastWords is the last line a program wrote that holds words, without its
// colours: what an update check ends by saying.
func lastWords(output string) string {
	lines := strings.Split(terminalColours.ReplaceAllString(output, ""), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// checkHarnessUpdate serves POST /api/harness/update-check, the Retry on a
// session's notice of a failed update: it starts the named harness's own
// update check outside every session.
func (h *HTTP) checkHarnessUpdate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Harness string `json:"harness"`
	}
	if err := decodeBody(w, r, &input, 1<<10); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.Service.retryUpdateCheck(harness.Kind(input.Harness)); err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	respond(w, http.StatusAccepted, struct {
		IsRunning bool `json:"running"`
	}{true})
}
