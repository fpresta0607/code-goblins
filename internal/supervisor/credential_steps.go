package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// credentialWaitTick is how often a wait on a card looks at the board's
// record, so the asker goes on within a fraction of a second of the card
// closing.
const credentialWaitTick = 200 * time.Millisecond

// CredentialStep is what a card's asker, or the CFO, does to it from outside
// the board: done, once the asker saw the step done; withdraw, once nobody
// needs it; and, from the goblin's wait, window-open or window-failed once
// it showed its browser's window or could not.
type CredentialStep struct {
	ID     string `json:"id"`
	Task   string `json:"task,omitempty"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

// StepCredential sends step to this home's supervisor: a goblin for its own
// task's card, proven from inside its terminal, or the registered CFO, whom
// the supervisor proves over its pipe.
func StepCredential(h home.Home, step CredentialStep) error {
	if !credentialID.MatchString(step.ID) {
		return errors.New("a card is named by its ID, cred- and 16 hexadecimal digits")
	}
	if step.Task != "" {
		if _, err := goblinAsker(h.State, step.Task); err != nil {
			return fmt.Errorf("only the goblin of %s, from its own terminal, can act on its card: %w", step.Task, err)
		}
	}
	return sendPipeRequest(h.State, runPipeRequest{Kind: "credential-step", Step: &step})
}

// stepCredential takes a step from process pid, connected at connected,
// once that process is proven to be the goblin the card is for, or the
// registered CFO.
func (s *Service) stepCredential(pid int, connected time.Time, step CredentialStep) error {
	request, found := s.Store.credential(step.ID)
	if !found {
		return fmt.Errorf("card %s is not on the board", step.ID)
	}
	by := "cfo"
	if step.Task != "" {
		if err := goblinPipeCaller(s.Store.Home.State, step.Task, pid, connected); err != nil {
			return fmt.Errorf("card caller: %w", err)
		}
		if request.Task != step.Task {
			return errors.New("only the goblin a card is for, or the CFO, can act on it")
		}
		by = "goblin"
	} else {
		if s.Options.CFO == nil {
			return errors.New("this supervisor cannot verify the CFO")
		}
		_, release, err := s.Options.CFO.identityOf(pid, connected)
		if err != nil {
			return err
		}
		release()
	}
	if step.Reason != "" {
		if err := oneLineReason(step.Reason); err != nil {
			return err
		}
	}
	switch step.Action {
	case "done":
		_, err := s.closeCredential(step.ID, "", "done", by, step.Reason)
		return err
	case "withdraw":
		if strings.TrimSpace(step.Reason) == "" {
			return errors.New("a withdrawal says why")
		}
		_, err := s.closeCredential(step.ID, "", "withdrawn", by, step.Reason)
		return err
	case "window-open", "window-failed":
		if by != "goblin" {
			return errors.New("only the goblin whose browser it is reports its window")
		}
		return s.reportCredentialWindow(step.ID, step.Action == "window-open", step.Reason)
	}
	return fmt.Errorf("a card takes done, withdraw, window-open or window-failed, not %q", step.Action)
}

// oneLineReason refuses a reason that is not one line of prose of at most
// 300 characters, or that holds anything shaped like a credential value.
func oneLineReason(reason string) error {
	if len(reason) > 300 || strings.ContainsFunc(reason, unicode.IsControl) {
		return errors.New("a reason is one line of at most 300 characters")
	}
	for index, word := range strings.Fields(reason) {
		if shape := auth.SecretShape(word); shape != "" {
			return fmt.Errorf("word %d of the reason looks like a credential value: %s", index+1, shape)
		}
	}
	return nil
}

// closeCredential closes an open card as done, dismissed or withdrawn: by
// the Overlord on the board, who names its generation, or by its asker or
// the CFO, who name none. His Done closes only a card that asks for no
// input, since one that asks closes when the input arrives. A closed card
// takes no value, and its asker's wait ends at once.
func (s *Service) closeCredential(id, generation, closed, by, reason string) (CredentialRequest, error) {
	s.credentialSaves.Lock()
	defer s.credentialSaves.Unlock()
	request, found := s.Store.credential(id)
	switch {
	case !found || generation != "" && generation != request.Generation:
		return request, credentialRefusal{status: http.StatusConflict, Message: "This card is not on the board. Refresh the board."}
	case request.State != "open":
		return request, credentialRefusal{status: http.StatusConflict, Message: "This card was already " + request.State + "."}
	case closed == "done" && by == "overlord" && len(request.inputs()) > 0:
		return request, credentialRefusal{status: http.StatusConflict, Message: "This card closes by itself once its values arrive."}
	case closed == "done" && credentialTerminalOpen(s.Store.Snapshot().Runs, request.ID):
		return request, credentialRefusal{status: http.StatusConflict, Message: "A terminal for this card is open. Finish it there first."}
	}
	now := time.Now().UTC()
	updated, err := s.Store.updateCredential(id, func(r *CredentialRequest) {
		r.State, r.ClosedAt = closed, &now
		if reason != "" {
			r.Reason = bounded(reason, 300)
		}
	})
	if err != nil {
		return updated, err
	}
	s.notify()
	s.credentialWork.Add(1)
	go s.afterCredentialClose(updated, by)
	return updated, nil
}

// afterCredentialClose tells whoever must hear that a card closed: the CFO
// of his Done or Dismiss and of a goblin's own done or withdrawal, and the
// goblin, in its own terminal, that he dismissed its card and it goes on
// without it.
func (s *Service) afterCredentialClose(request CredentialRequest, by string) {
	defer s.credentialWork.Done()
	whose := "the CFO's card " + request.ID
	if request.Task != "" {
		whose = request.Task + "'s card " + request.ID
	}
	whose += " (" + request.Why + ")"
	switch {
	case request.State == "dismissed":
		told := ""
		if request.By == "goblin" {
			told = "; the goblin was told"
			if s.Options.CFO == nil {
				told = "; the goblin could not be told: message transport is unavailable"
			} else if _, err := s.Options.CFO.SendGoblin(context.Background(), request.Task, request.Identity, "The Overlord dismissed your card "+request.ID+" ("+request.Why+") from the Command Center. Go on without it. If you still need him for it, file a new card."); err != nil {
				told = "; the goblin could not be told: " + bounded(err.Error(), 300)
			}
		}
		goesOn := "you go on without it"
		if request.Task != "" {
			goesOn = request.Task + " goes on without it"
		}
		s.credentialNotice(request, "the Overlord dismissed "+whose+" from the Command Center, so "+goesOn+told)
	case request.State == "done" && by == "overlord":
		s.credentialNotice(request, "the Overlord pressed Done on "+whose)
	case request.State == "done":
		s.credentialNotice(request, whose+" closed as done: the goblin saw the step done")
	case request.State == "withdrawn" && by == "goblin":
		s.credentialNotice(request, whose+" withdrawn by the goblin: "+request.Reason)
	}
}

// requestCredentialWindow records his press of a card's window button, which
// the goblin's wait sees and answers by showing its browser's window.
func (s *Service) requestCredentialWindow(id, generation string) error {
	s.credentialSaves.Lock()
	defer s.credentialSaves.Unlock()
	request, err := s.openCredential(id, generation)
	if err != nil {
		return err
	}
	if !request.Window {
		return credentialRefusal{status: http.StatusConflict, Message: "This card has no window to open."}
	}
	if request.WindowState != "" {
		return nil
	}
	if _, err := s.Store.updateCredential(id, func(r *CredentialRequest) { r.WindowState, r.Reason = "requested", "" }); err != nil {
		return err
	}
	s.notify()
	return nil
}

// reportCredentialWindow records what the goblin's wait did with his press:
// the window is open, or it could not open, and the button is offered again
// with why.
func (s *Service) reportCredentialWindow(id string, isOpen bool, reason string) error {
	s.credentialSaves.Lock()
	defer s.credentialSaves.Unlock()
	request, found := s.Store.credential(id)
	if !found || request.State != "open" || !request.Window {
		return errors.New("that card has no window waiting")
	}
	if _, err := s.Store.updateCredential(id, func(r *CredentialRequest) {
		if isOpen {
			r.WindowState, r.Reason = "open", ""
		} else {
			r.WindowState, r.Reason = "", bounded(reason, 300)
		}
	}); err != nil {
		return err
	}
	s.notify()
	return nil
}

// closeCredentialCard takes his Done or Dismiss on a card, from any board.
func (h *HTTP) closeCredentialCard(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID         string `json:"id"`
		Generation string `json:"generation"`
		Action     string `json:"action"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	closed := map[string]string{"done": "done", "dismiss": "dismissed"}[input.Action]
	if closed == "" || input.Generation == "" {
		apiError(w, http.StatusBadRequest, "Send the card's ID, its generation, and done or dismiss.")
		return
	}
	request, err := h.Service.closeCredential(input.ID, input.Generation, closed, "overlord", "")
	respondCredential(w, struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}{request.ID, request.State}, err)
}

