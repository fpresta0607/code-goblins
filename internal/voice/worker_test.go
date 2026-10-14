package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type workerFixture struct {
	Role   string
	Record string
}

func writeWorkerFixture(t *testing.T, voice *Voice, role, record string) {
	t.Helper()
	data, err := json.Marshal(workerFixture{Role: role, Record: record})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(voice.folder(voice.Settings.Engine), "worker-fixture.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func standInPipeWorker(arguments []string) int {
	options, err := ParseWorkerArguments(arguments[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	folder := filepath.Dir(options.Library)
	data, err := os.ReadFile(filepath.Join(folder, "worker-fixture.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	var fixture workerFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	isLoaded := false
	err = RunWorker(os.Stdin, os.Stdout, func(sound []byte) (string, error) {
		if !isLoaded {
			file, err := os.OpenFile(filepath.Join(folder, "engine-loads.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return "", err
			}
			_, writeErr := fmt.Fprintln(file, os.Getpid())
			closeErr := file.Close()
			if err := errors.Join(writeErr, closeErr); err != nil {
				return "", err
			}
			isLoaded = true
		}
		run := engineRun{Args: arguments, Bytes: len(sound), Payload: string(sound), Transport: "pipe", PID: os.Getpid(), Environment: os.Environ()}
		data, err := json.Marshal(run)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(fixture.Record, data, 0o600); err != nil {
			return "", err
		}
		switch fixture.Role {
		case "fails":
			return "", errors.New("Failed to read sound")
		case "crashes":
			fmt.Fprintln(os.Stderr, "owned fixture crashed")
			os.Exit(255)
		case "malformed":
			if err := writeFrame(os.Stdout, []byte("not JSON")); err != nil {
				return "", err
			}
		case "waits":
			for {
				if _, err := os.Stat(filepath.Join(folder, "release")); err == nil {
					break
				} else if !os.IsNotExist(err) {
					return "", err
				}
				time.Sleep(10 * time.Millisecond)
			}
			return string(sound), nil
		}
		fmt.Fprintln(os.Stderr, "fixture diagnostics are not words")
		return " Check the \"Vercel\" deployment.", nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	return 0
}

func assertPipeSound(t *testing.T, voice *Voice, run engineRun) {
	t.Helper()
	if run.Sound != "" || run.Transport != "pipe" || len(run.Payload) != run.Bytes {
		t.Fatalf("sound transport was %+v, want exact pipe bytes without a file", run)
	}
	for _, folder := range []string{voice.Dir, voice.folder(voice.Settings.Engine), os.TempDir()} {
		files, err := filepath.Glob(filepath.Join(folder, "cfo-dictation-*.wav"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 0 {
			t.Fatalf("dictation created sound files in %s: %v", folder, files)
		}
	}
}

func waitForWorkerRecord(t *testing.T, record string) engineRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(record)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		var run engineRun
		if err == nil && json.Unmarshal(data, &run) == nil {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("owned worker never received the request")
	return engineRun{}
}

func activeWorker(t *testing.T, voice *Voice) *worker {
	t.Helper()
	voice.mutex.Lock()
	defer voice.mutex.Unlock()
	if voice.worker == nil {
		t.Fatal("the owned worker is absent")
	}
	return voice.worker
}

func assertWorkerReaped(t *testing.T, voice *Voice, owned *worker) {
	t.Helper()
	select {
	case <-owned.exited:
	default:
		t.Fatal("the owned worker was not waited for")
	}
	voice.mutex.Lock()
	defer voice.mutex.Unlock()
	if voice.worker != nil {
		t.Fatal("the Voice retained its retired worker")
	}
}

func TestCancelingDictationReapsOnlyItsWorker(t *testing.T) {
	voice, record := engine(t, "waits", 8<<30)
	peer, _ := engine(t, "hears", 8<<30)
	if _, err := peer.Recognize(context.Background(), []byte("RIFF-peer")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	answer := make(chan error, 1)
	go func() {
		text, err := voice.Recognize(ctx, []byte("RIFF-blocked"))
		if text != "" {
			err = fmt.Errorf("canceled dictation returned words: %q", text)
		}
		answer <- err
	}()
	waitForWorkerRecord(t, record)
	owned := activeWorker(t, voice)
	cancel()
	if err := <-answer; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled recognition answered %v", err)
	}
	assertWorkerReaped(t, voice, owned)
	if text, err := peer.Recognize(context.Background(), []byte("RIFF-peer-again")); err != nil || text == "" {
		t.Fatalf("canceling another Voice damaged its peer: %q, %v", text, err)
	}
}

func TestCanceledQueuedDictationNeverReachesTheWorker(t *testing.T) {
	voice, record := engine(t, "waits", 8<<30)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	active := make(chan error, 1)
	go func() { _, err := voice.Recognize(ctx, []byte("RIFF-first")); active <- err }()
	first := waitForWorkerRecord(t, record)
	queued, cancelQueued := context.WithCancel(context.Background())
	cancelQueued()
	if _, err := voice.Recognize(queued, []byte("RIFF-queued")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled queued recognition answered %v", err)
	}
	if run := ran(t, record); run.Payload != first.Payload || run.PID != first.PID {
		t.Fatalf("a canceled queued job changed the active request: %+v", run)
	}
	select {
	case <-activeWorker(t, voice).exited:
		t.Fatal("a queued cancellation killed the active worker")
	default:
	}
	cancel()
	if err := <-active; !errors.Is(err, context.Canceled) {
		t.Fatalf("active cancellation answered %v", err)
	}
}

func TestConcurrentDictationsShareOneSerializedWorker(t *testing.T) {
	voice, record := engine(t, "waits", 8<<30)
	answers := make(chan error, 2)
	go func() {
		text, err := voice.Recognize(context.Background(), []byte("RIFF-first"))
		if text != "RIFF-first" {
			t.Errorf("first request received %q", text)
		}
		answers <- err
	}()
	first := waitForWorkerRecord(t, record)
	owned := activeWorker(t, voice)
	go func() {
		text, err := voice.Recognize(context.Background(), []byte("RIFF-second"))
		if text != "RIFF-second" {
			t.Errorf("second request received %q", text)
		}
		answers <- err
	}()
	if err := os.WriteFile(filepath.Join(voice.folder(voice.Settings.Engine), "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-answers; err != nil {
			t.Fatal(err)
		}
	}
	second := ran(t, record)
	if second.Payload != "RIFF-second" || second.PID != first.PID || activeWorker(t, voice) != owned {
		t.Fatalf("concurrent requests did not share one serialized worker: %+v", second)
	}
}

func TestClosingVoiceDropsActiveAndQueuedWords(t *testing.T) {
	voice, record := engine(t, "waits", 8<<30)
	answers := make(chan error, 2)
	go func() {
		text, err := voice.Recognize(context.Background(), []byte("RIFF-first"))
		if text != "" {
			t.Errorf("closed Voice returned words: %q", text)
		}
		answers <- err
	}()
	waitForWorkerRecord(t, record)
	owned := activeWorker(t, voice)
	go func() {
		text, err := voice.Recognize(context.Background(), []byte("RIFF-second"))
		if text != "" {
			t.Errorf("closed Voice returned queued words: %q", text)
		}
		answers <- err
	}()
	if err := voice.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-answers; err == nil {
			t.Fatal("closed recognition succeeded")
		}
	}
	assertWorkerReaped(t, voice, owned)
	if _, err := voice.Recognize(context.Background(), []byte("RIFF-later")); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("a closed Voice restarted: %v", err)
	}
}

func TestLowMemoryRetiresTheWarmWorker(t *testing.T) {
	for _, free := range [][2]uint64{{Room - 1, 8 << 30}, {8 << 30, Room - 1}} {
		voice, _ := engine(t, "hears", 8<<30)
		if _, err := voice.Recognize(context.Background(), []byte("RIFF-first")); err != nil {
			t.Fatal(err)
		}
		owned := activeWorker(t, voice)
		voice.Memory = func() (uint64, uint64, error) { return free[0], free[1], nil }
		var refused NoRoom
		if _, err := voice.Recognize(context.Background(), []byte("RIFF-next")); !errors.As(err, &refused) {
			t.Fatalf("low memory answered %v", err)
		}
		assertWorkerReaped(t, voice, owned)
	}
}

func TestWarmWorkerRetiresOnReadinessAndMemoryErrors(t *testing.T) {
	for _, failure := range []string{"memory", "readiness"} {
		voice, _ := engine(t, "hears", 8<<30)
		if _, err := voice.Recognize(context.Background(), []byte("RIFF-first")); err != nil {
			t.Fatal(err)
		}
		owned := activeWorker(t, voice)
		expected := "read the machine's memory"
		if failure == "memory" {
			voice.Memory = func() (uint64, uint64, error) { return 0, 0, errors.New("fixture memory read failed") }
		} else {
			expected = "not fetched"
			if err := os.Remove(filepath.Join(voice.folder(voice.Settings.Model), "verified.json")); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := voice.Recognize(context.Background(), []byte("RIFF-next")); err == nil || !strings.Contains(err.Error(), expected) {
			t.Fatalf("%s failure answered %v", failure, err)
		}
		assertWorkerReaped(t, voice, owned)
	}
}

func TestDictationWorkerProtocolErrorsRetireTheChild(t *testing.T) {
	for _, role := range []string{"crashes", "malformed"} {
		voice, record := engine(t, role, 8<<30)
		text, err := voice.Recognize(context.Background(), []byte("RIFF-failed"))
		expected := "owned fixture crashed"
		if role == "malformed" {
			expected = "read dictation worker reply"
		}
		if err == nil || text != "" || !strings.Contains(err.Error(), expected) {
			t.Fatalf("%s returned %q, %v", role, text, err)
		}
		if run := ran(t, record); run.Payload != "RIFF-failed" {
			t.Fatalf("%s did not receive the actual request: %+v", role, run)
		}
		voice.mutex.Lock()
		isRetained := voice.worker != nil
		voice.mutex.Unlock()
		if isRetained {
			t.Fatalf("%s retained the failed child", role)
		}
		writeWorkerFixture(t, voice, "hears", record)
		if text, err := voice.Recognize(context.Background(), []byte("RIFF-next")); err != nil || text == "" {
			t.Fatalf("a fresh request could not start after %s: %q, %v", role, text, err)
		}
	}
}

func TestRetiringAWorkerAfterWaitReleasesItsOwnership(t *testing.T) {
	for _, shouldClose := range []bool{false, true} {
		voice, _ := engine(t, "hears", 8<<30)
		owned, err := voice.startWorker()
		if err != nil {
			t.Fatal(err)
		}
		voice.mutex.Lock()
		voice.worker = owned
		voice.mutex.Unlock()
		if err := owned.input.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-owned.exited:
		case <-time.After(5 * time.Second):
			t.Fatal("the owned pipe worker did not finish after EOF")
		}
		if owned.command.ProcessState == nil || owned.command.ProcessState.ExitCode() != 0 {
			t.Fatalf("the completed-Wait premise failed: %v", owned.command.ProcessState)
		}
		if shouldClose {
			err = voice.Close()
		} else {
			err = voice.retire(owned)
		}
		if err != nil {
			t.Fatalf("retiring an already waited worker, close=%t: %v", shouldClose, err)
		}
		assertWorkerReaped(t, voice, owned)
		if owned.shutdownErr != nil {
			t.Fatalf("an already waited worker retained a shutdown failure: %v", owned.shutdownErr)
		}
		if err := voice.Close(); err != nil {
			t.Fatalf("closing the retired Voice: %v", err)
		}
	}
}

func TestDictationWorkerScrubsInheritedCredentials(t *testing.T) {
	for _, name := range []string{"CODEX_THREAD_ID", "CFO_HOME", "CFO_STATE_OVERRIDE", "CFO_ROLE", "NO_MISTAKES_GATE", "GITHUB_TOKEN", "VOICE_TEST_ENGINE"} {
		t.Setenv(name, "owned-fixture-value")
	}
	voice, record := engine(t, "hears", 8<<30)
	if _, err := voice.Recognize(context.Background(), []byte("RIFF-env")); err != nil {
		t.Fatal(err)
	}
	for _, entry := range ran(t, record).Environment {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "SYSTEMROOT", "WINDIR", "SYSTEMDRIVE", "COMSPEC", "PATH", "PATHEXT", "TEMP", "TMP":
		default:
			t.Fatalf("the worker inherited unapproved variable %s", name)
		}
	}
}

func TestIdleDictationWorkerIsReapedAfter120Seconds(t *testing.T) {
	voice, _ := engine(t, "hears", 8<<30)
	if _, err := voice.Recognize(context.Background(), []byte("RIFF-first")); err != nil {
		t.Fatal(err)
	}
	owned := activeWorker(t, voice)
	if WORKER_IDLE != 120*time.Second {
		t.Fatalf("idle policy is %s, want 120 seconds", WORKER_IDLE)
	}
	select {
	case <-owned.exited:
		t.Fatal("the worker was already retired")
	default:
	}
	time.Sleep(5 * time.Second)
	released := time.Now()
	if _, err := voice.Recognize(context.Background(), []byte("RIFF-reuse")); err != nil {
		t.Fatal(err)
	}
	if activeWorker(t, voice) != owned {
		t.Fatal("the next request did not reuse the warm worker")
	}
	deadline := time.NewTimer(130 * time.Second)
	defer deadline.Stop()
	select {
	case <-owned.exited:
	case <-deadline.C:
		t.Fatal("the worker stayed alive beyond its idle cutoff")
	}
	if elapsed := time.Since(released); elapsed < WORKER_IDLE {
		t.Fatalf("worker retired after %s since its last request began, before the cutoff", elapsed)
	}
	voice.mutex.Lock()
	voice.mutex.Unlock()
	assertWorkerReaped(t, voice, owned)
	if _, err := voice.Recognize(context.Background(), []byte("RIFF-after-idle")); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRejectsInvalidFramesWithoutRecognition(t *testing.T) {
	var oversized [4]byte
	binary.LittleEndian.PutUint32(oversized[:], MAX_SOUND_BYTES+1)
	for _, input := range [][]byte{{0, 0, 0, 0}, {1, 0}, {1, 0, 0, 0}, {2, 0, 0, 0, 1}, oversized[:]} {
		isCalled := false
		err := RunWorker(bytes.NewReader(input), io.Discard, func([]byte) (string, error) {
			isCalled = true
			return "unexpected", nil
		})
		if err == nil || isCalled {
			t.Fatalf("invalid frame %v answered %v, recognizer called=%t", input, err, isCalled)
		}
	}
}

func TestWorkerKeepsServingAfterARecognitionError(t *testing.T) {
	var input, output bytes.Buffer
	for _, sound := range []string{"first", "second"} {
		if err := writeFrame(&input, []byte(sound)); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	if err := RunWorker(&input, &output, func(sound []byte) (string, error) {
		count++
		if string(sound) == "first" {
			return "", errors.New("Failed to read sound")
		}
		return "second words", nil
	}); err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		data, err := readFrame(&output, MAX_REPLY_BYTES)
		if err != nil {
			t.Fatal(err)
		}
		var reply workerReply
		if err := json.Unmarshal(data, &reply); err != nil {
			t.Fatal(err)
		}
		if index == 0 && reply.Error != "Failed to read sound" || index == 1 && reply.Text != "second words" {
			t.Fatalf("reply %d was %+v", index, reply)
		}
	}
	if count != 2 || output.Len() != 0 {
		t.Fatalf("served %d requests with %d extra output bytes", count, output.Len())
	}
}

func TestDictationRejectsEmptyAndOversizedSoundBeforeStartingWorker(t *testing.T) {
	voice, record := engine(t, "hears", 8<<30)
	for _, sound := range [][]byte{nil, make([]byte, MAX_SOUND_BYTES+1)} {
		if _, err := voice.Recognize(context.Background(), sound); err == nil {
			t.Fatalf("accepted %d sound bytes", len(sound))
		}
		if _, err := os.Stat(record); !os.IsNotExist(err) {
			t.Fatalf("invalid sound reached the worker: %v", err)
		}
	}
}
