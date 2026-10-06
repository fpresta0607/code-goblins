package voice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"
)

// The fake engine's handles, each a different number, so a call handed the
// wrong one shows.
const (
	fakeRecognizer = 11
	fakeStream     = 22
	fakeWave       = 33
	fakeResult     = 44
)

// fakeSherpaAPI records the engine's calls with their handles and fails the
// one named failure. Its result is testdata/sherpa-onnx-1.13.8-stdout.txt,
// what the pinned engine gave for one spoken line, as it was captured.
type fakeSherpaAPI struct {
	failure string
	result  []byte
	// settings are the strings the configuration handed the engine.
	settings map[string]string
	threads  int32
	calls    []string
}

func newFakeSherpaAPI(t *testing.T, failure string) *fakeSherpaAPI {
	t.Helper()
	result, err := os.ReadFile(filepath.Join("testdata", "sherpa-onnx-1.13.8-stdout.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return &fakeSherpaAPI{failure: failure, result: result}
}

func (api *fakeSherpaAPI) call(name string, handles ...uintptr) {
	api.calls = append(api.calls, strings.TrimSuffix(fmt.Sprint(name, handles), "[]"))
}

func (api *fakeSherpaAPI) answer(name string, handle uintptr) uintptr {
	if api.failure == name {
		return 0
	}
	return handle
}

// cString reads a NUL-terminated string the way the engine does.
func cString(text *byte) string {
	if text == nil {
		return ""
	}
	var characters []byte
	for at := unsafe.Pointer(text); *(*byte)(at) != 0; at = unsafe.Add(at, 1) {
		characters = append(characters, *(*byte)(at))
	}
	return string(characters)
}

func (api *fakeSherpaAPI) createRecognizer(config *sherpaOfflineRecognizerConfig) uintptr {
	api.call("create-recognizer")
	model := config.ModelConfig
	api.settings = map[string]string{
		"encoder": cString(model.Transducer.Encoder), "decoder": cString(model.Transducer.Decoder), "joiner": cString(model.Transducer.Joiner),
		"tokens": cString(model.Tokens), "provider": cString(model.Provider), "model type": cString(model.ModelType),
		"decoding": cString(config.DecodingMethod), "paraformer": cString(model.Paraformer.Model), "language model": cString(config.LmConfig.Model),
	}
	api.threads = model.NumThreads
	if config.FeatConfig.SampleRate != 16000 || config.FeatConfig.FeatureDim != 80 || config.MaxActivePaths != 4 || model.Debug != 0 {
		api.call("unexpected-features")
	}
	return api.answer("create-recognizer", fakeRecognizer)
}

func (api *fakeSherpaAPI) readWave([]byte) uintptr {
	api.call("read-wave")
	return api.answer("read-wave", fakeWave)
}

func (api *fakeSherpaAPI) waveData(wave uintptr) (sherpaWave, error) {
	api.call("wave-data", wave)
	if api.failure == "wave-data" {
		return sherpaWave{}, errors.New("unreadable")
	}
	return sherpaWave{Samples: 1, SampleRate: 16000, NumSamples: 4}, nil
}

func (api *fakeSherpaAPI) freeWave(wave uintptr) { api.call("free-wave", wave) }

func (api *fakeSherpaAPI) createStream(recognizer uintptr) uintptr {
	api.call("create-stream", recognizer)
	return api.answer("create-stream", fakeStream)
}

func (api *fakeSherpaAPI) destroyStream(stream uintptr) { api.call("destroy-stream", stream) }

func (api *fakeSherpaAPI) acceptWaveform(stream uintptr, wave sherpaWave) {
	api.call("accept-waveform", stream, uintptr(wave.SampleRate))
}

func (api *fakeSherpaAPI) decode(recognizer, stream uintptr) {
	api.call("decode", recognizer, stream)
}

func (api *fakeSherpaAPI) resultJSON(stream uintptr) uintptr {
	api.call("result-json", stream)
	return api.answer("result-json", fakeResult)
}

func (api *fakeSherpaAPI) readJSON(result uintptr) ([]byte, error) {
	api.call("read-json", result)
	switch api.failure {
	case "read-json":
		return nil, errors.New("unreadable")
	case "not-json":
		return []byte("Done!"), nil
	}
	return api.result, nil
}

func (api *fakeSherpaAPI) freeJSON(result uintptr) { api.call("free-json", result) }

func workerArguments(folder string) []string {
	return []string{
		filepath.Join(folder, "sherpa-onnx-c-api.dll"),
		"--num-threads=3",
		"--encoder=" + filepath.Join(folder, "encoder.onnx"),
		"--decoder=" + filepath.Join(folder, "decoder.onnx"),
		"--joiner=" + filepath.Join(folder, "joiner.onnx"),
		"--tokens=" + filepath.Join(folder, "tokens.txt"),
		"--model-type=nemo_transducer",
	}
}

func TestTheWorkerTakesEachModelSettingOnceAndNothingElse(t *testing.T) {
	valid := workerArguments(t.TempDir())
	invalid := [][]string{nil, valid[:6], append(append([]string(nil), valid...), "--extra=value")}
	for index, value := range map[int]string{
		0: "sherpa-onnx-c-api.dll",
		1: "--num-threads=none",
		2: "--encoder=",
		3: valid[2],
		6: "--model-type",
	} {
		arguments := append([]string(nil), valid...)
		arguments[index] = value
		invalid = append(invalid, arguments)
	}
	for _, arguments := range invalid {
		if _, err := ParseWorkerArguments(arguments); err == nil {
			t.Errorf("the worker took %q", arguments)
		}
	}
	options, err := ParseWorkerArguments(valid)
	if err != nil {
		t.Fatal(err)
	}
	if options.Library != valid[0] || options.Threads != 3 || options.ModelType != "nemo_transducer" || options.Joiner != strings.TrimPrefix(valid[4], "--joiner=") {
		t.Fatalf("the worker read %+v", options)
	}
}

func TestTheShippedSettingsAreWhatTheWorkerTakes(t *testing.T) {
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
	if filepath.Base(options.Library) != "sherpa-onnx-c-api.dll" || options.Encoder != voice.folder(settings.Model)+"/encoder.int8.onnx" {
		t.Fatalf("the shipped settings load %+v", options)
	}
}

func TestTheRecognizerLoadsTheModelOnceAndFreesWhatEachSoundNeeded(t *testing.T) {
	folder := t.TempDir()
	options, err := ParseWorkerArguments(workerArguments(folder))
	if err != nil {
		t.Fatal(err)
	}
	api := newFakeSherpaAPI(t, "")
	recognizer, err := newSherpaRecognizer(options, api)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"encoder": filepath.Join(folder, "encoder.onnx"), "decoder": filepath.Join(folder, "decoder.onnx"), "joiner": filepath.Join(folder, "joiner.onnx"),
		"tokens": filepath.Join(folder, "tokens.txt"), "provider": "cpu", "model type": "nemo_transducer",
		"decoding": "greedy_search", "paraformer": "", "language model": "",
	}
	for name, value := range want {
		if api.settings[name] != value {
			t.Errorf("the engine was handed %s %q, want %q", name, api.settings[name], value)
		}
	}
	if api.threads != 3 {
		t.Errorf("the engine was handed %d threads, want 3", api.threads)
	}
	for range 2 {
		text, err := recognizer.Recognize([]byte("RIFF-sound"))
		if err != nil {
			t.Fatal(err)
		}
		if want := "Check the Versal Deployment for the website and rebase the branch on main."; text != want {
			t.Fatalf("heard %q, want %q", text, want)
		}
	}
	once := "read-wave wave-data[33] create-stream[11] accept-waveform[22 16000] decode[11 22] result-json[22] read-json[44] free-json[44] destroy-stream[22] free-wave[33]"
	if got := strings.Join(api.calls, " "); got != "create-recognizer "+once+" "+once {
		t.Fatalf("the engine was called\n%s\nwant\n%s", got, "create-recognizer "+once+" "+once)
	}
}

func TestEveryFailureFreesWhatTheEngineMadeAndNothingTwice(t *testing.T) {
	options, err := ParseWorkerArguments(workerArguments(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	for failure, frees := range map[string]string{
		"read-wave":     "",
		"wave-data":     "free-wave[33]",
		"create-stream": "free-wave[33]",
		"result-json":   "destroy-stream[22] free-wave[33]",
		"read-json":     "free-json[44] destroy-stream[22] free-wave[33]",
		"not-json":      "free-json[44] destroy-stream[22] free-wave[33]",
	} {
		api := newFakeSherpaAPI(t, failure)
		recognizer, err := newSherpaRecognizer(options, api)
		if err != nil {
			t.Fatal(err)
		}
		if text, err := recognizer.Recognize([]byte("RIFF-sound")); text != "" || err == nil {
			t.Fatalf("%s: answered %q, %v", failure, text, err)
		}
		var freed []string
		for _, call := range api.calls {
			if strings.HasPrefix(call, "free-") || strings.HasPrefix(call, "destroy-") {
				freed = append(freed, call)
			}
		}
		if got := strings.Join(freed, " "); got != frees {
			t.Fatalf("%s: freed %q, want %q", failure, got, frees)
		}
	}
}

func TestAModelTheEngineCannotLoadIsReported(t *testing.T) {
	if _, err := newSherpaRecognizer(WorkerOptions{}, newFakeSherpaAPI(t, "create-recognizer")); err == nil || !strings.Contains(err.Error(), "could not load the model") {
		t.Fatalf("a model that could not be loaded answered %v", err)
	}
}
