package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

const maxQuestions = 128

type Question struct {
	ID          string    `json:"id"`
	Identity    string    `json:"identity"`
	Text        string    `json:"text"`
	Options     []string  `json:"options"`
	Recommended string    `json:"recommended,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	AnswerID    string    `json:"answer_id,omitempty"`
	Answer      string    `json:"answer,omitempty"`
	AnswerKind  string    `json:"answer_kind,omitempty"`
	Status      string    `json:"status"`
	Message     string    `json:"message,omitempty"`
}

func validQuestion(q Question) error {
	if len(q.ID) < 8 || len(q.ID) > 128 || strings.ContainsAny(q.ID, "\x00\r\n") || len(q.Identity) != 64 || strings.TrimSpace(q.Text) == "" || len(q.Text) > 4000 || len(q.Options) > 8 || q.CreatedAt.IsZero() || q.CreatedAt.After(time.Now().Add(5*time.Minute)) {
		return errors.New("question requires an ID, registered CFO identity, timestamp, bounded text and at most eight choices")
	}
	seen := map[string]bool{}
	for _, option := range q.Options {
		if strings.TrimSpace(option) == "" || len(option) > 500 || seen[option] {
			return errors.New("question choices must be distinct, nonempty and bounded")
		}
		seen[option] = true
	}
	if q.Recommended != "" && !slices.Contains(q.Options, q.Recommended) {
		return errors.New("recommendation must name one of the supplied choices exactly")
	}
	return nil
}

// PublishQuestion is deliberately a local CFO operation, not a browser or
// worker-alert endpoint. The caller must descend from the registered primary
// process, including its creation time, and its native identity must be live.
func (c *CFOConnection) PublishQuestion(ctx context.Context, id, text string, options []string, recommended string) error {
	file, err := openPrimary(filepath.Join(c.State, "primary.json"))
	if err != nil {
		return errors.New("primary CFO registration is unavailable")
	}
	defer file.Close()
	p, identity, err := decodePrimary(file)
	if err != nil {
		return err
	}
	entries, err := proc.Ancestry(os.Getpid(), 32)
	if err != nil {
		return err
	}
	owner := false
	for _, entry := range entries {
		if entry.PID == p.Process.PID && entry.Start.Equal(p.Process.Start) {
			owner = true
		}
	}
	if !owner {
		return errors.New("only the registered CFO process may escalate a question to the user")
	}
	if err := c.verify(ctx, p); err != nil {
		return err
	}
	q := Question{ID: id, Identity: identity, Text: text, Options: options, Recommended: recommended, CreatedAt: time.Now().UTC(), Status: "pending"}
	if err := validQuestion(q); err != nil {
		return err
	}
	if _, err := lock.AcquireExclusiveNamed(c.State, ".question-publish.lock"); err != nil {
		return err
	}
	defer lock.ReleaseExclusiveNamed(c.State, ".question-publish.lock")
	// Read only. Opening a Store here would perform crash recovery while the
	// supervisor is live and overwrite its single-writer transaction state.
	if info, err := os.Stat(filepath.Join(c.State, ".supervisor.json")); err == nil {
		if info.Size() > maxStateBytes {
			return errors.New("supervisor state exceeds its bound")
		}
		data, err := os.ReadFile(filepath.Join(c.State, ".supervisor.json"))
		if err != nil {
			return err
		}
		var db Database
		if err := json.Unmarshal(data, &db); err != nil {
			return errors.New("supervisor question history is unreadable")
		}
		for _, prior := range db.Questions {
			if prior.ID == id {
				if prior.Identity != identity || prior.Text != text || !slices.Equal(prior.Options, options) || prior.Recommended != recommended {
					return errors.New("question ID already used")
				}
				return nil
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir := filepath.Join(c.State, "questions-inbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	entriesOnDisk, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(id))
	path := filepath.Join(dir, hex.EncodeToString(sum[:])+".json")
	if data, err := os.ReadFile(path); err == nil {
		var prior Question
		if json.Unmarshal(data, &prior) != nil || prior.Identity != identity || prior.Text != text || !slices.Equal(prior.Options, options) || prior.Recommended != recommended {
			return errors.New("question ID already used")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(entriesOnDisk) >= maxQuestions {
		return errors.New("user question inbox is full")
	}
	data, err := json.Marshal(q)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, data)
}

func (s *Store) acceptQuestion(q Question) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validQuestion(q); err != nil {
		return err
	}
	for _, prior := range s.db.Questions {
		if prior.ID == q.ID {
			if prior.Identity != q.Identity || prior.Text != q.Text || !slices.Equal(prior.Options, q.Options) || prior.Recommended != q.Recommended {
				return errors.New("question ID already used")
			}
			return nil
		}
	}
	if !s.db.QuestionFloor.IsZero() && !q.CreatedAt.After(s.db.QuestionFloor) {
		return nil
	}
	if len(s.db.Questions) >= maxQuestions {
		index := slices.IndexFunc(s.db.Questions, func(old Question) bool {
			return old.Status == "succeeded" || old.Status == "failed" || old.Status == "superseded"
		})
		if index < 0 {
			return ErrDeferred
		}
		if old := s.db.Questions[index]; old.CreatedAt.After(s.db.QuestionFloor) {
			// An older pending publication may outlive a newer answer. Never
			// move the replay cutoff past this admission or its timestamp peers.
			floor := old.CreatedAt
			if !floor.Before(q.CreatedAt) {
				floor = q.CreatedAt.Add(-time.Nanosecond)
			}
			if floor.After(s.db.QuestionFloor) {
				s.db.QuestionFloor = floor
			}
		}
		s.db.Questions = slices.Delete(s.db.Questions, index, index+1)
	}
	q.Status, q.AnswerID, q.Message = "pending", "", ""
	q.Answer, q.AnswerKind = "", ""
	q.Options = slices.Clone(q.Options)
	s.db.Questions = append(s.db.Questions, q)
	return s.save()
}

func (s *Store) ingestQuestions() error {
	dir := filepath.Join(s.Home.State, "questions-inbox")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type record struct {
		path     string
		question Question
		invalid  error
	}
	var records []record
	for _, entry := range entries[:min(len(entries), maxQuestions)] {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var q Question
		var invalid error
		if info.Size() > 12<<10 {
			invalid = errors.New("question exceeds its size limit")
		} else {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if json.Unmarshal(data, &q) != nil {
				invalid = errors.New("invalid question JSON")
			}
		}
		records = append(records, record{path, q, invalid})
	}
	// Publication caps the inbox at maxQuestions. Hash filenames carry no
	// chronology: admit its oldest records first so capacity deferral cannot
	// strand unseen questions behind a newer retired timestamp.
	slices.SortStableFunc(records, func(a, b record) int {
		return a.question.CreatedAt.Compare(b.question.CreatedAt)
	})
	for _, record := range records {
		q, invalid, path := record.question, record.invalid, record.path
		if invalid == nil {
			invalid = s.acceptQuestion(q)
		}
		if errors.Is(invalid, ErrStorage) {
			return invalid
		}
		if errors.Is(invalid, ErrDeferred) {
			continue
		}
		if invalid != nil {
			// Keep a bounded diagnostic, never raw question data or a poison
			// file that can stop subsequent records from being accepted.
			s.mu.Lock()
			s.db.Issues = append(s.db.Issues, "User question rejected: "+bounded(invalid.Error(), 300))
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

func (s *Store) supersedeQuestions() error {
	file, err := openPrimary(filepath.Join(s.Home.State, "primary.json"))
	if err != nil {
		return nil
	} // Missing evidence cannot establish replacement.
	_, identity, err := decodePrimary(file)
	_ = file.Close()
	if err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for i := range s.db.Questions {
		q := &s.db.Questions[i]
		if q.Status == "pending" && q.Identity != identity {
			q.Status = "superseded"
			q.Message = "The CFO session changed. Ask the current CFO to reissue this question."
			changed = true
		}
	}
	if changed {
		return s.save()
	}
	return nil
}

func (s *Store) questionAnswer(a Action) error {
	for _, q := range s.db.Questions {
		if q.ID != a.QuestionID {
			continue
		}
		if q.Identity != a.Generation {
			return errors.New("this question belongs to a previous CFO session")
		}
		if q.AnswerID != "" || q.Status != "pending" {
			return errors.New("an answer is already recorded for this question; inspect its outcome")
		}
		if a.AnswerKind != "" && a.AnswerKind != "option" && a.AnswerKind != "other" {
			return errors.New("answer kind must be option or other")
		}
		if a.AnswerKind != "other" && (len(q.Options) > 0 || a.AnswerKind == "option") && !slices.Contains(q.Options, a.Text) {
			return errors.New("select one of the CFO's supplied choices")
		}
		return nil
	}
	return errors.New("this user question is unavailable")
}
