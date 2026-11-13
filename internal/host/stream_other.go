//go:build !windows

package host

import "github.com/fpresta0607/code-goblins/internal/claudestream"

// Answer refuses, since no host runs on this system yet.
func Answer(record Record) error { return errNotPorted }

// ReadStreamState refuses, since no host runs on this system yet.
func ReadStreamState(record Record) (claudestream.Session, error) {
	return claudestream.Session{}, errNotPorted
}

// StreamViewer reads a stream session's lines, which this system has none of.
type StreamViewer struct{}

// WatchStream refuses, since no host runs on this system yet.
func WatchStream(record Record) (*StreamViewer, error) { return nil, errNotPorted }

// Next refuses, since no host runs on this system yet.
func (v *StreamViewer) Next() (StreamEvent, error) { return StreamEvent{}, errNotPorted }

// CloseSession refuses, since no host runs on this system yet.
func (v *StreamViewer) CloseSession() error { return errNotPorted }

// Close does nothing, since WatchStream connects to nothing.
func (v *StreamViewer) Close() error { return nil }

// StreamWriter writes lines into a stream session, which this system has none of.
type StreamWriter struct{}

// DialStreamWriter refuses, since no host runs on this system yet.
func DialStreamWriter(record Record) (*StreamWriter, error) { return nil, errNotPorted }

// Write refuses, since no host runs on this system yet.
func (w *StreamWriter) Write(line []byte) error { return errNotPorted }

// CloseSession refuses, since no host runs on this system yet.
func (w *StreamWriter) CloseSession() error { return errNotPorted }

// Close does nothing, since DialStreamWriter connects to nothing.
func (w *StreamWriter) Close() error { return nil }
