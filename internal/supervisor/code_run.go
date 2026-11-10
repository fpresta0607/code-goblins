package supervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
)

// codeRunRequest names a PowerShell code block in a session's reply by
// where it is, never by its text: the session, as the message view names
// it, the reply's ID, the text block of the reply and the fence in that
// block, counted as the message view counts them.
type codeRunRequest struct {
	Task       string `json:"task"`
	Generation string `json:"generation"`
	CFO        string `json:"cfo"`
	Message    string `json:"message"`
	Block      int    `json:"block"`
	Fence      int    `json:"fence"`
}

// codeRunRead bounds the look back through a session's record for the reply.
const codeRunRead = 8 * time.Second

// codeRun runs a PowerShell code block of a session's reply, the Run beside
// its Copy, in a terminal of its own on a run card, as a run card runs its
// command (#2051). It runs only the text the server reads back from the
// session's own record: a fence the reply wrote and closed, in its own words
// rather than a call's output or his message, that holds PowerShell. The
// browser names where the block is and never its text.
func (h *HTTP) codeRun(w http.ResponseWriter, r *http.Request) {
	var request codeRunRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Message == "" || request.Block < 0 || request.Fence < 0 {
		apiError(w, http.StatusBadRequest, "Name the reply and the code block to run.")
		return
	}
	if h.Service.Options.Runs == nil || h.Service.Options.Tree == nil {
		apiError(w, http.StatusServiceUnavailable, "This board cannot open a terminal. Copy the command instead.")
		return
	}
	query := url.Values{}
	source := "cfo"
	if request.CFO != "" {
		query.Set("cfo", request.CFO)
	} else {
		query.Set("task", request.Task)
		query.Set("generation", request.Generation)
		source = request.Task + " " + request.Generation
	}
	transcript, err := codeRunTranscript(r.Context(), h, query)
	if err != nil {
		var refused messagesRefused
		if errors.As(err, &refused) {
			apiError(w, refused.status, refused.reason)
		} else {
			apiError(w, http.StatusConflict, "That session's record could not be read, so nothing ran.")
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), codeRunRead)
	defer cancel()
	command, err := replyCommand(ctx, h.Service.Options.Tree, transcript, request)
	if err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	who := "the CFO"
	if request.CFO == "" {
		who = request.Task
	}
	source += fmt.Sprintf(" reply %s block %d fence %d", request.Message, request.Block, request.Fence)
	run, err := h.Service.codeRunItem("PowerShell from "+who+"'s reply", source, command)
	if err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	h.Service.notify()
	respond(w, http.StatusOK, struct {
		RunID string `json:"run_id"`
	}{run.ID})
}

func codeRunTranscript(ctx context.Context, h *HTTP, query url.Values) (string, error) {
	locate, err := h.messagesLocator(ctx, query)
	if err != nil {
		return "", err
	}
	located, err := locate()
	if err == nil && located.Path == "" {
		err = messagesRefused{http.StatusConflict, "That session has written no reply yet."}
	}
	return located.Path, err
}

// replyCommand reads back the command the request names from the session's
// record, paging back from its newest messages until it finds the reply.
func replyCommand(ctx context.Context, tree *fleettree.Reader, transcript string, request codeRunRequest) (string, error) {
	before := -1
	for ctx.Err() == nil {
		page, _, err := tree.MessagePageContext(ctx, transcript, before)
		if err != nil {
			return "", errors.New("That session's record could not be read, so nothing ran.")
		}
		if i := slices.IndexFunc(page.Messages, func(m fleettree.Message) bool { return m.ID == request.Message }); i >= 0 {
			return blockCommand(page.Messages[i], request.Block, request.Fence)
		}
		if !page.More || len(page.Messages) == 0 || before >= 0 && page.Messages[0].Seq >= before {
			break
		}
		before = page.Messages[0].Seq
	}
	return "", errors.New("That reply is not in the session's conversation, so nothing ran.")
}

