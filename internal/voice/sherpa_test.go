package voice

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type fakeSherpaAPI struct {
	failure string
	config  *sherpaOfflineRecognizerConfig
	calls   map[string]int
	sounds  [][]byte
}

func (api *fakeSherpaAPI) called(name string) uintptr {
	api.calls[name]++
	if api.failure == name {
		return 0
	}
	return 1
}

func (api *fakeSherpaAPI) createRecognizer(config *sherpaOfflineRecognizerConfig) uintptr {
	api.config = config
	return api.called("create-recognizer")
}

func (api *fakeSherpaAPI) destroyRecognizer(uintptr)          { api.called("destroy-recognizer") }
func (api *fakeSherpaAPI) freeWave(uintptr)                   { api.called("free-wave") }
func (api *fakeSherpaAPI) createStream(uintptr) uintptr       { return api.called("create-stream") }
func (api *fakeSherpaAPI) destroyStream(uintptr)              { api.called("destroy-stream") }
func (api *fakeSherpaAPI) acceptWaveform(uintptr, sherpaWave) { api.called("accept-waveform") }
func (api *fakeSherpaAPI) decode(uintptr, uintptr)            { api.called("decode") }
func (api *fakeSherpaAPI) resultJSON(uintptr) uintptr         { return api.called("result-json") }
func (api *fakeSherpaAPI) freeJSON(uintptr)                   { api.called("free-json") }

func (api *fakeSherpaAPI) readWave(sound []byte) uintptr {
	api.sounds = append(api.sounds, append([]byte(nil), sound...))
	return api.called("read-wave")
}

func (api *fakeSherpaAPI) waveData(uintptr) (sherpaWave, error) {
	api.called("wave-data")
	if api.failure == "wave-data" {
		return sherpaWave{}, errors.New("owned wave read failed")
	}
	return sherpaWave{Samples: 1, SampleRate: 16000, NumSamples: 4}, nil
}

func (api *fakeSherpaAPI) readJSON(uintptr) ([]byte, error) {
	api.called("read-json")
	switch api.failure {
	case "read-json":
		return nil, errors.New("owned JSON read failed")
	case "bad-json":
		return []byte("broken"), nil
	case "missing-text":
		return []byte("{}"), nil
	}
	return json.Marshal(struct{ Text string }{Text: " decoded words "})
}

func (api *fakeSherpaAPI) release() error {
	api.called("release")
	if api.failure == "release" {
		return errors.New("owned library release failed")
	}
	return nil
}

func workerArguments(t *testing.T) []string {
	t.Helper()
	folder := t.TempDir()
	return []string{
		filepath.Join(folder, WORKER_LIBRARY),
		"--num-threads=2",
		"--encoder=" + filepath.Join(folder, "encoder.onnx"),
		"--decoder=" + filepath.Join(folder, "decoder.onnx"),
		"--joiner=" + filepath.Join(folder, "joiner.onnx"),
		"--tokens=" + filepath.Join(folder, "tokens.txt"),
		"--model-type=nemo_transducer",
	}
}

func TestWorkerArgumentsRejectInvalidAndExtraSettings(t *testing.T) {
	valid := workerArguments(t)
	var invalid [][]string
	invalid = append(invalid, nil, valid[:6], append(append([]string(nil), valid...), "--extra=value"))
	for _, change := range []struct {
		index int
		value string
	}{
		{0, "relative.dll"},
		{0, filepath.Join(t.TempDir(), "wrong.dll")},
		{0, "//remote/share/" + WORKER_LIBRARY},
		{0, valid[0] + "\x00"},
		{1, "--num-threads=8"},
		{1, "--num-threads"},
		{2, "--encoder=relative.onnx"},
		{2, "--encoder=//remote/share/encoder.onnx"},
		{2, "--unknown=" + filepath.Join(t.TempDir(), "model.onnx")},
		{3, valid[2]},
		{6, "--model-type=whisper"},
	} {
		arguments := append([]string(nil), valid...)
		arguments[change.index] = change.value
		invalid = append(invalid, arguments)
	}
	for _, arguments := range invalid {
		if _, err := ParseWorkerArguments(arguments); err == nil {
			t.Fatalf("invalid worker arguments were accepted: %q", arguments)
		}
	}
}