// openCredentialWindow takes his press of a card's window button, from the
// board on this PC alone, since the window opens there.
func (h *HTTP) openCredentialWindow(w http.ResponseWriter, r *http.Request) {
	if offMachine(r, h.Host) != "" {
		apiError(w, http.StatusForbidden, "Open the window from the board on this PC.")
		return
	}
	var input struct {
		ID         string `json:"id"`
		Generation string `json:"generation"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	err := h.Service.requestCredentialWindow(input.ID, input.Generation)
	respondCredential(w, struct {
		ID string `json:"id"`
	}{input.ID}, err)
}

// respondCredential answers a card's press: its refusal with the status it
// names, any other failure as the board's, or the answer.
func respondCredential(w http.ResponseWriter, answer any, err error) {
	var refusal credentialRefusal
	switch {
	case errors.As(err, &refusal):
		respond(w, refusal.status, refusal)
	case err != nil:
		apiError(w, http.StatusInternalServerError, err.Error())
	default:
		respond(w, http.StatusOK, answer)
	}
}

// retireGoblinCredentials withdraws a goblin's open card once nobody waits
// on it: its goblin is gone or was replaced, or reported since it asked that
// it finished or failed, as its other items are retired.
func (s *Store) retireGoblinCredentials() error {
	for _, r := range s.Snapshot().Credentials {
		if r.State != "open" || r.By != "goblin" {
			continue
		}
		meta, err := state.ReadTaskMeta(s.Home.State, r.Task)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		var reason string
		if err != nil || goblinIdentity(meta) != r.Identity {
			reason = r.Task + " is gone"
		} else {
			lines, err := state.TailStatus(s.Home.State, r.Task, 50)
			if err != nil {
				return err
			}
			reportedAt, report := latestReport(lines, time.Time{})
			if !strings.HasPrefix(report, "done: ") && !strings.HasPrefix(report, "failed: ") || !reportedAt.After(r.CreatedAt) {
				continue
			}
			reason = r.Task + " reported: " + report
		}
		now := time.Now().UTC()
		if _, err := s.updateCredential(r.ID, func(c *CredentialRequest) {
			if c.State == "open" {
				c.State, c.Reason, c.ClosedAt = "withdrawn", bounded(reason, 300), &now
			}
		}); err != nil {
			return err
		}
	}
	return nil
}

// WaitCredential waits until card id closes and returns it as it closed,
// with the plain values he entered. It reads the board's own record, so the
// asker goes on within a fraction of a second, and calls seen with the card
// each time it changes while open. A card neither on the board nor waiting
// in its inbox was refused, and the wait ends saying so.
func WaitCredential(ctx context.Context, stateDir, id string, seen func(CredentialRequest)) (CredentialRequest, error) {
	var last CredentialRequest
	var stamp time.Time
	var size int64 = -1
	// The record is read when it changed, and every few seconds whatever its
	// stamp says.
	for look := 0; ; look++ {
		info, statErr := os.Stat(filepath.Join(stateDir, ".supervisor.json"))
		changed := statErr != nil || !info.ModTime().Equal(stamp) || info.Size() != size || look%10 == 0
		if statErr == nil {
			stamp, size = info.ModTime(), info.Size()
		}
		if changed {
			request, found, err := readCredential(stateDir, id)
			if errors.Is(err, errStateBusy) {
				size = -1
			}
			if err == nil && !found && !credentialSpooled(stateDir, id) {
				// The board may have taken it between the two looks.
				if request, found, err = readCredential(stateDir, id); err == nil && !found {
					return request, fmt.Errorf("card %s is not on the board or in its inbox: the board refused it, and its issues say why", id)
				}
			}
			switch {
			case err != nil && !errors.Is(err, errStateBusy):
				return last, err
			case found && request.State != "open":
				return request, nil
			case found && (request.WindowState != last.WindowState || request.Reason != last.Reason || last.ID == ""):
				last = request
				if seen != nil {
					seen(request)
				}
			}
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(credentialWaitTick):
		}
	}
}

// errStateBusy is a board record caught mid-write, which the next look reads
// whole.
var errStateBusy = errors.New("the board's record is being written")

// readCredential reads card id from the board's record.
func readCredential(stateDir, id string) (CredentialRequest, bool, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, ".supervisor.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return CredentialRequest{}, false, nil
	}
	if err != nil {
		return CredentialRequest{}, false, errors.Join(errStateBusy, err)
	}
	if len(data) > maxStateBytes {
		return CredentialRequest{}, false, errors.New("supervisor state exceeds its bound")
	}
	var record struct {
		Credentials []CredentialRequest `json:"credentials"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return CredentialRequest{}, false, errors.Join(errStateBusy, err)
	}
	i := slices.IndexFunc(record.Credentials, func(r CredentialRequest) bool { return r.ID == id })
	if i < 0 {
		return CredentialRequest{}, false, nil
	}
	return record.Credentials[i], true, nil
}

// credentialSpooled reports whether card id waits in the inbox for the
// supervisor to take it.
func credentialSpooled(stateDir, id string) bool {
	sum := sha256.Sum256([]byte(id))
	_, err := os.Stat(filepath.Join(stateDir, credentialInbox, hex.EncodeToString(sum[:])+".json"))
	return err == nil
}
