package voice

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// sherpaAPI is the part of the engine's C API dictation calls, each function
// by its handles, so that the recognizer can be tested without the library.
type sherpaAPI interface {
	createRecognizer(*sherpaOfflineRecognizerConfig) uintptr
	readWave([]byte) uintptr
	waveData(uintptr) (sherpaWave, error)
	freeWave(uintptr)
	createStream(uintptr) uintptr
	destroyStream(uintptr)
	acceptWaveform(uintptr, sherpaWave)
	decode(uintptr, uintptr)
	resultJSON(uintptr) uintptr
	readJSON(uintptr) ([]byte, error)
	freeJSON(uintptr)
}

// sherpaRecognizer holds the loaded model for as long as the worker runs: the
// worker is ended rather than the model unloaded, and the machine takes back
// its memory then.
type sherpaRecognizer struct {
	api    sherpaAPI
	handle uintptr
}

func newSherpaRecognizer(options WorkerOptions, api sherpaAPI) (*sherpaRecognizer, error) {
	// The engine copies the configuration while it loads the model, so it
	// is held in place only for that call.
	var pinner runtime.Pinner
	defer pinner.Unpin()
	text := func(value string) *byte {
		characters := append([]byte(value), 0)
		pinner.Pin(&characters[0])
		return &characters[0]
	}
	config := &sherpaOfflineRecognizerConfig{
		FeatConfig:     sherpaFeatureConfig{SampleRate: 16000, FeatureDim: 80},
		DecodingMethod: text("greedy_search"),
		MaxActivePaths: 4,
	}
	pinner.Pin(config)
	config.ModelConfig.Transducer = sherpaOfflineTransducerModelConfig{Encoder: text(options.Encoder), Decoder: text(options.Decoder), Joiner: text(options.Joiner)}
	config.ModelConfig.Tokens = text(options.Tokens)
	config.ModelConfig.NumThreads = int32(options.Threads)
	config.ModelConfig.Provider = text("cpu")
	config.ModelConfig.ModelType = text(options.ModelType)
	handle := api.createRecognizer(config)
	if handle == 0 {
		return nil, errors.New("the engine could not load the model")
	}
	return &sherpaRecognizer{api: api, handle: handle}, nil
}

// Recognize returns the words in sound, a WAV file's bytes, and frees what
// the engine made for it.
func (r *sherpaRecognizer) Recognize(sound []byte) (string, error) {
	wave := r.api.readWave(sound)
	if wave == 0 {
		return "", errors.New("the sound is not a WAV file the engine can read")
	}
	defer r.api.freeWave(wave)
	samples, err := r.api.waveData(wave)
	if err != nil {
		return "", fmt.Errorf("read the sound the engine decoded: %w", err)
	}
	stream := r.api.createStream(r.handle)
	if stream == 0 {
		return "", errors.New("the engine could not start recognising")
	}
	defer r.api.destroyStream(stream)
	r.api.acceptWaveform(stream, samples)
	r.api.decode(r.handle, stream)
	result := r.api.resultJSON(stream)
	if result == 0 {
		return "", errors.New("the engine gave no result")
	}
	defer r.api.freeJSON(result)
	data, err := r.api.readJSON(result)
	if err != nil {
		return "", fmt.Errorf("read the engine's result: %w", err)
	}
	var heard struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &heard); err != nil {
		return "", fmt.Errorf("read the engine's result: %w", err)
	}
	return strings.TrimSpace(heard.Text), nil
}