func TestWorkerArgumentsRetainTheShippedPinnedTransducer(t *testing.T) {
	settings, err := Load(filepath.Join("..", "..", "config", "voice.json"))
	if err != nil {
		t.Fatal(err)
	}
	voice := &Voice{Settings: settings, Dir: t.TempDir()}
	arguments := []string{filepath.Join(voice.folder(settings.Engine), settings.Program)}
	for _, argument := range settings.Args {
		arguments = append(arguments, strings.ReplaceAll(argument, "{model}", voice.folder(settings.Model)))
	}
	options, err := ParseWorkerArguments(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if options.Library != filepath.Join(voice.folder(settings.Engine), WORKER_LIBRARY) || options.Encoder != filepath.Join(voice.folder(settings.Model), "encoder.int8.onnx") {
		t.Fatalf("the pinned worker selected %+v", options)
	}
}

func TestSherpaReusesOneRecognizerAndReleasesEveryPipeJob(t *testing.T) {
	options, err := ParseWorkerArguments(workerArguments(t))
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeSherpaAPI{calls: make(map[string]int)}
	engine, err := newSherpaRecognizer(options, api)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Error(err)
		}
	})
	var input, output bytes.Buffer
	for _, sound := range []string{"RIFF-first", "RIFF-second"} {
		if err := writeFrame(&input, []byte(sound)); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunWorker(&input, &output, engine.Recognize); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		data, err := readFrame(&output, MAX_REPLY_BYTES)
		if err != nil {
			t.Fatal(err)
		}
		var reply workerReply
		if err := json.Unmarshal(data, &reply); err != nil || reply.Text != "decoded words" || reply.Error != "" {
			t.Fatalf("worker returned %s, %v", data, err)
		}
	}
	for _, name := range []string{"read-wave", "free-wave", "create-stream", "destroy-stream", "accept-waveform", "decode", "result-json", "free-json"} {
		if api.calls[name] != 2 {
			t.Fatalf("%s called %d times, want two", name, api.calls[name])
		}
	}
	if api.calls["create-recognizer"] != 1 || api.calls["destroy-recognizer"] != 0 || api.calls["release"] != 0 {
		t.Fatalf("recognizer was reloaded or retired between jobs: %v", api.calls)
	}
	if string(api.sounds[0]) != "RIFF-first" || string(api.sounds[1]) != "RIFF-second" {
		t.Fatalf("pipe sounds changed: %q", api.sounds)
	}
	config := api.config
	if config.FeatConfig.SampleRate != 16000 || config.FeatConfig.FeatureDim != 80 || config.ModelConfig.NumThreads != 2 || config.ModelConfig.Debug != 0 || config.ModelConfig.Paraformer.Model != nil || config.LmConfig.Model != nil {
		t.Fatal("pinned CPU transducer configuration or inactive fields changed")
	}
}

func TestSherpaReleasesOwnedResourcesOnEveryRecognitionFailure(t *testing.T) {
	options, err := ParseWorkerArguments(workerArguments(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []struct {
		name string
		wave, stream, result int
	}{
		{"read-wave", 0, 0, 0},
		{"wave-data", 1, 0, 0},
		{"create-stream", 1, 0, 0},
		{"result-json", 1, 1, 0},
		{"read-json", 1, 1, 1},
		{"bad-json", 1, 1, 1},
		{"missing-text", 1, 1, 1},
	} {
		api := &fakeSherpaAPI{failure: failure.name, calls: make(map[string]int)}
		engine, err := newSherpaRecognizer(options, api)
		if err != nil {
			t.Fatal(err)
		}
		text, recognizeErr := engine.Recognize([]byte("RIFF-failed"))
		closeErr := engine.Close()
		if text != "" || recognizeErr == nil || closeErr != nil {
			t.Fatalf("%s returned %q, %v; close %v", failure.name, text, recognizeErr, closeErr)
		}
		if api.calls["free-wave"] != failure.wave || api.calls["destroy-stream"] != failure.stream || api.calls["free-json"] != failure.result || api.calls["destroy-recognizer"] != 1 || api.calls["release"] != 1 {
			t.Fatalf("%s leaked or double-freed a resource: %v", failure.name, api.calls)
		}
	}
}

func TestSherpaInitializationFailureUnloadsItsOwnedLibrary(t *testing.T) {
	api := &fakeSherpaAPI{failure: "create-recognizer", calls: make(map[string]int)}
	engine, err := newSherpaRecognizer(WorkerOptions{}, api)
	if engine != nil || err == nil || api.calls["create-recognizer"] != 1 || api.calls["destroy-recognizer"] != 0 || api.calls["release"] != 1 {
		t.Fatalf("failed initialization returned %v, %v, calls %v", engine, err, api.calls)
	}
}

func TestSherpaCloseIsIdempotentAndReportsReleaseFailure(t *testing.T) {
	api := &fakeSherpaAPI{failure: "release", calls: make(map[string]int)}
	engine, err := newSherpaRecognizer(WorkerOptions{}, api)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err == nil || !strings.Contains(err.Error(), "release failed") {
		t.Fatalf("release failure answered %v", err)
	}
	api.failure = ""
	for range 2 {
		if err := engine.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if text, err := engine.Recognize([]byte("RIFF-late")); text != "" || err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed recognizer returned %q, %v", text, err)
	}
	if api.calls["destroy-recognizer"] != 1 || api.calls["release"] != 2 || api.calls["read-wave"] != 0 {
		t.Fatalf("repeated Close or late recognition changed ownership: %v", api.calls)
	}
}
