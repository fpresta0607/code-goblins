package afk

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Reader returns the log's lines for one stretch to a caller that asks again
// and again, as the supervisor does for every snapshot it sends a board. The
// log only grows, so the file is read again only when its size or the time it
// was written has changed since the last read.
type Reader struct {
	mu         sync.Mutex
	read       bool
	session    string
	size       int64
	written    time.Time
	entries    []Entry
	unreadable int
}

// Entries returns what Entries returns for the stretch session.
func (r *Reader) Entries(stateDir, session string) ([]Entry, int, error) {
	info, err := os.Stat(filepath.Join(stateDir, auditFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.read && r.session == session && r.size == info.Size() && r.written.Equal(info.ModTime()) {
		return r.entries, r.unreadable, nil
	}
	entries, unreadable, err := Entries(stateDir, session)
	if err != nil {
		return nil, 0, err
	}
	r.read, r.session, r.size, r.written, r.entries, r.unreadable = true, session, info.Size(), info.ModTime(), entries, unreadable
	return entries, unreadable, nil
}
