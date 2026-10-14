//go:build !windows || !amd64

package voice

import "errors"

func OpenWorker(WorkerOptions) (WorkerRecognizer, error) {
	return nil, errors.New("the pinned dictation C API requires Windows x64")
}
