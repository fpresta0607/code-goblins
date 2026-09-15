package pipeline

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

type Launch struct {
	Project      string `json:"project"`
	Branch       string `json:"branch"`
	Head         string `json:"head"`
	Nonce        string `json:"nonce"`
	Generation   string `json:"generation"`
	IntentDigest string `json:"intent_digest"`
	PolicyHash   string `json:"policy_hash"`
	RunID        string `json:"run_id"`
}

var ErrNoBoundRun = errors.New("pipeline: native launch has no associated run yet")

type NativeRun struct {
	RunID           string `json:"run_id"`
	Project         string `json:"project"`
	Branch          string `json:"branch"`
	Head            string `json:"head"`
	Nonce           string `json:"nonce"`
	Generation      string `json:"generation"`
	IntentDigest    string `json:"intent_digest"`
	Status          string `json:"status"`
	CustodyReturned int64  `json:"custody_returned"`
	Pushed          string `json:"pushed"`
}

func NewLaunch(project, branch, head, intent, policy string) Launch {
	return Launch{Project: project, Branch: branch, Head: head, Nonce: rand.Text(), Generation: rand.Text(), IntentDigest: fmt.Sprintf("%x", sha256.Sum256([]byte(intent))), PolicyHash: policy}
}

func (l Launch) Verify(r NativeRun) error {
	if (l.RunID != "" && l.RunID != r.RunID) || r.RunID == "" || !fsx.SamePath(l.Project, r.Project) || l.Branch != r.Branch || l.Head != r.Head || l.Nonce != r.Nonce || l.Generation != r.Generation || l.IntentDigest != r.IntentDigest {
		return errors.New("pipeline: native run does not match registered task launch identity")
	}
	return nil
}

func (r Reader) BoundRun(ctx context.Context, l Launch) (NativeRun, error) {
	var rows []NativeRun
	sql := `SELECT runs.id AS run_id,repos.working_path AS project,runs.branch,COALESCE(runs.submitted_head_sha,'') AS head,COALESCE(runs.launch_nonce,'') AS nonce,COALESCE(runs.launch_validation_generation,'') AS generation,COALESCE(runs.launch_intent_digest,'') AS intent_digest,runs.status,COALESCE(runs.custody_returned_at,0) AS custody_returned,COALESCE(runs.last_pushed_sha,'') AS pushed FROM runs JOIN repos ON repos.id=runs.repo_id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(` + sqlString(filepath.ToSlash(l.Project)) + `) AND runs.branch=` + sqlString(l.Branch) + ` AND runs.launch_nonce=` + sqlString(l.Nonce)
	if err := r.query(ctx, sql, &rows); err != nil {
		return NativeRun{}, err
	}
	if len(rows) == 0 {
		return NativeRun{}, ErrNoBoundRun
	}
	if len(rows) != 1 {
		return NativeRun{}, errors.New("pipeline: exact native launch association unavailable; inspect status, do not resubmit with a new identity")
	}
	return rows[0], l.Verify(rows[0])
}

func LoadLaunch(path string) (Launch, error) {
	var l Launch
	data, err := os.ReadFile(path)
	if err != nil {
		return l, err
	}
	if err = json.Unmarshal(data, &l); err != nil {
		return l, err
	}
	if l.Project == "" || l.Branch == "" || l.Head == "" || l.Nonce == "" || l.Generation == "" || l.IntentDigest == "" || l.PolicyHash == "" {
		return l, errors.New("pipeline: incomplete launch association")
	}
	return l, nil
}
func (l Launch) Save(path string) error { return saveJSON(path, l) }

type Budget struct {
	PolicyHash string          `json:"policy_hash"`
	Cap        int             `json:"cap"`
	Responses  map[string]bool `json:"responses"`
}

func LoadBudget(path, policy string, cap int) (Budget, error) {
	b := Budget{PolicyHash: policy, Cap: cap, Responses: map[string]bool{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	if err = json.Unmarshal(data, &b); err != nil {
		return b, err
	}
	if b.PolicyHash != policy || b.Cap != cap || b.Responses == nil || len(b.Responses) > cap {
		return b, errors.New("pipeline: durable task repair budget changed or is invalid")
	}
	for _, reserved := range b.Responses {
		if !reserved {
			return b, errors.New("pipeline: invalid unreserved budget entry")
		}
	}
	return b, nil
}

// Reserve precedes native submission. An uncertain response remains charged;
// retrying the exact parked round does not charge again or reset the task cap.
func (b *Budget) Reserve(path string, g Gate) error {
	key := fmt.Sprintf("%s/%s/%d", g.RunID, g.StepID, g.Round)
	if b.Responses[key] {
		return nil
	}
	if len(b.Responses) >= b.Cap {
		return fmt.Errorf("%w; task repair budget exhausted (%d/%d); retain findings and custody", ErrUnresolved, len(b.Responses), b.Cap)
	}
	b.Responses[key] = true
	return saveJSON(path, b)
}
func (b Budget) Save(path string) error { return saveJSON(path, b) }
func saveJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, append(data, '\n'))
}
