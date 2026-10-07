package fleettree

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// transcriptPatterns are where each harness writes its session transcript,
// as globs under the user's home with {session} standing for the session id.
var transcriptPatterns = map[string]string{
	"claude": filepath.Join(".claude", "projects", "*", "{session}.jsonl"),
	"codex":  filepath.Join(".codex", "sessions", "*", "*", "*", "rollout-*-{session}.jsonl"),
	"pi":     filepath.Join(".pi", "agent", "sessions", "*", "*_{session}.jsonl"),
}

// sessionID is the shape of a harness session id. Anything else could reach
// outside the transcript directories once substituted into a glob.
var sessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// SessionTranscript is the transcript the harness keeps for session under
// home, the newest match, or empty when there is none or the id is not one.
func SessionTranscript(home, harness, session string) string {
	pattern, ok := transcriptPatterns[strings.ToLower(harness)]
	if home == "" || !ok || !sessionID.MatchString(session) {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(home, strings.ReplaceAll(pattern, "{session}", session)))
	return newestFile(matches)
}

// newestFile is the path in paths whose file was written last.
func newestFile(paths []string) string {
	newest := ""
	var written int64
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && (newest == "" || info.ModTime().UnixNano() > written) {
			newest, written = path, info.ModTime().UnixNano()
		}
	}
	return newest
}

// TranscriptEntryReach bounds how much of a transcript's end is read for its
// last entries, since one entry holding a large tool result can run to
// megabytes.
const TranscriptEntryReach = 4 << 20

// WrittenAt returns when a transcript was last written: the later of the
// file's write time and its last complete entry's own timestamp, zero when
// it cannot be read. Codex keeps its rollout open and appends to it, and on
// 2026-09-29 a rollout's write time stayed ninety seconds after its creation
// for hours while its entries ran on, read alike with os.Stat and from an
// open handle.
func WrittenAt(path string) time.Time {
	written, _ := tail(path)
	return written
}

// tail reads when a transcript was last written and its last complete
// entries. An entry still being written does not end in a newline and is
// passed over.
func tail(path string) (time.Time, [][]byte) {
	if path == "" {
		return time.Time{}, nil
	}
	file, err := fsx.Open(path)
	if err != nil {
		return time.Time{}, nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return time.Time{}, nil
	}
	start := max(0, info.Size()-TranscriptEntryReach)
	data := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(data, start); err != nil && !errors.Is(err, io.EOF) {
		return info.ModTime().UTC(), nil
	}
	lines := bytes.Split(data, []byte("\n"))
	if start > 0 {
		// The first piece may begin part way through an entry.
		lines = lines[1:]
	}
	written := info.ModTime().UTC()
	for i := len(lines) - 1; i >= 0; i-- {
		var entry struct {
			Timestamp time.Time `json:"timestamp"`
		}
		if json.Unmarshal(lines[i], &entry) == nil && !entry.Timestamp.IsZero() {
			written = later(written, entry.Timestamp.UTC())
			break
		}
	}
	return written, lines
}
