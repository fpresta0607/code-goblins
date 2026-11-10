package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// maxScopeLog is how much of a step's log, from its end, names the files a
// correction may touch: a failure is reported last, after the output of the
// checks that passed.
const maxScopeLog = 8 << 20

// Handback is a run parked at a failed Test, Lint or CI step that its gate
// hands back to the goblin that owns the branch, since the task's policy lets
// the gate repair none of it. The run waits at that step with the step's
// findings until a goblin's submitted correction stops it.
type Handback struct {
	RunID         string
	RepoID        string
	Branch        string
	Head          string
	SubmittedHead string
	Step          string
	Findings      []HandbackFinding
	// Log is the step's own log, which names what failed.
	Log string
}

// HandbackFinding is one open finding of a handed-back step.
type HandbackFinding struct {
	ID          string
	Severity    string
	Description string
}

// HandedBackFindings are the open findings, every one but a no-op, of a step
// parked at a failure that policy hands back to the goblin that owns the
// branch, read from the step's findings document. There are none when policy
// hands the step back to nobody, when the step recorded no findings, or when
// each open one is test-command-no-turn: a test command given no turn is an
// environment the CFO repairs before the command runs again, not code to fix.
func (p Policy) HandedBackFindings(step, document string) ([]HandbackFinding, error) {
	if !p.HandsBack(step) || document == "" {
		return nil, nil
	}
	var report struct {
		Findings []finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(document), &report); err != nil {
		return nil, err
	}
	var open []HandbackFinding
	for _, f := range report.Findings {
		if f.Action != "no-op" {
			open = append(open, HandbackFinding{ID: f.ID, Severity: f.Severity, Description: f.Description})
		}
	}
	if !slices.ContainsFunc(open, func(f HandbackFinding) bool { return f.ID != "test-command-no-turn" }) {
		return nil, nil
	}
	return open, nil
}

// FindingIDs are the ids of the step's open findings, in their order.
func (run Handback) FindingIDs() []string {
	ids := make([]string, 0, len(run.Findings))
	for _, finding := range run.Findings {
		ids = append(ids, finding.ID)
	}
	return ids
}

// HandedBack reads the latest run of branch and reports it while it is still
// running, parked at a failed step whose findings policy hands back. A run
// that stopped, by whoever stopped it, or one whose policy hands nothing
// back, is reported as none.
func (reader Reader) HandedBack(ctx context.Context, project, branch string, policy Policy) (Handback, bool, error) {
	var rows []struct {
		RunID         string `json:"run_id"`
		RepoID        string `json:"repo_id"`
		Branch        string `json:"branch"`
		Status        string `json:"status"`
		Head          string `json:"head"`
		SubmittedHead string `json:"submitted_head"`
		Step          string `json:"step"`
		Findings      string `json:"findings"`
	}
	query := `SELECT runs.id AS run_id,runs.repo_id,runs.branch,runs.status,runs.head_sha AS head,COALESCE(runs.submitted_head_sha,'') AS submitted_head,s.step_name AS step,COALESCE(s.findings_json,'') AS findings FROM runs JOIN repos ON repos.id=runs.repo_id JOIN step_results s ON s.run_id=runs.id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(` + sqlString(filepath.ToSlash(project)) + `) AND runs.branch=` + sqlString(branch) + ` AND runs.id=(SELECT latest.id FROM runs latest WHERE latest.repo_id=runs.repo_id AND latest.branch=runs.branch ORDER BY latest.created_at DESC,latest.id DESC LIMIT 1) AND s.status IN ('awaiting_approval','awaiting_agent') ORDER BY s.step_order`
	if err := reader.query(ctx, query, &rows); err != nil {
		return Handback{}, false, err
	}
	for _, row := range rows {
		if row.Status != "running" {
			continue
		}
		findings, err := policy.HandedBackFindings(row.Step, row.Findings)
		if err != nil {
			return Handback{}, false, fmt.Errorf("pipeline: run %s's %s findings cannot be read: %w", row.RunID, row.Step, err)
		}
		if len(findings) == 0 {
			continue
		}
		if !gateIdentity.MatchString(row.RunID) || !gateIdentity.MatchString(row.RepoID) || row.Branch != branch || !gateCommit.MatchString(row.Head) {
			return Handback{}, false, errors.New("pipeline: handed-back run identity could not be verified")
		}
		return Handback{RunID: row.RunID, RepoID: row.RepoID, Branch: row.Branch, Head: row.Head, SubmittedHead: row.SubmittedHead, Step: row.Step, Findings: findings, Log: filepath.Join(reader.Root, "logs", row.RunID, row.Step+".log")}, true, nil
	}
	return Handback{}, false, nil
}

