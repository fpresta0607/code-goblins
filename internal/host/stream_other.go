//go:build !windows

package host

import (
	"errors"
	"fmt"

	"github.com/fpresta0607/code-goblins/internal/claudestream"
)

// errStreamNotPorted is what a stream session's host returns on this system:
// a program on pipes, speaking lines, runs only on Windows yet.
var errStreamNotPorted = fmt.Errorf("host: stream sessions are not ported to this system yet: %w", errors.ErrUnsupported)

// ReadStreamState refuses, since no stream session runs on this system yet.
func ReadStreamState(record Record) (claudestream.Session, error) {
	return claudestream.Session{}, errStreamNotPorted
}

// StreamViewer reads a stream session's lines, which this system has none of.
type StreamViewer struct{}

// WatchStream refuses, since no stream session runs on this system yet.
func WatchStream(record Record) (*StreamViewer, error) { return nil, errStreamNotPorted }

// Next refuses, since no stream session runs on this system yet.
func (v *StreamViewer) Next() (StreamEvent, error) { return StreamEvent{}, errStreamNotPorted }

// CloseSession refuses, since no stream session runs on this system yet.
func (v *StreamViewer) CloseSession() error { return errStreamNotPorted }

// Close does nothing, since WatchStream connects to nothing.
func (v *StreamViewer) Close() error { return nil }

// StreamWriter writes lines into a stream session, which this system has none of.
type StreamWriter struct{}

// DialStreamWriter refuses, since no stream session runs on this system yet.
func DialStreamWriter(record Record) (*StreamWriter, error) { return nil, errStreamNotPorted }

// Write refuses, since no stream session runs on this system yet.
func (w *StreamWriter) Write(line []byte) error { return errStreamNotPorted }

// CloseSession refuses, since no stream session runs on this system yet.
func (w *StreamWriter) CloseSession() error { return errStreamNotPorted }

// Close does nothing, since DialStreamWriter connects to nothing.
func (w *StreamWriter) Close() error { return nil }
