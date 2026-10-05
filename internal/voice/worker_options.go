package voice

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// WorkerOptions is what `cfo voice-worker` loads: the engine's library and a
// transducer model.
type WorkerOptions struct {
	Library   string
	Threads   int
	ModelType string
	Encoder   string
	Decoder   string
	Joiner    string
	Tokens    string
}

// ParseWorkerArguments reads `cfo voice-worker`'s arguments: the full path of
// the engine's library, then the settings' Args with the model's folder in
// place of {model}.
func ParseWorkerArguments(arguments []string) (WorkerOptions, error) {
	if len(arguments) == 0 || !filepath.IsAbs(arguments[0]) {
		return WorkerOptions{}, errors.New("voice-worker needs the full path of the engine's library, then the model's settings")
	}
	options := WorkerOptions{Library: arguments[0]}
	var threads string
	names := []string{"--num-threads", "--model-type", "--encoder", "--decoder", "--joiner", "--tokens"}
	values := map[string]*string{"--num-threads": &threads, "--model-type": &options.ModelType, "--encoder": &options.Encoder, "--decoder": &options.Decoder, "--joiner": &options.Joiner, "--tokens": &options.Tokens}
	for _, argument := range arguments[1:] {
		name, value, _ := strings.Cut(argument, "=")
		target, known := values[name]
		if !known || value == "" || *target != "" {
			return WorkerOptions{}, fmt.Errorf("voice-worker does not take %q: it takes each of %s once", argument, strings.Join(names, ", "))
		}
		*target = value
	}
	for _, name := range names {
		if *values[name] == "" {
			return WorkerOptions{}, fmt.Errorf("voice-worker needs %s", name)
		}
	}
	var err error
	if options.Threads, err = strconv.Atoi(threads); err != nil || options.Threads < 1 {
		return WorkerOptions{}, fmt.Errorf("voice-worker needs a number of threads above 0, not %q", threads)
	}
	return options, nil
}
