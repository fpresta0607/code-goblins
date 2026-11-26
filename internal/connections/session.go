package connections

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

const (
	// maxTranscriptLine is the longest line of a session record that is read;
	// a longer one is skipped.
	maxTranscriptLine = 8 << 20
	// maxServerTools is the most tools one server's item lists.
	maxServerTools = 1024
	// maxFailureText is the longest reason a failed server's item gives.
	maxFailureText = 200
)

// deferredToolsDelta is the line of Claude's session record that names the
// tools and MCP servers a session gained or lost, and the servers that failed
// to load or were still starting.
const deferredToolsDelta = "deferred_tools_delta"

type failedServer struct{ name, reason string }

// session is the servers a Claude session record says loaded, failed to load
// or were still starting, from the lines read so far.
type session struct {
	tools    map[string]map[string]bool
	failed   []failedServer
	pending  []string
	sawDelta bool
}

// sessionFrom is the session prior's items say, so a read from an offset
// carries forward what the lines before it said.
func sessionFrom(prior Goblin) session {
	read := session{tools: map[string]map[string]bool{}, sawDelta: prior.IsConfirmed}
	for _, item := range prior.Items {
		switch {
		case item.Group == GroupLoaded && item.Status == StatusVerified:
			read.tools[item.Name] = map[string]bool{}
			for _, tool := range item.Tools {
				read.tools[item.Name][tool] = true
			}
		case item.Group == GroupLoaded:
			read.pending = append(read.pending, item.Name)
		case item.Group == GroupFailed:
			read.failed = append(read.failed, failedServer{item.Name, item.Detail})
		}
	}
	return read
}

// readSession reads the Claude session record at path from byte offset from
// with prior's servers carried forward, and returns the session and the offset
// after the last whole line read. A record shorter than from is read again from
// the start. A path that is empty or cannot be opened is an error: the session
// has recorded nothing to read.
func readSession(path string, from int64, prior Goblin) (session, int64, error) {
	if path == "" {
		return session{}, 0, errors.New("no session record")
	}
	file, err := fsx.Open(path)
	if err != nil {
		return session{}, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return session{}, 0, err
	}
	read := sessionFrom(prior)
	if from < 0 || info.Size() < from {
		from, read = 0, sessionFrom(Goblin{})
	}
	if _, err := file.Seek(from, io.SeekStart); err != nil {
		return session{}, 0, err
	}
	consumed, err := eachLine(bufio.NewReaderSize(file, 64<<10), func(line []byte) {
		if bytes.Contains(line, []byte(deferredToolsDelta)) {
			read.apply(line)
		}
	})
	return read, from + consumed, err
}

