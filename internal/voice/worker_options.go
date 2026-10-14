package voice

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const WORKER_LIBRARY = "sherpa-onnx-c-api.dll"

type WorkerOptions struct {
	Library string
	Encoder string
	Decoder string
	Joiner  string
	Tokens  string
}

type WorkerRecognizer interface {
	Recognize([]byte) (string, error)
	Close() error
}

func ParseWorkerArguments(arguments []string) (WorkerOptions, error) {
	if len(arguments) != 7 {
		return WorkerOptions{}, errors.New("voice-worker requires one absolute C API library path and six pinned transducer arguments")
	}
	options := WorkerOptions{Library: arguments[0]}
	if !localWorkerPath(options.Library) || filepath.Base(options.Library) != WORKER_LIBRARY {
		return WorkerOptions{}, errors.New("voice-worker requires an absolute local sherpa-onnx-c-api.dll path")
	}
	seen := make(map[string]bool, 6)
	for _, argument := range arguments[1:] {
		name, value, isAssigned := strings.Cut(argument, "=")
		if !isAssigned || seen[name] {
			return WorkerOptions{}, fmt.Errorf("voice-worker argument %q is missing its value or repeated", name)
		}
		seen[name] = true
		switch name {
		case "--num-threads":
			if value != "2" {
				return WorkerOptions{}, errors.New("voice-worker requires --num-threads=2")
			}
		case "--model-type":
			if value != "nemo_transducer" {
				return WorkerOptions{}, errors.New("voice-worker requires --model-type=nemo_transducer")
			}
		case "--encoder", "--decoder", "--joiner", "--tokens":
			if !localWorkerPath(value) {
				return WorkerOptions{}, fmt.Errorf("voice-worker %s requires an absolute local file path", name)
			}
			switch name {
			case "--encoder":
				options.Encoder = filepath.Clean(value)
			case "--decoder":
				options.Decoder = filepath.Clean(value)
			case "--joiner":
				options.Joiner = filepath.Clean(value)
			case "--tokens":
				options.Tokens = filepath.Clean(value)
			}
		default:
			return WorkerOptions{}, fmt.Errorf("voice-worker does not support argument %q", name)
		}
	}
	for _, name := range []string{"--num-threads", "--model-type", "--encoder", "--decoder", "--joiner", "--tokens"} {
		if !seen[name] {
			return WorkerOptions{}, fmt.Errorf("voice-worker is missing %s", name)
		}
	}
	options.Library = filepath.Clean(options.Library)
	return options, nil
}

func localWorkerPath(path string) bool {
	return filepath.IsAbs(path) && !strings.HasPrefix(path, `\\`) && !strings.HasPrefix(path, "//") && !strings.ContainsRune(path, 0)
}
