package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A review asks the CFO to direct work without borrowing pipeline custody.
func (s *Service) deliverReview(ctx context.Context, meta state.TaskMeta, a Action) (Evaluation, error) {
	diff, err := s.previewGit(meta).Diff(ctx, meta.Worktree, a.Revision, a.File)
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: %v; refresh Changes", ErrRejected, err)
	}
	if a.Head == "" || a.Head != diff.Head || a.DiffID == "" || a.DiffID != diff.Fingerprint || a.Revision != diff.Revision {
		return Evaluation{}, fmt.Errorf("%w: HEAD or diff changed; refresh Changes and reselect the range", ErrRejected)
	}
	code, err := diffRangeContext(diff.Patch, a.Line, a.EndLine, a.Side)
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	payload := struct {
		ID         string `json:"id"`
		TaskID     string `json:"task_id"`
		Generation string `json:"generation"`
		File       string `json:"file"`
		Line       int    `json:"line"`
		EndLine    int    `json:"end_line"`
		Side       string `json:"side"`
		Head       string `json:"head"`
		Revision   string `json:"revision"`
		DiffID     string `json:"diff_id"`
		Text       string `json:"text"`
		Code       string `json:"code"`
	}{a.ID, a.TaskID, a.Generation, a.File, a.Line, a.EndLine, a.Side, a.Head, a.Revision, a.DiffID, a.Text, code}
	detail, err := json.Marshal(payload)
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: invalid review context", ErrRejected)
	}
	if a.CFOIdentity == "" || s.Options.CFO == nil {
		return Evaluation{}, fmt.Errorf("%w: this review has no pinned CFO recipient; refresh and submit a new comment", ErrRejected)
	}
	// Running intent and the recipient fingerprint are already durable. Native
	// acceptance is the only delivery transport; no second actionable wake is
	// appended. A crash or unknown send remains uncertain and is never replayed.
	return s.Options.CFO.Send(ctx, a.CFOIdentity, "Review request for CFO: "+string(detail))
}

func diffRangeContext(patch string, first, last int, side string) (string, error) {
	if first < 1 || last < first || last-first >= 200 || (side != "old" && side != "new") {
		return "", errors.New("select 1 to 200 contiguous old or new lines")
	}
	old, next, found := 0, 0, 0
	inHunk := false
	var code strings.Builder
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				return "", errors.New("invalid diff header; refresh Changes")
			}
			if _, err := fmt.Sscanf(strings.Split(fields[1], ",")[0], "-%d", &old); err != nil {
				return "", err
			}
			if _, err := fmt.Sscanf(strings.Split(fields[2], ",")[0], "+%d", &next); err != nil {
				return "", err
			}
			inHunk = true
			continue
		}
		if !inHunk || line == "" {
			continue
		}
		coordinate := 0
		switch line[0] {
		case '+':
			if side == "new" {
				coordinate = next
			}
			next++
		case '-':
			if side == "old" {
				coordinate = old
			}
			old++
		case ' ':
			if side == "old" {
				coordinate = old
			} else {
				coordinate = next
			}
			old++
			next++
		}
		if coordinate < first || coordinate > last {
			continue
		}
		if coordinate != first+found {
			return "", errors.New("selection crosses unavailable lines; refresh Changes and reselect")
		}
		found++
		if code.Len() < 8192 {
			code.WriteString(bounded(line[1:]+"\n", 8192-code.Len()))
		}
	}
	if found != last-first+1 {
		return "", errors.New("select a contiguous visible diff range; refresh Changes if stale")
	}
	return code.String(), nil
}
