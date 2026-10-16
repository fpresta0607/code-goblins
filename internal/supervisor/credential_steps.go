package supervisor

import (
	"context"
	"crypto/rand"
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
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// credentialWaitTick is how often a wait on a card looks at the board's
// record, so the asker goes on within a fraction of a second of the card
// closing.
const credentialWaitTick = 200 * time.Millisecond

// maxCredentialAnswer is the longest answer he writes on a card instead of
// its step.
const maxCredentialAnswer = 4000

// CredentialStep is what a card's asker, or the CFO, does to it from outside
// the board: done, once the asker saw the step done; withdraw, once nobody
// needs it; reopen, to bring back a card he closed; and, from the goblin's
// wait, window-open or window-failed once it showed its browser's window or
// could not.
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
	case "reopen":
		return s.reopenCredential(step.ID, "", by)
	case "window-open", "window-failed":
		if by != "goblin" {
			return errors.New("only the goblin whose browser it is reports its window")
		}
		return s.reportCredentialWindow(step.ID, step.Action == "window-open", step.Reason)
	}
	return fmt.Errorf("a card takes done, withdraw, reopen, window-open or window-failed, not %q", step.Action)
}

// reopenCredential brings back a card he pressed Done on, answered or
// dismissed: by the Overlord from History on the board, who names its
// generation, or by its asker or the CFO, who name none. It comes back under a
// new generation so the card he closed never saves into it, for the rest of
// its day, and the goblin that asked is told to wait on it again, as the CFO
// is of his own press.
func (s *Service) reopenCredential(id, generation, by string) error {
	s.credentialSaves.Lock()
	defer s.credentialSaves.Unlock()
	request, found := s.Store.credential(id)
	switch {
	case !found || generation != "" && generation != request.Generation:
		return credentialRefusal{status: http.StatusConflict, Message: "This card is not on the board. Refresh the board."}
	case !slices.Contains([]string{"done", "answered", "dismissed"}, request.State):
		return credentialRefusal{status: http.StatusConflict, Message: "This card is " + request.State + ", so it does not come back."}
	case !time.Now().Before(request.ExpiresAt):
		return credentialRefusal{status: http.StatusConflict, Message: "This card expired. File a new one."}
	}
	var fresh [16]byte
	if _, err := rand.Read(fresh[:]); err != nil {
		return err
	}
	reopened, err := s.Store.updateCredential(id, func(r *CredentialRequest) {
		r.State, r.ClosedAt, r.Reason, r.WindowState, r.Generation = "open", nil, "", "", hex.EncodeToString(fresh[:])
	})
	if err != nil {
		return err
	}
	s.notify()
	if by == "overlord" {
		whose := "the CFO's card " + reopened.ID
		if reopened.Task != "" {
			whose = reopened.Task + "'s card " + reopened.ID
		}
		wait := "Wait on it again with cfo auth request --wait-on " + reopened.ID + "."
		if reopened.By == "goblin" {
			wait = reopened.Task + " is told to wait on it again."
		}
		s.credentialNotice(reopened, "the Overlord brought back "+whose+" ("+reopened.Why+") from History, so it waits on him again. "+wait)
	}
	if reopened.By == "goblin" && s.Options.CFO != nil {
		s.credentialWork.Add(1)
		go func() {
			defer s.credentialWork.Done()
			if _, err := s.Options.CFO.SendGoblin(context.Background(), reopened.Task, reopened.Identity, "Your card "+reopened.ID+" ("+reopened.Why+") is back in the Command Center. Wait on it again with cfo auth request --wait-on "+reopened.ID+" --task "+reopened.Task+"."); err != nil {
				s.credentialNotice(reopened, "card "+reopened.ID+" is back in the Command Center, but "+reopened.Task+" could not be told: "+bounded(err.Error(), 300))
			}
		}()
	}
	return nil
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
			r.Reason = bounded(reason, maxCredentialAnswer)
		}
	})
	if err != nil {
		return updated, err
	}
	if closed == "done" {
		if err := s.Store.closeWaitsForCard(updated, by); err != nil {
			s.publish(err)
		}
	}
	s.notify()
	s.credentialWork.Add(1)
	go s.afterCredentialClose(updated, by)
	return updated, nil
}