// TakeRunHead puts the head a handed-back run failed on into the task's
// branch, so the goblin's correction is made on every commit the run
// validated, its gate's rebase and review fixes included. It moves the branch
// only while it still holds the head the run was submitted from, which the
// run's head carries, and pins the run's head first; it returns where.
func (reader Reader) TakeRunHead(ctx context.Context, worktree string, run Handback) (string, error) {
	head, err := reader.branchHead(ctx, worktree)
	if err != nil {
		return "", err
	}
	if head == run.Head {
		return "", nil
	}
	ref := "refs/heads/archive/handback-" + run.RunID + "-" + run.Head
	if head != run.SubmittedHead {
		return "", fmt.Errorf("pipeline: the branch holds %s, which run %s was not submitted from, so it is left as it is: make the correction on the run's head %s", head, run.RunID, run.Head)
	}
	bare := filepath.Join(reader.Root, "repos", run.RepoID+".git")
	if err := reader.preserveRecoveryHead(ctx, bare, ref, run.Head); err != nil {
		return "", err
	}
	for _, args := range [][]string{{"fetch", "--no-tags", bare, ref + ":" + ref}, {"reset", "--keep", run.Head}} {
		result, err := reader.runGit(ctx, worktree, false, args...)
		if err != nil || result.ExitCode != 0 {
			return "", fmt.Errorf("pipeline: git %s of run %s's head %s failed, so the branch stays at %s: %s", args[0], run.RunID, run.Head, head, strings.TrimSpace(string(result.Stderr)))
		}
	}
	return ref, nil
}

