package voice

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSherpaAPI counts the engine's calls and fails the one named failure.
// Its result is testdata/sherpa-onnx-1.13.8-stdout.txt, what the pinned
// engine gave for one spoken line, as it was captured.
type fakeSherpaAPI struct {
	failure string
	result  []byte
	config  *sherpaOfflineRecognizerConfig
	calls   map[string]int
	sounds  []string
}

func newFakeSherpaAPI(t *testing.T, failure string) *fakeSherpaAPI {
	t.Helper()
	result, err := os.ReadFile(filepath.Join("testdata", "sherpa-onnx-1.13.8-stdout.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return &fakeSherpaAPI{failure: failure, result: result, calls: map[string]int{}}
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

func (api *fakeSherpaAPI) readWave(sound []byte) uintptr {
	api.sounds = append(api.sounds, string(sound))
	return api.called("read-wave")
}

func (api *fakeSherpaAPI) waveData(uintptr) (sherpaWave, error) {
	if api.called("wave-data") == 0 {
		return sherpaWave{}, errors.New("unreadable")
	}
	return sherpaWave{Samples: 1, SampleRate: 16000, NumSamples: 4}, nil
}

func (api *fakeSherpaAPI) freeWave(uintptr)                   { api.called("free-wave") }
func (api *fakeSherpaAPI) createStream(uintptr) uintptr       { return api.called("create-stream") }
func (api *fakeSherpaAPI) destroyStream(uintptr)              { api.called("destroy-stream") }
func (api *fakeSherpaAPI) acceptWaveform(uintptr, sherpaWave) { api.called("accept-waveform") }
func (api *fakeSherpaAPI) decode(uintptr, uintptr)            { api.called("decode") }
func (api *fakeSherpaAPI) resultJSON(uintptr) uintptr         { return api.called("result-json") }
func (api *fakeSherpaAPI) freeJSON(uintptr)                   { api.called("free-json") }

func (api *fakeSherpaAPI) readJSON(uintptr) ([]byte, error) {
	if api.called("read-json") == 0 {
		return nil, errors.New("unreadable")
	}
	if api.failure == "not-json" {
		return []byte("Done!"), nil
	}
	return api.result, nil
}

func workerArguments(folder string) []string {
	return []string{
		filepath.Join(folder, "sherpa-onnx-c-api.dll"),
		"--num-threads=2",
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
	if options.Library != valid[0] || options.Threads != 2 || options.ModelType != "nemo_transducer" || options.Joiner != strings.TrimPrefix(valid[4], "--joiner=") {
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
	options, err := ParseWorkerArguments(workerArguments(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	api := newFakeSherpaAPI(t, "")
	recognizer, err := newSherpaRecognizer(options, api)
	if err != nil {
		t.Fatal(err)
	}
	for _, sound := range []string{"RIFF-first", "RIFF-second"} {
		text, err := recognizer.Recognize([]byte(sound))
		if err != nil {
			t.Fatal(err)
		}
		if want := "Check the Versal Deployment for the website and rebase the branch on main."; text != want {
			t.Fatalf("heard %q, want %q", text, want)
		}
	}
	if api.calls["create-recognizer"] != 1 || strings.Join(api.sounds, " ") != "RIFF-first RIFF-second" {
		t.Fatalf("the engine loaded %d times for %q", api.calls["create-recognizer"], api.sounds)
	}
	for _, name := range []string{"free-wave", "destroy-stream", "decode", "free-json"} {
		if api.calls[name] != 2 {
			t.Errorf("%s was called %d times for two sounds", name, api.calls[name])
		}
	}
	config := api.config
	if config.FeatConfig.SampleRate != 16000 || config.FeatConfig.FeatureDim != 80 || config.ModelConfig.NumThreads != 2 || config.ModelConfig.Debug != 0 || config.ModelConfig.Paraformer.Model != nil || config.LmConfig.Model != nil {
		t.Fatalf("the engine was configured with %+v", config)
	}
}

func TestEveryFailureFreesWhatTheEngineMadeAndNothingTwice(t *testing.T) {
	options, err := ParseWorkerArguments(workerArguments(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []struct {
		name                 string
		wave, stream, result int
	}{
		{"read-wave", 0, 0, 0},
		{"wave-data", 1, 0, 0},
		{"create-stream", 1, 0, 0},
		{"result-json", 1, 1, 0},
		{"read-json", 1, 1, 1},
		{"not-json", 1, 1, 1},
	} {
		api := newFakeSherpaAPI(t, failure.name)
		recognizer, err := newSherpaRecognizer(options, api)
		if err != nil {
			t.Fatal(err)
		}
		if text, err := recognizer.Recognize([]byte("RIFF-sound")); text != "" || err == nil {
			t.Fatalf("%s: answered %q, %v", failure.name, text, err)
		}
		if api.calls["free-wave"] != failure.wave || api.calls["destroy-stream"] != failure.stream || api.calls["free-json"] != failure.result {
			t.Fatalf("%s: freed %v", failure.name, api.calls)
		}
	}
}

func TestAModelTheEngineCannotLoadIsReported(t *testing.T) {
	if _, err := newSherpaRecognizer(WorkerOptions{}, newFakeSherpaAPI(t, "create-recognizer")); err == nil || !strings.Contains(err.Error(), "could not load the model") {
		t.Fatalf("a model that could not be loaded answered %v", err)
	}
}