// closeWaitsForCard closes, as answered on it, each wait on the Overlord that
// card's goblin opened while the card was open: the wait asked him for the
// card's step, which its Done settled. A wait opened before the card, or on a
// page, is a card of its own and stays.
func (s *Store) closeWaitsForCard(card CredentialRequest, by string) error {
	if card.Task == "" {
		return nil
	}
	reason := "You pressed Done on its step card " + card.ID + "."
	if by != "overlord" {
		reason = "Its step card " + card.ID + " closed as done: the goblin saw the step done."
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	isClosed := false
	now := time.Now().UTC()
	for i := range s.db.Reviews {
		r := &s.db.Reviews[i]
		if r.State == "open" && r.Task == card.Task && strings.HasPrefix(r.ID, "waiting-"+card.Task+"-") && r.LavishPage == "" && !r.CreatedAt.Before(card.CreatedAt) {
			r.State, r.AnsweredBy, r.AnsweredIn, r.Reason, r.UpdatedAt = "answered", by, "step card", reason, now
			isClosed = true
		}
	}
	if !isClosed {
		return nil
	}
	return s.save()
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
	case request.State == "answered":
		told := ""
		if request.By == "goblin" {
			told = "; the goblin was told"
			if s.Options.CFO == nil {
				told = "; the goblin could not be told: message transport is unavailable"
			} else if _, err := s.Options.CFO.SendGoblin(context.Background(), request.Task, request.Identity, "The Overlord answered your card "+request.ID+" ("+request.Why+") instead of taking its step: "+request.Reason); err != nil && !errors.Is(err, fleet.ErrQueuedForToolCall) {
				told = "; the goblin could not be told: " + bounded(err.Error(), 300)
			}
		}
		s.credentialNotice(request, "the Overlord answered "+whose+" instead of taking its step: "+request.Reason+told)
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
		Text       string `json:"text"`
	}
	if err := decodeBody(w, r, &input, 16<<10); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	closed := map[string]string{"done": "done", "dismiss": "dismissed", "answer": "answered"}[input.Action]
	if closed == "" || input.Generation == "" || (closed == "answered") != (input.Text != "") {
		apiError(w, http.StatusBadRequest, "Send the card's ID, its generation, and done, dismiss, or answer with its text.")
		return
	}
	// His word reaches the goblin's conversation, so words shaped like a key
	// are refused and never repeated.
	if text := strings.TrimSpace(input.Text); closed == "answered" && (text == "" || len(text) > maxCredentialAnswer || slices.ContainsFunc(strings.Fields(text), func(word string) bool {
		return auth.IsKeyValue(word) || auth.SecretShape(word) != "" && !strings.Contains(word, "=")
	})) {
		apiError(w, http.StatusBadRequest, "Your answer goes to the goblin, so it is a few words of at most 4000 characters and holds no key. Nothing was sent.")
		return
	}
	request, err := h.Service.closeCredential(input.ID, input.Generation, closed, "overlord", strings.TrimSpace(input.Text))
	respondCredential(w, struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}{request.ID, request.State}, err)
}

// reopenCredentialCard takes his pencil on a card in History, from any
// board.
func (h *HTTP) reopenCredentialCard(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID         string `json:"id"`
		Generation string `json:"generation"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.ID == "" || input.Generation == "" {
		apiError(w, http.StatusBadRequest, "Send the card's ID and its generation.")
		return
	}
	err := h.Service.reopenCredential(input.ID, input.Generation, "overlord")
	respondCredential(w, struct {
		ID string `json:"id"`
	}{input.ID}, err)
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
	credentials, err := readCredentials(stateDir)
	if err != nil {
		return CredentialRequest{}, false, err
	}
	i := slices.IndexFunc(credentials, func(r CredentialRequest) bool { return r.ID == id })
	if i < 0 {
		return CredentialRequest{}, false, nil
	}
	return credentials[i], true, nil
}

// readCredentials reads every card on the board's record.
func readCredentials(stateDir string) ([]CredentialRequest, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, ".supervisor.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Join(errStateBusy, err)
	}
	if len(data) > maxStateBytes {
		return nil, errors.New("supervisor state exceeds its bound")
	}
	var record struct {
		Credentials []CredentialRequest `json:"credentials"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, errors.Join(errStateBusy, err)
	}
	return record.Credentials, nil
}

// OpenStepCard names the open step card of goblin task, on the board's
// record or waiting in its inbox for the board to take it, or is empty when
// it has none. It only reads, as WaitCredential does.
func OpenStepCard(stateDir, task string) (string, error) {
	credentials, err := readCredentials(stateDir)
	if err != nil {
		return "", err
	}
	if i := slices.IndexFunc(credentials, func(r CredentialRequest) bool { return r.Task == task && r.State == "open" }); i >= 0 {
		return credentials[i].ID, nil
	}
	dir := filepath.Join(stateDir, credentialInbox)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, entry := range entries[:min(len(entries), maxCredentialRequests)] {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := fsx.ReadFile(filepath.Join(dir, entry.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			// The board took it meanwhile, and its record has it from now.
			continue
		}
		if err != nil {
			return "", err
		}
		var spooled CredentialRequest
		if json.Unmarshal(data, &spooled) == nil && spooled.Task == task {
			return spooled.ID, nil
		}
	}
	return "", nil
}

// credentialSpooled reports whether card id waits in the inbox for the
// supervisor to take it.
func credentialSpooled(stateDir, id string) bool {
	sum := sha256.Sum256([]byte(id))
	_, err := os.Stat(filepath.Join(stateDir, credentialInbox, hex.EncodeToString(sum[:])+".json"))
	return err == nil
}
