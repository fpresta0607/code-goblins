package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/voice"
)

type fakeVoiceRecognizer struct {
	sounds   [][]byte
	closeErr error
	closes   int
}

func (recognizer *fakeVoiceRecognizer) Recognize(sound []byte) (string, error) {
	recognizer.sounds = append(recognizer.sounds, append([]byte(nil), sound...))
	if string(sound) == "RIFF-error" {
		return "", errors.New("visible recognition failure")
	}
	return "heard " + string(sound), nil
}

func (recognizer *fakeVoiceRecognizer) Close() error {
	recognizer.closes++
	return recognizer.closeErr
}

func voiceWorkerArguments(t *testing.T) []string {
	t.Helper()
	folder := t.TempDir()
	return []string{
		filepath.Join(folder, voice.WORKER_LIBRARY),
		"--num-threads=2",
		"--encoder=" + filepath.Join(folder, "encoder.onnx"),
		"--decoder=" + filepath.Join(folder, "decoder.onnx"),
		"--joiner=" + filepath.Join(folder, "joiner.onnx"),
		"--tokens=" + filepath.Join(folder, "tokens.txt"),
		"--model-type=nemo_transducer",
	}
}

func voiceWorkerInput(t *testing.T, sounds ...string) *bytes.Buffer {
	t.Helper()
	var input bytes.Buffer
	for _, sound := range sounds {
		if err := binary.Write(&input, binary.LittleEndian, uint32(len(sound))); err != nil {
			t.Fatal(err)
		}
		input.WriteString(sound)
	}
	return &input
}

func voiceWorkerReply(t *testing.T, output *bytes.Buffer) (text, failure string) {
	t.Helper()
	var size uint32
	if err := binary.Read(output, binary.LittleEndian, &size); err != nil {
		t.Fatal(err)
	}
	if size == 0 || size > voice.MAX_REPLY_BYTES {
		t.Fatalf("invalid reply length %d", size)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(output, data); err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Text  string
		Error string
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		t.Fatal(err)
	}
	return reply.Text, reply.Error
}

func TestVoiceWorkerDispatchPrecedesHomeAndLauncherInitialization(t *testing.T) {
	arguments := voiceWorkerArguments(t)
	opens := 0
	command := commandRuntime{
		goblins: true,
		resolveHome: func() (home.Home, error) {
			t.Fatal("voice-worker resolved a home or entered the launcher")
			return home.Home{}, nil
		},
		openVoiceWorker: func(options voice.WorkerOptions) (voice.WorkerRecognizer, error) {
			opens++
			if options.Library != arguments[0] {
				t.Fatalf("hidden dispatch changed the library: %+v", options)
			}
			return nil, errors.New("owned worker opening stopped before stdin")
		},
	}
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime(append([]string{"voice-worker"}, arguments...), &stdout, &stderr, command)
	if exit != 1 || opens != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "owned worker opening stopped before stdin") {
		t.Fatalf("hidden dispatch returned %d, opens %d, %q, %q", exit, opens, stdout.String(), stderr.String())
	}
	stderr.Reset()
	if exit := run([]string{"voice-worker", "invalid"}, &stdout, &stderr); exit != 2 || !strings.Contains(stderr.String(), "voice-worker requires") {
		t.Fatalf("actual hidden dispatch did not reject arguments before initialization: %d, %q", exit, stderr.String())
	}
	if strings.Contains(usage, "voice-worker") {
		t.Fatal("the owned worker leaf became a public launcher command")
	}
}

func TestVoiceWorkerRejectsArgumentsBeforeOpeningTheEngine(t *testing.T) {
	valid := voiceWorkerArguments(t)
	for _, arguments := range [][]string{nil, valid[:6], append(append([]string(nil), valid...), "--extra=value")} {
		var stdout, stderr bytes.Buffer
		opens := 0
		exit := runVoiceWorker(arguments, bytes.NewReader(nil), &stdout, &stderr, func(voice.WorkerOptions) (voice.WorkerRecognizer, error) {
			opens++
			return nil, errors.New("must not open")
		})
		if exit != 2 || opens != 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "voice-worker requires") {
			t.Fatalf("invalid arguments returned %d, opens %d, %q, %q", exit, opens, stdout.String(), stderr.String())
		}
	}
}

