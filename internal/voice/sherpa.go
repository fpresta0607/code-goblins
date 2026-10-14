package voice

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
)

type sherpaAPI interface {
	createRecognizer(*sherpaOfflineRecognizerConfig) uintptr
	destroyRecognizer(uintptr)
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
	release() error
}

type sherpaRecognizer struct {
	api        sherpaAPI
	handle     uintptr
	config     *sherpaOfflineRecognizerConfig
	pinner     runtime.Pinner
	isClosed   bool
	isReleased bool
}

func newSherpaRecognizer(options WorkerOptions, api sherpaAPI) (*sherpaRecognizer, error) {
	engine := &sherpaRecognizer{api: api, config: &sherpaOfflineRecognizerConfig{}}
	engine.pinner.Pin(engine.config)
	stringsToPin := []struct {
		value  string
		target **byte
	}{
		{options.Encoder, &engine.config.ModelConfig.Transducer.Encoder},
		{options.Decoder, &engine.config.ModelConfig.Transducer.Decoder},
		{options.Joiner, &engine.config.ModelConfig.Transducer.Joiner},
		{options.Tokens, &engine.config.ModelConfig.Tokens},
		{"cpu", &engine.config.ModelConfig.Provider},
		{"nemo_transducer", &engine.config.ModelConfig.ModelType},
		{"greedy_search", &engine.config.DecodingMethod},
	}
	for _, setting := range stringsToPin {
		if strings.ContainsRune(setting.value, 0) {
			return nil, errors.Join(errors.New("dictation configuration contains a null byte"), engine.Close())
		}
		buffer := append([]byte(setting.value), 0)
		engine.pinner.Pin(&buffer[0])
		*setting.target = &buffer[0]
	}
	engine.config.FeatConfig = sherpaFeatureConfig{SampleRate: 16000, FeatureDim: 80}
	engine.config.ModelConfig.NumThreads = 2
	engine.config.MaxActivePaths = 4
	engine.handle = api.createRecognizer(engine.config)
	if engine.handle == 0 {
		return nil, errors.Join(errors.New("the pinned C API could not create the dictation recognizer"), engine.Close())
	}
	return engine, nil
}

func (engine *sherpaRecognizer) Recognize(sound []byte) (string, error) {
	if engine.isClosed {
		return "", errors.New("the dictation recognizer is closed")
	}
	if len(sound) == 0 || len(sound) > MAX_SOUND_BYTES {
		return "", errors.New("dictation WAV bytes are empty or exceed the sound limit")
	}
	waveHandle := engine.api.readWave(sound)
	if waveHandle == 0 {
		return "", errors.New("the dictation engine could not read the mono 16-bit PCM WAV")
	}
	defer engine.api.freeWave(waveHandle)
	wave, err := engine.api.waveData(waveHandle)
	if err != nil {
		return "", fmt.Errorf("read the dictation wave: %w", err)
	}
	if wave.Samples == 0 || wave.SampleRate <= 0 || wave.NumSamples <= 0 || wave.NumSamples > MAX_SOUND_BYTES/2 {
		return "", errors.New("the dictation engine returned an empty wave")
	}
	stream := engine.api.createStream(engine.handle)
	if stream == 0 {
		return "", errors.New("the dictation engine could not create an input stream")
	}
	defer engine.api.destroyStream(stream)
	engine.api.acceptWaveform(stream, wave)
	engine.api.decode(engine.handle, stream)
	result := engine.api.resultJSON(stream)
	if result == 0 {
		return "", errors.New("the dictation engine returned no result")
	}
	defer engine.api.freeJSON(result)
	data, err := engine.api.readJSON(result)
	if err != nil {
		return "", fmt.Errorf("read the dictation result: %w", err)
	}
	var reply struct {
		Text *string
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return "", fmt.Errorf("read the dictation result JSON: %w", err)
	}
	if reply.Text == nil {
		return "", errors.New("the dictation engine returned no words field")
	}
	return strings.TrimSpace(*reply.Text), nil
}

func (engine *sherpaRecognizer) Close() error {
	if !engine.isClosed {
		engine.isClosed = true
		if engine.handle != 0 {
			engine.api.destroyRecognizer(engine.handle)
			engine.handle = 0
		}
		engine.pinner.Unpin()
	}
	if engine.isReleased {
		return nil
	}
	if err := engine.api.release(); err != nil {
		return fmt.Errorf("unload the dictation C API: %w", err)
	}
	engine.isReleased = true
	return nil
}
