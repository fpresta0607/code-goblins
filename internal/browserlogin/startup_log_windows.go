package browserlogin

import "sync"

// startupLogLimit is how much of the end of Chrome's log a start keeps.
const startupLogLimit = 4 << 10

// startupLog is Chrome's standard error, where it logs, while its start
// runs: it keeps the last startupLogLimit bytes until the start ends and
// drops everything after, so what a page logs once the browser is in use is
// never kept.
type startupLog struct {
	mu     sync.Mutex
	tail   []byte
	isDone bool
}

func (l *startupLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.isDone {
		l.tail = append(l.tail, data...)
		if len(l.tail) > startupLogLimit {
			l.tail = l.tail[len(l.tail)-startupLogLimit:]
		}
	}
	return len(data), nil
}

// end stops the keeping and returns what was kept.
func (l *startupLog) end() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := string(l.tail)
	l.tail, l.isDone = nil, true
	return kept
}
