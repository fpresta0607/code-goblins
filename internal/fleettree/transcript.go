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

// claudeProjectFolder names the folder under .claude\projects where Claude
// Code keeps the conversations held in a working folder: its path with every
// character that is not a letter or digit as a dash.
var claudeProjectFolder = regexp.MustCompile(`[^A-Za-z0-9]`)

// HeldTranscript is SessionTranscript for a conversation held in dir. Claude
// Code keeps it in dir's own folder, so that folder is asked first, and only
// a conversation it does not hold is looked for in every folder. Looking in
// every folder asked the disk about each of the 1,273 folders Claude Code
// kept on 2026-10-28, 10,196 file operations and 84 ms of processor a look,
// and the board looks for every live goblin's conversation every 15 seconds
// (#1716).
func HeldTranscript(home, harness, dir, session string) string {
	if home != "" && dir != "" && strings.EqualFold(harness, "claude") && sessionID.MatchString(session) {
		own := filepath.Join(home, ".claude", "projects", claudeProjectFolder.ReplaceAllString(dir, "-"), session+".jsonl")
		if info, err := os.Stat(own); err == nil && info.Mode().IsRegular() {
			return own
		}
	}
	return SessionTranscript(home, harness, session)
}

// piFolderCharacters are the characters pi 0.85 writes as a hyphen in the
// folder it keeps a working folder's sessions in, which it wraps in two
// hyphens: C:\dev\x is --C--dev-x--.
var piFolderCharacters = strings.NewReplacer(`\`, "-", "/", "-", ":", "-")

// PiSessions is every session pi keeps under home for the folder dir.
func PiSessions(home, dir string) []string {
	if home == "" || !filepath.IsAbs(dir) {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".pi", "agent", "sessions", "--"+piFolderCharacters.Replace(dir)+"--", "*.jsonl"))
	return matches
}

// piSessionID is the id of the pi session whose file is path, which pi names
// <time>_<id>.jsonl, or empty for none.
func piSessionID(path string) string {
	_, id, found := strings.Cut(strings.TrimSuffix(filepath.Base(path), ".jsonl"), "_")
	if !found || !sessionID.MatchString(id) {
		return ""
	}
	return id
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