// CheckCorrection is nil when the worktree's HEAD is the goblin's one
// correction of run's handed-back findings: a single commit on the run's
// head, nothing else in the worktree, and no file changed outside the
// findings' scope. That scope is what the branch itself changes from trusted,
// and every file the step's log or its findings name, as isNamed reads them,
// from the worktree or from the run's own gate worktree; a wider correction
// is the CFO's to admit.
func (reader Reader) CheckCorrection(ctx context.Context, worktree, trusted string, run Handback) error {
	status, err := reader.runGit(ctx, worktree, false, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil || status.ExitCode != 0 || strings.TrimSpace(string(status.Stdout)) != "" {
		return errors.New("pipeline: commit the correction, and leave nothing else in the worktree")
	}
	parents, err := reader.runGit(ctx, worktree, false, "rev-list", "--parents", "-n", "1", "HEAD")
	if fields := strings.Fields(string(parents.Stdout)); err != nil || parents.ExitCode != 0 || len(fields) != 2 || fields[1] != run.Head {
		return fmt.Errorf("pipeline: a correction is one commit on run %s's head %s", run.RunID, run.Head)
	}
	base, err := reader.runGit(ctx, worktree, false, "merge-base", trusted, run.Head)
	if err != nil || base.ExitCode != 0 {
		return fmt.Errorf("pipeline: the branch's own change cannot be read against origin's default branch %s; fetch origin, then submit again", trusted)
	}
	changed, err := reader.changedFiles(ctx, worktree, run.Head, "HEAD")
	if err != nil {
		return err
	}
	own, err := reader.changedFiles(ctx, worktree, strings.TrimSpace(string(base.Stdout)), run.Head)
	if err != nil {
		return err
	}
	named, err := run.namedText()
	if err != nil {
		return err
	}
	roots := []string{filepath.ToSlash(worktree), filepath.ToSlash(filepath.Join(reader.Root, "worktrees", run.RepoID, run.RunID))}
	var outside []string
	for _, file := range changed {
		if !slices.Contains(own, file) && !isNamed(named, file, roots, own) {
			outside = append(outside, file)
		}
	}
	if len(outside) != 0 {
		return fmt.Errorf("pipeline: the correction changes %s, which the branch does not change and run %s's %s failure does not name", strings.Join(outside, ", "), run.RunID, run.Step)
	}
	return nil
}

func (reader Reader) changedFiles(ctx context.Context, worktree, from, to string) ([]string, error) {
	result, err := reader.runGit(ctx, worktree, false, "diff", "--name-only", "--no-renames", "-z", from, to)
	if err != nil || result.ExitCode != 0 {
		return nil, fmt.Errorf("pipeline: the files changed from %s to %s cannot be read", from, to)
	}
	var files []string
	for _, file := range strings.Split(string(result.Stdout), "\x00") {
		if file != "" {
			files = append(files, file)
		}
	}
	return files, nil
}

// namedText is what names the files a correction of run may touch: the end
// of its step's log and its findings' descriptions, with every path written
// with forward slashes. A step that left no log is named by its findings.
func (run Handback) namedText() (string, error) {
	var text bytes.Buffer
	for _, finding := range run.Findings {
		text.WriteString(finding.Description + "\n")
	}
	file, err := fsx.Open(run.Log)
	if errors.Is(err, os.ErrNotExist) {
		return strings.ReplaceAll(text.String(), `\`, "/"), nil
	}
	if err != nil {
		return "", fmt.Errorf("pipeline: run %s's %s log cannot be read: %w", run.RunID, run.Step, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if _, err := file.Seek(max(0, info.Size()-maxScopeLog), io.SeekStart); err != nil {
		return "", err
	}
	if _, err := io.Copy(&text, file); err != nil {
		return "", fmt.Errorf("pipeline: run %s's %s log cannot be read: %w", run.RunID, run.Step, err)
	}
	return strings.ReplaceAll(text.String(), `\`, "/"), nil
}

// isNamed reports whether text names file, by one of two rules. Its path
// names it where it stands as a path of its own, as a compiler, linter or
// stack trace prints one: after `./` or one of roots, the absolute worktrees
// the failure ran in, or, for a path with a directory, after the start of the
// text, whitespace, a quote or an opening parenthesis; and before the end of
// the text, whitespace, a colon, a quote, a parenthesis or a comma. Its base
// name before a colon or an opening parenthesis, at the start of the text or
// after whitespace, as a Go test failure prints it, names it only when own,
// the files the branch itself changes, holds one in the same directory.
func isNamed(text, file string, roots, own []string) bool {
	isOpen := func(before string) bool {
		return before == "" || strings.ContainsRune(" \t\r\n\"'`(", rune(before[len(before)-1]))
	}
	isAnchored := func(before string) bool {
		return strings.HasSuffix(before, "./") && isOpen(before[:len(before)-2]) || slices.ContainsFunc(roots, func(root string) bool {
			root += "/"
			return len(before) >= len(root) && strings.EqualFold(before[len(before)-len(root):], root)
		})
	}
	isPathStart := isAnchored
	if strings.Contains(file, "/") {
		isPathStart = func(before string) bool { return isOpen(before) || isAnchored(before) }
	}
	isClosed := func(after string) bool {
		return after == "" || strings.ContainsRune(" \t\r\n:\"'`(),", rune(after[0]))
	}
	if occurs(text, file, isPathStart, isClosed) {
		return true
	}
	dir := path.Dir(file)
	if !slices.ContainsFunc(own, func(changed string) bool { return path.Dir(changed) == dir }) {
		return false
	}
	isSpaced := func(before string) bool {
		return before == "" || strings.ContainsRune(" \t\r\n", rune(before[len(before)-1]))
	}
	isLocated := func(after string) bool {
		return after != "" && (after[0] == ':' || after[0] == '(')
	}
	return occurs(text, path.Base(file), isSpaced, isLocated)
}

// occurs reports whether text holds needle anywhere isBefore accepts what
// precedes it and isAfter accepts what follows it.
func occurs(text, needle string, isBefore, isAfter func(string) bool) bool {
	for start := 0; ; start++ {
		i := strings.Index(text[start:], needle)
		if i < 0 {
			return false
		}
		start += i
		if isBefore(text[:start]) && isAfter(text[start+len(needle):]) {
			return true
		}
	}
}

// OwnerAdmission stops run, parked at the handed-back findings the goblin
// corrected, and writes into dir the admission that submits the correction to
// the run's own validation family. It binds the run's original launch, intent
// and agents and the task's frozen policy, so no allowance the run spent is
// renewed, with trusted as today's trusted commit, and returns the file.
func (reader Reader) OwnerAdmission(ctx context.Context, worktree, trusted, dir string, run Handback, selection Selection, env []string) (string, error) {
	result, err := reader.Commands.Run(ctx, execx.Request{Dir: worktree, Env: env, Name: "no-mistakes", Args: []string{"axi", "abort", "--run", run.RunID}})
	if err != nil || result.ExitCode != 0 {
		return "", fmt.Errorf("pipeline: stopping run %s for its correction failed, and nothing was submitted: %w", run.RunID, errors.Join(err, errors.New(strings.TrimSpace(string(result.Stderr)))))
	}
	result, err = reader.Commands.Run(ctx, execx.Request{Dir: worktree, Env: env, Name: "no-mistakes", Args: []string{"axi", "terminal-preflight", "--predecessor", run.RunID}})
	if err != nil || result.ExitCode != 0 {
		return "", fmt.Errorf("pipeline: native terminal-preflight of run %s failed, and nothing was submitted: %w", run.RunID, errors.Join(err, errors.New(strings.TrimSpace(string(result.Stdout)+string(result.Stderr)))))
	}
	fields, err := documentFields(result.Stdout)
	if err != nil {
		return "", err
	}
	switch {
	case fields["predecessor_id"] != run.RunID || fields["predecessor_status"] != "cancelled":
		return "", fmt.Errorf("pipeline: run %s is %q, not stopped for its correction, and nothing was submitted", run.RunID, fields["predecessor_status"])
	case fields["has_competing_run"] != "false":
		return "", fmt.Errorf("pipeline: another run owns %s, so run %s's correction was not submitted", run.Branch, run.RunID)
	case fields["caller_is_clean"] != "true":
		return "", errors.New("pipeline: the worktree changed while the correction was submitted, and nothing was submitted")
	}
	var assertion map[string]json.RawMessage
	if err := json.Unmarshal([]byte(fields["predecessor_launch_assertion_json"]), &assertion); err != nil || assertion == nil {
		return "", fmt.Errorf("pipeline: run %s carries no launch selection to submit its correction under", run.RunID)
	}
	if assertion["trusted_sha"], err = json.Marshal(trusted); err != nil {
		return "", err
	}
	caller, err := fsx.Canonical(worktree)
	if err != nil {
		return "", err
	}
	request := admission{PredecessorID: run.RunID, SnapshotDigest: fields["snapshot_digest"], RepoID: run.RepoID, Branch: run.Branch, CallerDir: caller, CandidateSHA: fields["candidate_sha"], CandidateTree: fields["candidate_tree"], LaunchNonce: fields["predecessor_launch_nonce"], ValidationGeneration: fields["predecessor_validation_generation"], IntentDigest: fields["intent_digest"], PublishedHeadSHA: fields["published_head_sha"], PushTargetFingerprint: fields["push_target_fingerprint"], HistoricalSourceCompleteness: "unknown"}
	if generation := fields["push_generation"]; generation != "" {
		if request.PushGeneration, err = strconv.ParseInt(generation, 10, 64); err != nil {
			return "", fmt.Errorf("pipeline: run %s's push generation %q cannot be read", run.RunID, generation)
		}
	}
	request.ControllerPolicy.SHA256, request.ControllerPolicy.Class, request.ControllerPolicy.ReviewCycles = selection.Hash, selection.Class, selection.ReviewCycles
	if request.LaunchAssertion, err = json.Marshal(assertion); err != nil {
		return "", err
	}
	request.Reason = fmt.Sprintf("The goblin that owns %s corrected run %s's %s findings %s, which pipeline policy version %d hands back to it, in one commit; nothing else changed.", run.Branch, run.RunID, run.Step, strings.Join(run.FindingIDs(), ","), selection.Policy.Version)
	data, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	file := filepath.Join(dir, "owner-admission-"+run.RunID+".json")
	if err := fsx.AtomicWriteFile(file, data); err != nil {
		return "", err
	}
	return file, nil
}

// documentFields reads the top-level scalar fields of a document native AXI
// prints: one "key: value" line each, a quoted value unquoted and null read
// as empty. Nested values, which are indented, are left out.
func documentFields(document []byte) (map[string]string, error) {
	fields := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(document), "\r\n", "\n"), "\n") {
		key, value, isField := strings.Cut(line, ": ")
		if !isField || line[0] == ' ' {
			continue
		}
		switch {
		case value == "null":
			value = ""
		case strings.HasPrefix(value, `"`):
			unquoted, err := strconv.Unquote(value)
			if err != nil {
				return nil, fmt.Errorf("pipeline: native field %s cannot be read: %w", key, err)
			}
			value = unquoted
		}
		fields[key] = value
	}
	return fields, nil
}