// eachLine gives fn every whole line of reader that is no longer than
// maxTranscriptLine, and returns how many bytes it read through the last
// whole line, a line that ends the stream with no newline among them when it
// is complete JSON. A longer line is read past and skipped.
func eachLine(reader *bufio.Reader, fn func(line []byte)) (int64, error) {
	var line []byte
	var total, consumed int64
	isTooLong := false
	for {
		chunk, err := reader.ReadSlice('\n')
		total += int64(len(chunk))
		if !isTooLong {
			if len(line)+len(chunk) > maxTranscriptLine {
				isTooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		switch {
		case err == nil:
			if !isTooLong {
				fn(line)
			}
			line, isTooLong, consumed = line[:0], false, total
		case errors.Is(err, bufio.ErrBufferFull):
		case errors.Is(err, io.EOF):
			if !isTooLong && len(line) > 0 && json.Valid(line) {
				fn(line)
				consumed = total
			}
			return consumed, nil
		default:
			return consumed, err
		}
	}
}

// delta is the part of a session record's line a session's servers are read
// from.
type delta struct {
	Type       string `json:"type"`
	Attachment struct {
		Type    string   `json:"type"`
		Added   []string `json:"addedNames"`
		Readded []string `json:"readdedNames"`
		Removed []string `json:"removedNames"`
		Failed  []struct {
			Name      string `json:"name"`
			ErrorCode string `json:"errorCode"`
			Error     string `json:"error"`
		} `json:"failedMcpServers"`
		Pending []string `json:"pendingMcpServers"`
	} `json:"attachment"`
}

// apply reads one line that names a deferred tools delta. A line that does not
// parse, or is another attachment, changes nothing.
func (s *session) apply(line []byte) {
	var read delta
	if json.Unmarshal(line, &read) != nil || read.Type != "attachment" || read.Attachment.Type != deferredToolsDelta {
		return
	}
	s.sawDelta = true
	for _, name := range slices.Concat(read.Attachment.Added, read.Attachment.Readded) {
		if server, tool, ok := mcpToolName(name); ok {
			if s.tools[server] == nil {
				s.tools[server] = map[string]bool{}
			}
			s.tools[server][tool] = true
		}
	}
	for _, name := range read.Attachment.Removed {
		if server, tool, ok := mcpToolName(name); ok {
			delete(s.tools[server], tool)
			if len(s.tools[server]) == 0 {
				delete(s.tools, server)
			}
		}
	}
	s.failed, s.pending = nil, slices.Clone(read.Attachment.Pending)
	for _, failure := range read.Attachment.Failed {
		reason := failure.Error
		if reason == "" {
			reason = failure.ErrorCode
		}
		s.failed = append(s.failed, failedServer{failure.Name, reason})
	}
}

// mcpToolName splits mcp__<server>__<tool> into its server and tool.
func mcpToolName(name string) (server, tool string, ok bool) {
	rest, isMCP := strings.CutPrefix(name, "mcp__")
	if !isMCP {
		return "", "", false
	}
	server, tool, ok = strings.Cut(rest, "__")
	return server, tool, ok && server != "" && tool != ""
}

// items are the session's servers as a goblin's rows: those loaded with the
// tools they gave, those that failed with why, and those still starting.
func (s session) items() []Item {
	var items []Item
	failed := map[string]bool{}
	for _, failure := range s.failed {
		failed[failure.name] = true
	}
	loaded := make([]string, 0, len(s.tools))
	for server := range s.tools {
		if server != chromeServer && !failed[server] && validName(server) {
			loaded = append(loaded, server)
		}
	}
	slices.Sort(loaded)
	for _, server := range loaded {
		tools := make([]string, 0, len(s.tools[server]))
		for tool := range s.tools[server] {
			tools = append(tools, tool)
		}
		slices.Sort(tools)
		items = append(items, Item{ID: "loaded:" + server, Level: LevelGoblin, Group: GroupLoaded, Kind: "mcp", Name: server, Status: StatusVerified, Detail: "Loaded in its session.", Tools: tools[:min(len(tools), maxServerTools)], Source: sessionSource})
	}
	for _, failure := range s.failed {
		if validName(failure.name) {
			items = append(items, Item{ID: "failed:" + failure.name, Level: LevelGoblin, Group: GroupFailed, Kind: "mcp", Name: failure.name, Status: StatusFailed, Detail: failureText(failure.reason), Source: sessionSource})
		}
	}
	for _, server := range s.pending {
		if validName(server) && s.tools[server] == nil && !failed[server] {
			items = append(items, Item{ID: "pending:" + server, Level: LevelGoblin, Group: GroupLoaded, Kind: "mcp", Name: server, Status: StatusNotChecked, Detail: "Still starting when its record was read.", Source: sessionSource})
		}
	}
	return items
}

var (
	urlUserInfo      = regexp.MustCompile(`(https?://)[^\s/@"'<>]+@`)
	urlQuery         = regexp.MustCompile(`(https?://[^\s?#"'<>]*)[?#][^\s"'<>]*`)
	bearerValue      = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	namedCredentials = regexp.MustCompile(`(?i)\b(token|api[_-]?key|secret|password|authorization|key)\s*[:=]\s*[^\s,;"']+`)
)

// failureText is a server's failure as one plain line of at most
// maxFailureText characters. The record's own words can quote an address or a
// header, so an address keeps no query or sign-in part, and a value written
// after a credential's name is dropped.
func failureText(reason string) string {
	reason = urlUserInfo.ReplaceAllString(reason, "$1")
	reason = urlQuery.ReplaceAllString(reason, "$1")
	reason = bearerValue.ReplaceAllString(reason, "Bearer [removed]")
	reason = namedCredentials.ReplaceAllString(reason, "$1=[removed]")
	reason = strings.Join(strings.Fields(reason), " ")
	if reason == "" {
		return "It failed to load."
	}
	if runes := []rune(reason); len(runes) > maxFailureText {
		reason = string(runes[:maxFailureText])
	}
	return reason
}
