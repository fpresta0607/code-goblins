package wake

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// episodeFile is the recovery-generation marker: one line "status:generation"
// (pending:N or acked:N), rewritten atomically under the .wake-queue.lock
// invariant so a watcher's publish never races a drain's ack.
const episodeFile = ".watcher-down"

// Episode is the current recovery-generation marker.
type Episode struct {
	Pending bool
	Gen     int
}

// ErrGenerationMismatch reports that AckEpisode was asked to retire a
// generation other than the one currently pending; callers treat this as a
// signal to re-drain, not a failure.
var ErrGenerationMismatch = errors.New("wake: recovery generation moved")

// readEpisode is ReadEpisode's implementation, reused by PublishEpisode and
// AckEpisode from inside the lock they already hold.
func readEpisode(dir string) (Episode, error) {
	data, err := os.ReadFile(filepath.Join(dir, episodeFile))
	if errors.Is(err, os.ErrNotExist) {
		return Episode{}, nil
	}
	if err != nil {
		return Episode{}, err
	}
	line := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	status, genText, ok := strings.Cut(line, ":")
	if !ok {
		return Episode{}, nil
	}
	gen, err := strconv.Atoi(genText)
	if err != nil {
		return Episode{}, nil
	}
	return Episode{Pending: status == "pending", Gen: gen}, nil
}

// ReadEpisode returns the current recovery-generation marker. A missing
// file, or a line that does not split into exactly two colon-separated
// fields whose second field parses as an integer, both read as a zero
// Episode with a nil error, so a truncated or hand-edited marker degrades
// instead of panicking inside a Stop hook.
func ReadEpisode(dir string) (Episode, error) {
	return readEpisode(dir)
}

func writeEpisode(dir, status string, gen int) error {
	return fsx.AtomicWriteFile(filepath.Join(dir, episodeFile), []byte(status+":"+strconv.Itoa(gen)+"\n"))
}

// PublishEpisode increments the recovery generation and atomically marks it
// pending. NOT PORTED IN V1: upstream's second phase (handling, its
// fm_recovery_marker_begin_handling) is cut, because nothing in Plan 2
// transitions an episode between presentation and acknowledgement and a
// written-but-never-produced value has no place in the schema.
func PublishEpisode(dir string) (int, error) {
	var gen int
	err := withLock(dir, func() error {
		current, err := readEpisode(dir)
		if err != nil {
			return err
		}
		gen = current.Gen + 1
		return writeEpisode(dir, "pending", gen)
	})
	return gen, err
}

// AckEpisode retires a pending episode whose generation matches gen by
// rewriting the marker as acked:<gen>. A generation mismatch returns
// ErrGenerationMismatch and leaves the marker untouched; callers treat it as
// a signal to re-drain, not a failure.
func AckEpisode(dir string, gen int) error {
	return withLock(dir, func() error {
		current, err := readEpisode(dir)
		if err != nil {
			return err
		}
		if current.Gen != gen {
			return ErrGenerationMismatch
		}
		return writeEpisode(dir, "acked", gen)
	})
}