// blockCommand is the command a reply's code block holds: the block must be
// the reply's own words, its fence closed and its code PowerShell.
func blockCommand(message fleettree.Message, block, fence int) (string, error) {
	if message.Kind != fleettree.MessageReply || block >= len(message.Blocks) || message.Blocks[block].Kind != fleettree.BlockText {
		return "", errors.New("Only a code block in a reply's own words runs, so nothing ran.")
	}
	fences := codeFences(message.Blocks[block].Text)
	if fence >= len(fences) || !fences[fence].isClosed {
		return "", errors.New("That reply holds no finished code block there, so nothing ran.")
	}
	command, ok := powerShellCommand(fences[fence].language, fences[fence].text)
	if !ok {
		return "", errors.New("That code block is not a PowerShell command, so nothing ran.")
	}
	return command, nil
}

// fenceOpen is the line that opens a fenced code block, as the message view
// reads it (textParts in frontend/src/session-messages.ts): three or more
// backticks or tildes and the language it names.
var fenceOpen = regexp.MustCompile("^\\s*(`{3,}|~{3,})\\s*([\\w+#.-]*)")

type codeFence struct {
	language, text string
	isClosed       bool
}

// codeFences are the fenced code blocks of a reply's text, in order. A fence
// closes on a line of the same mark at least as long; one left open, as a
// reply cut short leaves it, is not closed.
func codeFences(text string) []codeFence {
	lines := strings.Split(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text), "\n")
	var fences []codeFence
	for at := 0; at < len(lines); at++ {
		open := fenceOpen.FindStringSubmatch(lines[at])
		if open == nil {
			continue
		}
		closing := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(open[1][:1]) + `{` + strconv.Itoa(len(open[1])) + `,}\s*$`)
		var code []string
		for at++; at < len(lines) && !closing.MatchString(lines[at]); at++ {
			code = append(code, lines[at])
		}
		fences = append(fences, codeFence{language: open[2], text: strings.Join(code, "\n"), isClosed: at < len(lines)})
	}
	return fences
}

// leadingPowerShell is an untagged block's command that starts PowerShell.
var leadingPowerShell = regexp.MustCompile(`(?i)^(powershell|pwsh)(\.exe)?(\s|$)`)

// powerShellCommand is the command a code block runs when it is PowerShell:
// its fence names powershell, pwsh or ps1, or, untagged, it starts with
// powershell or pwsh. A leading "! ", the harness's shell-mode marker, is
// dropped.
func powerShellCommand(language, code string) (string, bool) {
	command := strings.TrimPrefix(strings.TrimLeft(code, " \t"), "! ")
	isTagged := slices.Contains([]string{"powershell", "pwsh", "ps1"}, strings.ToLower(language))
	if strings.TrimSpace(command) == "" || !isTagged && (language != "" || !leadingPowerShell.MatchString(command)) {
		return "", false
	}
	return command, true
}

// codeRunItem records the block's command as an item the board made and
// starts it at once, his press being its Run, in a terminal in the Code
// Goblins home folder under Windows PowerShell 5.1. The audit names the
// reply it came from. A block whose earlier run still runs is not run again.
func (s *Service) codeRunItem(title, source, command string) (Run, error) {
	s.runRequests.Lock()
	defer s.runRequests.Unlock()
	if slices.ContainsFunc(s.Store.Snapshot().Runs, func(r Run) bool { return r.Reply == source && (r.State == "ready" || r.State == "running") }) {
		return Run{}, errors.New("That command still runs from this reply; wait for it to end.")
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Run{}, err
	}
	id := "code-run-" + hex.EncodeToString(nonce[:])
	now := time.Now().UTC()
	identity := sha256.Sum256([]byte("code-run\n" + id + "\n" + strconv.FormatInt(now.UnixNano(), 10)))
	run, err := s.recordBoardRun(Run{ID: id, Identity: hex.EncodeToString(identity[:]), Title: title, Shell: "powershell", Command: command, Cwd: s.Store.Home.Root, State: "ready", CreatedAt: now, ExpiresAt: now.Add(runLifetime), Reply: source})
	if err != nil {
		return Run{}, err
	}
	if err := appendRunAudit(s.Store.Home.State, run, "started from "+source, now); err != nil {
		return Run{}, err
	}
	_, err = s.Store.Queue(Action{ID: "run-" + run.ID, Kind: "run", RunID: run.ID, Generation: run.Identity})
	return run, err
}
