package axi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
)

// Lavish invokes lavish-axi, the Overlord's review surface, to open a page
// and to wait for his feedback on it. Its output is TOON: only the session
// fields cfo acts on are read, and a poll's whole output is kept for the CFO.
type Lavish struct {
	Commands execx.Runner
}

// PagePoll is one poll of a page.
type PagePoll struct {
	// Status is the session status lavish-axi reported: feedback, ended,
	// browser_disconnected, or waiting when the timeout passed first.
	Status string
	// Ended says the Overlord ended the session along with this feedback.
	Ended bool
	// EndedBy says who ended an ended session: user for the Overlord on the
	// page, agent for an agent that ran lavish-axi end.
	EndedBy string
	// Prompts is what the Overlord wrote on the page, one entry per prompt,
	// his changes of an answer aside.
	Prompts []string
	// Notes are those prompts, in order, each with what it is attached to.
	Notes []PageNote
	// Picked says a prompt picks an option the page declared in its
	// data-lavish-choices block, which answers the page.
	Picked bool
	// Changes are the answers he changed with a return page's Change, in the
	// order he sent them.
	Changes []PageChange
	// Output is the poll's whole output, which the CFO reads.
	Output string
}

// PageNote is one prompt he sent on a page and what it is attached to: an
// element by its tag, selector and text, with a table cell's row and column,
// or, for a note from the page's message box, the whole page.
type PageNote struct {
	Prompt   string `json:"prompt"`
	Tag      string `json:"tag,omitempty"`
	Selector string `json:"selector,omitempty"`
	Text     string `json:"text,omitempty"`
	Row      string `json:"row,omitempty"`
	Column   string `json:"column,omitempty"`
}

// PageChange is one answer he changed on a page: the question in his History
// and the answer it takes now.
type PageChange struct {
	Question string
	Answer   string
}

// ErrSessionEnded refuses to open a page whose review the Overlord ended,
// which lavish-axi reports as the session's address with the status
// user-ended, exiting 0.
var ErrSessionEnded = errors.New("axi: the page's review session has ended")

// Open opens or resumes the page's review session without opening a browser,
// and returns the page's address. lavish-axi opens again a page whose review
// an agent ended, but not one whose review the Overlord ended: that is
// ErrSessionEnded.
func (l Lavish) Open(ctx context.Context, file string) (string, error) {
	return l.open(ctx, file, "--no-open")
}

// Reopen is Open that also opens again a page whose review the Overlord
// ended, which only his asking for another look warrants.
func (l Lavish) Reopen(ctx context.Context, file string) (string, error) {
	return l.open(ctx, file, "--no-open", "--reopen")
}

func (l Lavish) open(ctx context.Context, file string, flags ...string) (string, error) {
	args := append([]string{file}, flags...)
	req, err := l.request(args...)
	if err != nil {
		return "", err
	}
	result, err := run(ctx, l.Commands, "lavish-axi "+strings.Join(args, " "), req)
	if err != nil {
		return "", err
	}
	output := string(result.Stdout)
	url := sessionField(output, "url")
	if url == "" {
		return "", errors.New("axi: lavish-axi reported no address for " + file)
	}
	if sessionField(output, "status") == "user-ended" {
		return "", fmt.Errorf("%w at %s", ErrSessionEnded, url)
	}
	return url, nil
}

