package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestVoiceWorkerIsHiddenAndRefusesWhatItDoesNotTake(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exit := run([]string{"voice-worker", "engine.dll"}, &stdout, &stderr); exit != 2 || !strings.Contains(stderr.String(), "voice-worker needs") {
		t.Fatalf("voice-worker with a relative library answered %d, %q", exit, stderr.String())
	}
	if strings.Contains(usage, "voice-worker") {
		t.Fatal("the usage lists voice-worker, which only cfo serve starts")
	}
}

func TestVoiceWorkerSaysWhenItCannotLoadTheEngine(t *testing.T) {
	folder := t.TempDir()
	arguments := []string{
		"voice-worker", filepath.Join(folder, "sherpa-onnx-c-api.dll"),
		"--num-threads=2", "--model-type=nemo_transducer",
		"--encoder=" + filepath.Join(folder, "encoder.onnx"), "--decoder=" + filepath.Join(folder, "decoder.onnx"),
		"--joiner=" + filepath.Join(folder, "joiner.onnx"), "--tokens=" + filepath.Join(folder, "tokens.txt"),
	}
	var stdout, stderr bytes.Buffer
	if exit := run(arguments, &stdout, &stderr); exit != 1 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "voice-worker: ") {
		t.Fatalf("voice-worker with no engine answered %d, %q, %q", exit, stdout.String(), stderr.String())
	}
}

// A home whose dictation settings cannot be read gets no engine at all,
// rather than one the board would try and fail to dictate with.
func TestDictationEngineIsNoneWhenTheSettingsCannotBeRead(t *testing.T) {
	folder := t.TempDir()
	if err := os.Mkdir(filepath.Join(folder, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "config", "voice.json"), []byte("not settings"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if engine := dictationEngine(home.Home{Root: folder}, &stderr); engine != nil || !strings.Contains(stderr.String(), "dictation:") {
		t.Fatalf("settings that cannot be read gave %v, %q", engine, stderr.String())
	}
}
