package axi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
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
	// when lavish-axi printed its prompts as a table.
	Prompts []string
	// Output is the poll's whole output, which the CFO reads.
	Output string
}

// Open opens or resumes the page's review session without opening a browser,
// and returns the page's address.
func (l Lavish) Open(ctx context.Context, file string) (string, error) {
	result, err := command(ctx, l.Commands, "lavish-axi "+file+" --no-open", "lavish-axi", file, "--no-open")
	if err != nil {
		return "", err
	}
	url := sessionField(string(result.Stdout), "url")
	if url == "" {
		return "", errors.New("axi: lavish-axi reported no address for " + file)
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
	result, err := run(ctx, l.Commands, "lavish-axi poll "+file, execx.Request{
		Name:     "lavish-axi",
		Args:     append(args, "--timeout-ms", strconv.FormatInt(timeout.Milliseconds(), 10)),
		KillTree: true,
	})
	if err != nil {
		return PagePoll{}, err
	}
	output := string(result.Stdout)
	status := sessionField(output, "status")
	if status == "" {
		return PagePoll{}, errors.New("axi: lavish-axi poll reported no session status for " + file)
	}
	return PagePoll{Status: status, Ended: sessionField(output, "session_ended") == "true", EndedBy: sessionField(output, "ended_by"), Prompts: promptTexts(output), Output: output}, nil
}

// End ends the page's review session as an agent, so the page says the
// review has ended and takes nothing more. Feedback queued before the end
// stays for the next poll to deliver.
func (l Lavish) End(ctx context.Context, file string) error {
	_, err := command(ctx, l.Commands, "lavish-axi end "+file, "lavish-axi", "end", file)
	return err
}

// PageSession is one review session lavish-axi keeps: its page, its status
// (open, feedback or ended), who ended an ended one, and how many of the
// Overlord's prompts wait on it undelivered.
type PageSession struct {
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
	for _, session := range stored.Sessions {
		sessions = append(sessions, PageSession{File: session.File, Status: session.Status, EndedBy: session.EndedBy, Pending: max(session.PendingPrompts, len(session.Prompts))})
	}
	slices.SortFunc(sessions, func(a, b PageSession) int { return strings.Compare(a.File, b.File) })
	return sessions, nil
}

// promptTexts reads the prompt column of the top-level TOON `prompts` table,
// one entry per row, unquoting a quoted value. Prompts printed as a list, or
// a table without that column, read as none.
func promptTexts(output string) []string {
	var texts []string
	column := -1
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if column < 0 {
			header, ok := strings.CutPrefix(line, "prompts[")
			_, fields, found := strings.Cut(header, "]{")
			if ok && found && strings.HasSuffix(fields, "}:") {
				column = slices.Index(strings.Split(strings.TrimSuffix(fields, "}:"), ","), "prompt")
				if column < 0 {
					return nil
				}
			}
			continue
		}
		row, ok := strings.CutPrefix(line, "  ")
		if !ok {
			break
		}
		if values := tableRow(row); column < len(values) {
			texts = append(texts, values[column])
		}
	}
	return texts
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