// Poll waits up to timeout for the Overlord's feedback on the page, first
// posting reply, when there is one, to the page's conversation panel.
// Delivery consumes the feedback, so a page must have one poller: a cancelled
// poll ends its whole process tree, leaving no poll behind to take the
// feedback.
func (l Lavish) Poll(ctx context.Context, file, reply string, timeout time.Duration) (PagePoll, error) {
	args := []string{"poll", file}
	if reply != "" {
		args = append(args, "--agent-reply", reply)
	}
	req, err := l.request(append(args, "--timeout-ms", strconv.FormatInt(timeout.Milliseconds(), 10))...)
	if err != nil {
		return PagePoll{}, err
	}
	req.KillTree = true
	result, err := run(ctx, l.Commands, "lavish-axi poll "+file, req)
	if err != nil {
		return PagePoll{}, err
	}
	output := string(result.Stdout)
	status := sessionField(output, "status")
	if status == "" {
		return PagePoll{}, errors.New("axi: lavish-axi poll reported no session status for " + file)
	}
	// A change is no prompt of his for anyone to read: it goes to the
	// question it changes.
	var changes []PageChange
	var written []string
	var notes []PageNote
	isPicked := false
	for _, p := range readPrompts(output) {
		isPicked = isPicked || p.fields["tag"] == "choice"
		prompt, hasPrompt := p.fields["prompt"]
		switch {
		case !hasPrompt:
		case p.fields["tag"] == pageChangeTag:
			changes = append(changes, PageChange{Question: p.fields["text"], Answer: prompt})
		default:
			written = append(written, prompt)
			notes = append(notes, PageNote{Prompt: prompt, Tag: p.fields["tag"], Selector: p.fields["selector"], Text: p.fields["text"], Row: p.target["rowLabel"], Column: p.target["columnLabel"]})
		}
	}
	return PagePoll{Status: status, Ended: sessionField(output, "session_ended") == "true", EndedBy: sessionField(output, "ended_by"), Prompts: written, Notes: notes, Picked: isPicked, Changes: changes, Output: output}, nil
}

// pageChangeTag is the tag a return page's Change sends its answer under.
const pageChangeTag = "answer_change"

// End ends the page's review session as an agent, so the page says the
// review has ended and takes nothing more. Feedback queued before the end
// stays for the next poll to deliver.
func (l Lavish) End(ctx context.Context, file string) error {
	req, err := l.request("end", file)
	if err != nil {
		return err
	}
	_, err = run(ctx, l.Commands, "lavish-axi end "+file, req)
	return err
}

// request runs lavish-axi in the user's profile, never in the caller's
// folder. The first call starts lavish-axi's page server, which outlives it
// in the folder that call ran in, and a goblin's worktree the server sat in
// could not be removed while it ran (#915).
func (l Lavish) request(args ...string) (execx.Request, error) {
	profile, err := os.UserHomeDir()
	if err != nil {
		return execx.Request{}, fmt.Errorf("axi: lavish-axi has no folder to run in: %w", err)
	}
	return execx.Request{Dir: profile, Name: "lavish-axi", Args: args, Env: remoteaccess.LocalScrawlEnvironment(os.Environ())}, nil
}

// PageSession is one review session lavish-axi keeps: its key, which its
// address ends with as /session/<key>, its page, its status (open, feedback or
// ended), who ended an ended one, and how many of the Overlord's prompts wait
// on it undelivered.
type PageSession struct {
	Key     string
	File    string
	Status  string
	EndedBy string
	Pending int
}

