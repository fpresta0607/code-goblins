package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// serviceLog is the service's log file. Once a line would take it past its
// limit, the file is moved to its name with .1 added, over the one before, so
// the log never takes more than twice its limit.
type serviceLog struct {
	mu    sync.Mutex
	path  string
	limit int64
	file  *os.File
	size  int64
}

// openLog opens the log at path, kept within limit, to add to it.
func openLog(path string, limit int64) (*serviceLog, error) {
	l := &serviceLog{path: path, limit: limit}
	return l, l.open()
}

func (l *serviceLog) open() error {
	file, err := fsx.OpenAppend(l.path, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	l.file, l.size = file, info.Size()
	return nil
}

// Say adds line to the log, with the time it was said. A line the log cannot
// take is lost, as the service has nowhere else to say it.
func (l *serviceLog) Say(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := fmt.Sprintf("%s %s\n", time.Now().UTC().Format(time.RFC3339), line)
	if l.size+int64(len(entry)) > l.limit && l.size > 0 {
		l.rotate()
	}
	written, _ := l.file.WriteString(entry)
	l.size += int64(written)
}

// rotate moves the log to its name with .1 added and opens a new one. A move
// that fails, as while another program holds the file, leaves the log where
// it is, to be moved at the next line.
func (l *serviceLog) rotate() {
	if l.file.Close() != nil {
		return
	}
	_ = os.Rename(l.path, l.path+".1")
	_ = l.open()
}

// Close closes the log.
func (l *serviceLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}
