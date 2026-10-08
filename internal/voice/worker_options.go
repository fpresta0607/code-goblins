package voice

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// WorkerOptions is what `cfo voice-worker` loads: the engine's library and a
// model, a transducer or a Moonshine model.
type WorkerOptions struct {
	Library string
	Threads int
	Tokens  string
	// A transducer: its type and its three parts.
	ModelType string
	Encoder   string
	Decoder   string
	Joiner    string
	// A Moonshine model: its encoder and its merged decoder.
	MoonshineEncoder       string
	MoonshineMergedDecoder string
}

// ParseWorkerArguments reads `cfo voice-worker`'s arguments: the full path of
// the engine's library, then the settings' Args with the model's folder in
// place of {model}.
func ParseWorkerArguments(arguments []string) (WorkerOptions, error) {
	if len(arguments) == 0 || !filepath.IsAbs(arguments[0]) {
		return WorkerOptions{}, errors.New("voice-worker needs the full path of the engine's library, then the model's settings")
	}
	options, err := parseModelSettings(arguments[1:])
	if err != nil {
		return WorkerOptions{}, err
	}
	options.Library = arguments[0]
	return options, nil
}

// parseModelSettings reads the model's settings, each of them once. They are
// the flags of the engine's own sherpa-onnx-offline, so a model is pinned with
// the flags its documentation gives: the threads and the tokens, and either
// a transducer's type and three parts or a Moonshine model's two.
func parseModelSettings(arguments []string) (WorkerOptions, error) {
	var options WorkerOptions
	var threads string
	values := map[string]*string{
		"--num-threads": &threads, "--tokens": &options.Tokens,
		"--model-type": &options.ModelType, "--encoder": &options.Encoder, "--decoder": &options.Decoder, "--joiner": &options.Joiner,
		"--moonshine-encoder": &options.MoonshineEncoder, "--moonshine-merged-decoder": &options.MoonshineMergedDecoder,
	}
	for _, argument := range arguments {
		name, value, _ := strings.Cut(argument, "=")
		target, known := values[name]
		if !known || value == "" || *target != "" {
			return WorkerOptions{}, fmt.Errorf("voice-worker does not take %q: it takes --num-threads and --tokens, then a transducer's --model-type, --encoder, --decoder and --joiner or a Moonshine model's --moonshine-encoder and --moonshine-merged-decoder, each once", argument)
		}
		*target = value
	}
	for _, name := range []string{"--num-threads", "--tokens"} {
		if *values[name] == "" {
			return WorkerOptions{}, fmt.Errorf("voice-worker needs %s", name)
		}
	}
	transducer := []string{"--model-type", "--encoder", "--decoder", "--joiner"}
	moonshine := []string{"--moonshine-encoder", "--moonshine-merged-decoder"}
	given := func(names []string) int {
		count := 0
		for _, name := range names {
			if *values[name] != "" {
				count++
			}
		}
		return count
	}
	if !(given(transducer) == len(transducer) && given(moonshine) == 0 || given(moonshine) == len(moonshine) && given(transducer) == 0) {
		return WorkerOptions{}, fmt.Errorf("voice-worker needs one model: a transducer's %s, or a Moonshine model's %s", strings.Join(transducer, ", "), strings.Join(moonshine, " and "))
	}
	var err error
	if options.Threads, err = strconv.Atoi(threads); err != nil || options.Threads < 1 {
		return WorkerOptions{}, fmt.Errorf("voice-worker needs a number of threads above 0, not %q", threads)
	}
	return options, nil
}
