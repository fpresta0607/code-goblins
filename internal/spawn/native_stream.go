package spawn

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/claudestream"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// onStream reports whether task id's start on kind goes on stream-json pipes
// (#2374): only a Claude Code goblin's, only in a home whose
// config/fleet.json turns claude_goblins_on_stream on, and never the start
// after a stream session of the task that ended by itself, which starts in
// the terminal and says why as fallback.
func (s Service) onStream(id string, kind harness.Kind) (isStream bool, fallback string, err error) {
	if kind != harness.Claude {
		return false, "", nil
	}
	settings, err := fleetconfig.Read(s.HomeRoot)
	if err != nil {
		return false, "", fmt.Errorf("spawn: %w", err)
	}
	if !settings.ClaudeGoblinsOnStream {
		return false, "", nil
	}
	end, err := host.ReadEnd(s.StateDir, id)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, "", fmt.Errorf("spawn: read how task %s's last session ended: %w", id, err)
	}
	if err == nil && end.Stream && !end.Closed {
		return false, fmt.Sprintf("its last stream session ended by itself with exit code %d at %s", end.Code, end.Ended.Format(time.RFC3339)), nil
	}
	return true, "", nil
}

// proveStream asks the stream session record names what it runs with,
// within nativeStartup, and says why it is not a session a goblin runs on,
// or nil (claudestream.ProveStart). The CLAUDE.md files it must have loaded
// are dir's and the user's own, each where one exists.
func proveStream(record host.Record, dir string) error {
	viewer, err := host.WatchStream(record)
	if err != nil {
		return err
	}
	writer, err := host.DialStreamWriter(record)
	if err != nil {
		_ = viewer.Close()
		return err
	}
	defer writer.Close()
	proving, cancel := awake.WithTimeout(context.Background(), nativeStartup)
	defer cancel()
	go func() {
		// Closing the viewer ends a read that waits on a session that never
		// answers, once the bound is spent, and every read once the proof is
		// done.
		<-proving.Done()
		_ = viewer.Close()
	}()
	next := func() (json.RawMessage, error) {
		for {
			event, err := viewer.Next()
			if err != nil {
				if errors.Is(proving.Err(), context.DeadlineExceeded) {
					return nil, fmt.Errorf("it gave no answer within %s", nativeStartup)
				}
				return nil, err
			}
			if event.IsExited {
				return nil, fmt.Errorf("Claude Code ended with exit code %d", event.Code)
			}
			if !event.Line.IsWritten {
				return event.Line.Line, nil
			}
		}
	}
	write := func(line json.RawMessage) error { return writer.Write(line) }
	_, err = claudestream.ProveStart(write, next, expectedMemory(dir))
	return err
}

// expectedMemory are the CLAUDE.md files a session in dir loads, each where
// one exists: the project's in dir and the user's own in his Claude Code
// folder.
func expectedMemory(dir string) []string {
	candidates := []string{filepath.Join(dir, "CLAUDE.md")}
	if userHome, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(userHome, ".claude", "CLAUDE.md"))
	}
	var expected []string
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			expected = append(expected, path)
		}
	}
	return expected
}

// reportFallback tells the CEO, as goblins notify does, that task id runs in
// its terminal rather than on stream-json pipes, and why.
func (s Service) reportFallback(id, why string) error {
	line := "working: runs in its terminal rather than on stream-json pipes: " + bounded(state.NormalizeStatusDetail(why), 900)
	if err := state.AppendStatus(s.StateDir, id, line); err != nil {
		return fmt.Errorf("spawn: report the terminal fallback: %w", err)
	}
	if _, err := wake.Append(s.StateDir, "notify", id, line); err != nil {
		return fmt.Errorf("spawn: report the terminal fallback: %w", err)
	}
	return nil
}

// lineID is a new random id for a line written to a stream session, a
// version 4 UUID as Claude Code gives its own.
func lineID() string {
	var id [16]byte
	_, _ = rand.Read(id[:]) // crypto/rand.Read never fails
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:])
}

// deliverStream writes text to the stream session record names as a line of
// its own id, and proves Claude Code has it from its own report of that line
// (claudestream.Session.Commands): started, it took it; still queued
// nativeQueuedProof into a turn, it holds it for its next step, which is
// fleet.ErrQueuedForToolCall. ShouldRefuseTurn writes nothing into a turn. A
// line it never reports taking within nativeAccepted is unconfirmed, never
// written again.
func (s Service) deliverStream(ctx context.Context, record host.Record, text string) error {
	before, err := host.ReadStreamState(record)
	if err != nil {
		return fmt.Errorf("spawn: %w: %w", fleet.ErrNotTyped, err)
	}
	if before.IsWorking && s.ShouldRefuseTurn {
		return fmt.Errorf("spawn: stream session %s: %w: %w", record.ID, fleet.ErrNotTyped, ErrInTurn)
	}
	writer, err := host.DialStreamWriter(record)
	if err != nil {
		return fmt.Errorf("spawn: stream session %s does not answer: %w: %w", record.ID, fleet.ErrNotTyped, err)
	}
	defer writer.Close()
	id := lineID()
	if err := writer.Write(claudestream.UserLine(id, text)); err != nil {
		return fmt.Errorf("spawn: the line for stream session %s may not have reached it; check it with goblins peek %s rather than sending it again: %w", record.ID, record.ID, err)
	}
	if before.IsWorking && s.SubmittedIntoTurn != nil {
		s.SubmittedIntoTurn()
	}
	written := time.Now()
	for {
		session, err := host.ReadStreamState(record)
		if err != nil {
			return fmt.Errorf("spawn: the line was written to stream session %s, and whether Claude Code took it cannot be read: %w", record.ID, err)
		}
		command := session.Commands[id]
		switch {
		case command == "started" || command == "completed":
			return nil
		case session.IsExited:
			return fmt.Errorf("spawn: Claude Code in stream session %s ended with exit code %d before it took the line", record.ID, session.ExitCode)
		case command == "queued" && session.IsWorking && time.Since(written) >= nativeQueuedProof:
			return fmt.Errorf("spawn: stream session %s holds the line: %w", record.ID, fleet.ErrQueuedForToolCall)
		case time.Since(written) >= nativeAccepted:
			return fmt.Errorf("spawn: the line was written to stream session %s, and Claude Code did not report taking it within %s; check it with goblins peek %s rather than sending it again", record.ID, nativeAccepted, record.ID)
		}
		if err := s.sleep(ctx, nativePoll); err != nil {
			return err
		}
	}
}
