//go:build !windows || !amd64

package voice

import "errors"

// OpenWorker refuses: the pinned engine is built for Windows x64 alone.
func OpenWorker(WorkerOptions) (func([]byte) (string, error), error) {
	return nil, errors.New("the dictation engine needs Windows x64")
}
