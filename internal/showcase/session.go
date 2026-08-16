package showcase

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// StateDirName is the directory beside an artifact that holds its review
// session state.
const StateDirName = ".showcase"

// ErrEndedByUser reports that the reviewer ended the session from the
// browser; reopening it uninvited needs an explicit --reopen.
var ErrEndedByUser = errors.New("showcase: session was ended by the user")

// Feedback is one queued prompt from the reviewer: a freeform message, an
// element annotation, or a comment on selected text.
type Feedback struct {
	ID        int       `json:"id"`
	Type      string    `json:"type"` // "message", "annotation", or "selection"
	Text      string    `json:"text"`
	Quote     string    `json:"quote,omitempty"`
	Selector  string    `json:"selector,omitempty"`
	Context   string    `json:"context,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Delivered bool      `json:"delivered,omitempty"`
}

// Message is one conversation-panel entry from either side.
type Message struct {
	Role      string    `json:"role"` // "user" or "agent"
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// Session is the persisted review state for one artifact.
type Session struct {
	Version     int        `json:"version"`
	ID          string     `json:"id"`
	Artifact    string     `json:"artifact"`
	Kind        Kind       `json:"kind"`
	CreatedAt   time.Time  `json:"created_at"`
	EndedBy     string     `json:"ended_by,omitempty"` // "user" or "agent"
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	EndReported bool       `json:"end_reported,omitempty"`
	Feedback    []Feedback `json:"feedback,omitempty"`
	Messages    []Message  `json:"messages,omitempty"`
}

// Payload is the JSON document poll prints when it delivers feedback or
// reports that the session ended.
type Payload struct {
	Session  string     `json:"session"`
	Artifact string     `json:"artifact"`
	Kind     Kind       `json:"kind"`
	Ended    bool       `json:"ended"`
	EndedBy  string     `json:"ended_by,omitempty"`
	Feedback []Feedback `json:"feedback"`
}

// SessionID derives a stable session id from the artifact's absolute path.
func SessionID(artifact string) string {
	normalized := strings.ToLower(filepath.ToSlash(artifact))
	sum := sha1.Sum([]byte(normalized))
	return hex.EncodeToString(sum[:])[:12]
}

// StatePath returns the session file for an artifact: a JSON document in a
// .showcase directory beside the artifact. Artifacts that already live in a
// .showcase directory keep their session file beside them.
func StatePath(artifact string) string {
	dir := filepath.Dir(artifact)
	if filepath.Base(dir) != StateDirName {
		dir = filepath.Join(dir, StateDirName)
	}
	return filepath.Join(dir, filepath.Base(artifact)+".session.json")
}

// Load reads the session file for an artifact. A missing file returns an
// error satisfying errors.Is(err, fs.ErrNotExist).
func Load(artifact string) (*Session, error) {
	data, err := os.ReadFile(StatePath(artifact))
	if err != nil {
		return nil, err
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("showcase: corrupt session file: %w", err)
	}
	return &session, nil
}

// mutate serializes read-modify-write access to a session file across the
// server, poll, and end processes. The lock file spins briefly and breaks a
// stale lock left by a crashed process.
func mutate(artifact string, fn func(*Session) error) error {
	path := StatePath(artifact)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lock := path + ".lock"
	deadline := time.Now().Add(15 * time.Second)
	for {
		handle, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			handle.Close()
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > 10*time.Second {
			os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("showcase: timed out waiting for %s", lock)
		}
		time.Sleep(25 * time.Millisecond)
	}
	defer os.Remove(lock)

	session, err := Load(artifact)
	if errors.Is(err, fs.ErrNotExist) {
		session = &Session{
			Version:  1,
			ID:       SessionID(artifact),
			Artifact: artifact,
		}
		err = nil
	}
	if err != nil {
		return err
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now()
	}
	if err := fn(session); err != nil {
		return err
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, data)
}

// Open creates or resumes the session for an artifact. A session the
// reviewer ended from the browser refuses to resume unless reopen is set;
// an agent-ended session resumes freely.
func Open(artifact string, kind Kind, reopen bool) (*Session, error) {
	var opened *Session
	err := mutate(artifact, func(session *Session) error {
		if session.EndedBy == "user" && !reopen {
			return ErrEndedByUser
		}
		session.EndedBy = ""
		session.EndedAt = nil
		session.EndReported = false
		session.Kind = kind
		session.Artifact = artifact
		opened = session
		return nil
	})
	return opened, err
}

// AppendFeedback queues one reviewer prompt. The queue is full fidelity:
// items survive restarts until poll consumes them. Appending to a session
// the reviewer ended is refused.
func AppendFeedback(artifact string, feedback Feedback) error {
	return mutate(artifact, func(session *Session) error {
		if session.EndedBy == "user" {
			return ErrEndedByUser
		}
		maxID := 0
		for _, item := range session.Feedback {
			if item.ID > maxID {
				maxID = item.ID
			}
		}
		feedback.ID = maxID + 1
		feedback.CreatedAt = time.Now()
		feedback.Delivered = false
		session.Feedback = append(session.Feedback, feedback)
		return nil
	})
}

// AppendMessage adds one conversation-panel entry from role ("user" or
// "agent").
func AppendMessage(artifact, role, text string) error {
	return mutate(artifact, func(session *Session) error {
		session.Messages = append(session.Messages, Message{
			Role:      role,
			Text:      text,
			CreatedAt: time.Now(),
		})
		return nil
	})
}

// End marks the session ended by "user" (browser) or "agent" (CLI).
func End(artifact, by string) error {
	return mutate(artifact, func(session *Session) error {
		now := time.Now()
		session.EndedBy = by
		session.EndedAt = &now
		return nil
	})
}

// Consume returns the deliverable payload for poll, if any: undelivered
// feedback, or the ended state exactly once. Delivered feedback is marked so
// a later poll never repeats it.
func Consume(artifact string) (*Payload, bool, error) {
	var payload *Payload
	delivered := false
	err := mutate(artifact, func(session *Session) error {
		payload = &Payload{
			Session:  session.ID,
			Artifact: session.Artifact,
			Kind:     session.Kind,
			Feedback: []Feedback{},
		}
		var pending []Feedback
		for i, item := range session.Feedback {
			if !item.Delivered {
				pending = append(pending, item)
				session.Feedback[i].Delivered = true
			}
		}
		if len(pending) > 0 {
			payload.Feedback = pending
			payload.Ended = session.EndedBy != ""
			payload.EndedBy = session.EndedBy
			if payload.Ended {
				session.EndReported = true
			}
			delivered = true
			return nil
		}
		if session.EndedBy != "" && !session.EndReported {
			payload.Ended = true
			payload.EndedBy = session.EndedBy
			session.EndReported = true
			delivered = true
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return payload, delivered, nil
}

// readHead reads up to limit bytes from the start of path.
func readHead(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	buffer := make([]byte, limit)
	n, err := file.Read(buffer)
	if err != nil && n == 0 {
		return nil, err
	}
	return buffer[:n], nil
}