func TestVoiceWorkerReusesTheEngineForExactPipeJobsAndClosesAtEOF(t *testing.T) {
	arguments := voiceWorkerArguments(t)
	recognizer := &fakeVoiceRecognizer{}
	opens := 0
	var stdout, stderr bytes.Buffer
	exit := runVoiceWorker(arguments, voiceWorkerInput(t, "RIFF-first", "RIFF-second"), &stdout, &stderr, func(options voice.WorkerOptions) (voice.WorkerRecognizer, error) {
		opens++
		return recognizer, nil
	})
	if exit != 0 || opens != 1 || recognizer.closes != 1 || stderr.Len() != 0 || len(recognizer.sounds) != 2 {
		t.Fatalf("pipe leaf returned %d, opens %d, closes %d, jobs %q, error %q", exit, opens, recognizer.closes, recognizer.sounds, stderr.String())
	}
	for index, sound := range []string{"RIFF-first", "RIFF-second"} {
		text, failure := voiceWorkerReply(t, &stdout)
		if string(recognizer.sounds[index]) != sound || text != "heard "+sound || failure != "" {
			t.Fatalf("job %d changed bytes or reply: %q, %q, %q", index, recognizer.sounds[index], text, failure)
		}
	}
	if stdout.Len() != 0 {
		t.Fatal("the leaf duplicated a reply")
	}
}

func TestVoiceWorkerKeepsRecognitionFailuresVisibleAndDrainsTheNextJob(t *testing.T) {
	recognizer := &fakeVoiceRecognizer{}
	var stdout, stderr bytes.Buffer
	exit := runVoiceWorker(voiceWorkerArguments(t), voiceWorkerInput(t, "RIFF-error", "RIFF-next"), &stdout, &stderr, func(voice.WorkerOptions) (voice.WorkerRecognizer, error) { return recognizer, nil })
	text, failure := voiceWorkerReply(t, &stdout)
	if text != "" || failure != "visible recognition failure" {
		t.Fatalf("failure lost its words: %q, %q", text, failure)
	}
	text, failure = voiceWorkerReply(t, &stdout)
	if exit != 0 || recognizer.closes != 1 || len(recognizer.sounds) != 2 || text != "heard RIFF-next" || failure != "" || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("next job or teardown changed: exit %d, closes %d, sounds %q, text %q, failure %q, stderr %q", exit, recognizer.closes, recognizer.sounds, text, failure, stderr.String())
	}
}

func TestVoiceWorkerReportsOpenProtocolAndCloseFailures(t *testing.T) {
	arguments := voiceWorkerArguments(t)
	for _, failure := range []struct {
		name string
		input []byte
		openErr, closeErr error
		wantCalls, wantCloses int
	}{
		{"open", nil, errors.New("visible open failure"), nil, 0, 0},
		{"protocol", []byte{1, 0}, nil, nil, 0, 1},
		{"missing payload", []byte{1, 0, 0, 0}, nil, nil, 0, 1},
		{"close", nil, nil, errors.New("visible close failure"), 0, 1},
	} {
		recognizer := &fakeVoiceRecognizer{closeErr: failure.closeErr}
		var stdout, stderr bytes.Buffer
		exit := runVoiceWorker(arguments, bytes.NewReader(failure.input), &stdout, &stderr, func(voice.WorkerOptions) (voice.WorkerRecognizer, error) { return recognizer, failure.openErr })
		if exit != 1 || stdout.Len() != 0 || stderr.Len() == 0 || recognizer.closes != failure.wantCloses || len(recognizer.sounds) != failure.wantCalls {
			t.Fatalf("%s lost its error or resource disposition: exit %d, closes %d, jobs %q, error %q", failure.name, exit, recognizer.closes, recognizer.sounds, stderr.String())
		}
	}
}

func TestDictationEngineReturnsNoOwnerWhenSettingsAreInvalid(t *testing.T) {
	folder := t.TempDir()
	if err := os.Mkdir(filepath.Join(folder, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "config", "voice.json"), []byte("invalid settings"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if engine := dictationEngine(home.Home{Root: folder}, &stderr); engine != nil || !strings.Contains(stderr.String(), "dictation:") {
		t.Fatalf("bad settings created an owner or hid the failure: %v, %q", engine, stderr.String())
	}
}