// Sessions lists every review session lavish-axi keeps, read from its state
// file where lavish-axi itself keeps it: LAVISH_AXI_STATE_DIR, else
// .lavish-axi in the user's profile. Reading delivers nothing; only a poll
// takes the feedback waiting on a page. No state file is no sessions.
func (l Lavish) Sessions() ([]PageSession, error) {
	dir := os.Getenv("LAVISH_AXI_STATE_DIR")
	if dir == "" {
		profile, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(profile, ".lavish-axi")
	}
	data, err := fsx.ReadFile(filepath.Join(dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var stored struct {
		Sessions map[string]struct {
			File           string            `json:"file"`
			Status         string            `json:"status"`
			EndedBy        string            `json:"ended_by"`
			PendingPrompts int               `json:"pending_prompts"`
			Prompts        []json.RawMessage `json:"prompts"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("axi: lavish-axi's state file is unreadable: %w", err)
	}
	sessions := make([]PageSession, 0, len(stored.Sessions))
	for key, session := range stored.Sessions {
		sessions = append(sessions, PageSession{Key: key, File: session.File, Status: session.Status, EndedBy: session.EndedBy, Pending: max(session.PendingPrompts, len(session.Prompts))})
	}
	slices.SortFunc(sessions, func(a, b PageSession) int { return strings.Compare(a.File, b.File) })
	return sessions, nil
}

// SessionAt finds the review session a Scrawl link names: the one whose key
// the link's address ends with as /session/<key>, on whichever of
// lavish-axi's addresses the link names. found is false for a link to no
// session lavish-axi keeps.
func (l Lavish) SessionAt(link string) (session PageSession, found bool, err error) {
	u, err := url.Parse(link)
	if err != nil {
		return PageSession{}, false, nil
	}
	key, isSession := strings.CutPrefix(u.Path, "/session/")
	if !isSession || key == "" || strings.Contains(key, "/") {
		return PageSession{}, false, nil
	}
	sessions, err := l.Sessions()
	if err != nil {
		return PageSession{}, false, err
	}
	i := slices.IndexFunc(sessions, func(s PageSession) bool { return s.Key == key })
	if i < 0 {
		return PageSession{}, false, nil
	}
	return sessions[i], true, nil
}

// pagePrompt is one entry of a poll's top-level TOON `prompts` array: its own
// fields, such as prompt, tag and text, and its target's, such as a table
// cell's rowLabel and columnLabel.
type pagePrompt struct {
	fields, target map[string]string
}

// readPrompts reads the top-level TOON `prompts` array in either form
// lavish-axi prints it: a table, when every prompt has the same plain fields,
// or a list, when one carries more, such as a table cell's target. Each value
// is unquoted when quoted, and fields nested any deeper are passed over.
func readPrompts(output string) []pagePrompt {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	for i, line := range lines {
		header, isPrompts := strings.CutPrefix(line, "prompts[")
		_, form, isArray := strings.Cut(header, "]")
		if !isPrompts || !isArray {
			continue
		}
		var prompts []pagePrompt
		if names, isTable := strings.CutPrefix(form, "{"); isTable {
			columns := strings.Split(strings.TrimSuffix(names, "}:"), ",")
			for _, line := range lines[i+1:] {
				row, isRow := strings.CutPrefix(line, "  ")
				if !isRow {
					break
				}
				p := pagePrompt{fields: map[string]string{}}
				for column, value := range tableRow(row) {
					if column < len(columns) {
						p.fields[columns[column]] = value
					}
				}
				prompts = append(prompts, p)
			}
			return prompts
		}
		isTarget := false
		for _, line := range lines[i+1:] {
			switch item, isItem := strings.CutPrefix(line, "  - "); {
			case isItem:
				prompts = append(prompts, pagePrompt{fields: map[string]string{}, target: map[string]string{}})
				isTarget = false
				promptField(prompts[len(prompts)-1].fields, item)
			case len(prompts) > 0 && strings.HasPrefix(line, "      "):
				if isTarget {
					promptField(prompts[len(prompts)-1].target, strings.TrimPrefix(line, "      "))
				}
			case len(prompts) > 0 && strings.HasPrefix(line, "    "):
				field := strings.TrimPrefix(line, "    ")
				isTarget = field == "target:"
				promptField(prompts[len(prompts)-1].fields, field)
			default:
				return prompts
			}
		}
		return prompts
	}
	return nil
}

// promptField reads one `name: value` line of a prompt into fields, unquoting
// a quoted value. A line that opens a nested field holds no value of its own.
func promptField(fields map[string]string, line string) {
	name, value, isField := strings.Cut(line, ": ")
	if !isField {
		return
	}
	if unquoted, err := strconv.Unquote(value); err == nil {
		value = unquoted
	}
	fields[name] = value
}

// tableRow splits one row of a comma-delimited TOON table, unquoting each
// quoted value.
func tableRow(row string) []string {
	var values []string
	for row != "" || len(values) == 0 {
		var value string
		if strings.HasPrefix(row, `"`) {
			end := 1
			for end < len(row) && row[end] != '"' {
				if row[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(row) {
				return values
			}
			unquoted, err := strconv.Unquote(row[:end+1])
			if err != nil {
				return values
			}
			value, row = unquoted, strings.TrimPrefix(row[end+1:], ",")
		} else {
			value, row, _ = strings.Cut(row, ",")
		}
		values = append(values, value)
	}
	return values
}

// sessionField reads one scalar field of the top-level TOON `session:`
// object, unquoting a quoted value.
func sessionField(output, name string) string {
	in := false
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if !in {
			in = line == "session:"
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			return ""
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ": ")
		if !ok || key != name {
			continue
		}
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
		return value
	}
	return ""
}
