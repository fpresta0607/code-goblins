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

// workerFixture tells the stand-in worker how to answer: its role, and the
// file it records each sound it is handed in.
type workerFixture struct {
	Role   string
	Record string
}

type engineRun struct {
	Args        []string `json:"args"`
	Bytes       int      `json:"bytes"`
	Payload     string   `json:"payload"`
	PID         int      `json:"pid"`
	Environment []string `json:"environment"`
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

// standInWorker is this test binary started as `voice-worker`. Beside a
// worker-fixture.json it answers as its role says; without one it is the
// real worker, which the live test runs.
func standInWorker(arguments []string) int {
	options, err := ParseWorkerArguments(arguments)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	folder := filepath.Dir(options.Library)
	data, err := os.ReadFile(filepath.Join(folder, "worker-fixture.json"))
	if errors.Is(err, os.ErrNotExist) {
		recognize, err := OpenWorker(options)
		if err == nil {
			err = RunWorker(os.Stdin, os.Stdout, recognize)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	var fixture workerFixture
	if err == nil {
		err = json.Unmarshal(data, &fixture)
	}
	if err == nil {
		err = appendLine(filepath.Join(folder, "engine-loads.txt"), fmt.Sprint(os.Getpid()))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	answered := 0
	err = RunWorker(os.Stdin, os.Stdout, func(sound []byte) (string, error) {
		answered++
		if fixture.Role == "dies silently later" && answered > 1 {
			os.Exit(255)
		}
		run, err := json.Marshal(engineRun{Args: os.Args[1:], Bytes: len(sound), Payload: string(sound), PID: os.Getpid(), Environment: os.Environ()})
		if err == nil {
			err = os.WriteFile(fixture.Record, run, 0o600)
		}
		if err != nil {
			return "", err
		}
		switch fixture.Role {
		case "fails":
			return "", errors.New("Failed to read sound")
		case "crashes":
			fmt.Fprintln(os.Stderr, "the stand-in engine crashed")
			os.Exit(255)
		case "malformed":
			return "", writeFrame(os.Stdout, []byte("not JSON"))
		case "waits":
			for {
				if _, err := os.Stat(filepath.Join(folder, "release")); err == nil {
					return string(sound), nil
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		fmt.Fprintln(os.Stderr, "what the engine says about its run is not words")
		return ` Check the "Vercel" deployment.`, nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	return 0
}

func appendLine(file, line string) error {
	out, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, line)
	return errors.Join(err, out.Close())
}

func ran(t *testing.T, record string) engineRun {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the engine did not run: %v", err)
	}
	var run engineRun
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	return run
}

// loads counts the times a stand-in worker started for voice.
func loads(t *testing.T, voice *Voice) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(voice.folder(voice.Settings.Engine), "engine-loads.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(data)))
}

// waitForRun waits until the stand-in worker has been handed a sound.
func waitForRun(t *testing.T, record string) engineRun {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		data, err := os.ReadFile(record)
		var run engineRun
		if err == nil && json.Unmarshal(data, &run) == nil {
			return run
		}
	}
	t.Fatal("the worker was never handed the sound")
	return engineRun{}
}

// current is voice's worker, read while holding the turn.
func current(t *testing.T, voice *Voice) *worker {
	t.Helper()
	if err := voice.take(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer voice.give()
	return voice.worker
}

func exited(w *worker) bool {
	select {
	case <-w.exited:
		return true
	default:
		return false
	}
}

func release(t *testing.T, voice *Voice) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(voice.folder(voice.Settings.Engine), "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSuccessiveDictationsReuseOneEngineLoad(t *testing.T) {
	voice, record := engine(t, "hears", 8<<30)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := voice.Recognize(canceled, []byte("RIFF-canceled")); !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled dictation answered %v", err)
	}
	if loads(t, voice) != 0 {
		t.Fatal("a canceled dictation started the engine")
	}
	for _, sound := range []string{"RIFF-first", "RIFF-second"} {
		text, err := voice.Recognize(context.Background(), []byte(sound))
		if err != nil {
			t.Fatal(err)
		}
		if text != `Check the "Vercel" deployment.` {
			t.Fatalf("heard %q", text)
		}
		if run := ran(t, record); run.Payload != sound {
			t.Fatalf("the engine was handed %q, want %q", run.Payload, sound)
		}
	}
	if count := loads(t, voice); count != 1 {
		t.Fatalf("two dictations loaded the engine %d times, want once", count)
	}
}

func TestDictationsWaitTheirTurnOnTheOneEngine(t *testing.T) {
	voice, record := engine(t, "waits", 8<<30)
	answers := make(chan error, 2)
	for _, sound := range []string{"RIFF-first", "RIFF-second"} {
		go func() {
			text, err := voice.Recognize(context.Background(), []byte(sound))
			if err == nil && text != sound {
				err = fmt.Errorf("%s was answered with %q", sound, text)
			}
			answers <- err
		}()
		if sound == "RIFF-first" {
			waitForRun(t, record)
		}
	}
	release(t, voice)
	for range 2 {
		if err := <-answers; err != nil {
			t.Fatal(err)
		}
	}
	if count := loads(t, voice); count != 1 {
		t.Fatalf("two dictations at once loaded the engine %d times, want once", count)
	}
}

func TestAbandoningADictationEndsItsEngineAndNoOther(t *testing.T) {
	voice, record := engine(t, "waits", 8<<30)
	peer, _ := engine(t, "hears", 8<<30)
	if _, err := peer.Recognize(context.Background(), []byte("RIFF-peer")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	answer := make(chan error, 1)
	go func() {
		_, err := voice.Recognize(ctx, []byte("RIFF-abandoned"))
		answer <- err
	}()
	waitForRun(t, record)
	// A dictation queued behind it and abandoned while it waits ends nothing.
	queued, cancelQueued := context.WithCancel(context.Background())
	cancelQueued()
	if _, err := voice.Recognize(queued, []byte("RIFF-queued")); !errors.Is(err, context.Canceled) {
		t.Fatalf("an abandoned queued dictation answered %v", err)
	}
	if run := ran(t, record); run.Payload != "RIFF-abandoned" {
		t.Fatalf("the abandoned queued dictation reached the engine: %+v", run)
	}
	cancel()
	if err := <-answer; !errors.Is(err, context.Canceled) {
		t.Fatalf("the abandoned dictation answered %v", err)
	}
	if current(t, voice) != nil {
		t.Fatal("the abandoned dictation's engine was kept")
	}
	if text, err := peer.Recognize(context.Background(), []byte("RIFF-peer-again")); err != nil || text == "" || loads(t, peer) != 1 {
		t.Fatalf("abandoning one dictation disturbed another engine: %q, %v", text, err)
	}
}

func TestClosingEndsTheEngineAndRefusesDictation(t *testing.T) {
	voice, record := engine(t, "waits", 8<<30)
	answers := make(chan error, 2)
	for _, sound := range []string{"RIFF-first", "RIFF-second"} {
		go func() {
			text, err := voice.Recognize(context.Background(), []byte(sound))
			if err == nil {
				err = fmt.Errorf("a dictation was answered %q after dictation closed", text)
			} else if !errors.Is(err, errClosed) {
				err = fmt.Errorf("a dictation answered %w, want that dictation is closed", err)
			} else {
				err = nil
			}
			answers <- err
		}()
		if sound == "RIFF-first" {
			waitForRun(t, record)
		}
	}
	voice.Close()
	for range 2 {
		if err := <-answers; err != nil {
			t.Fatal(err)
		}
	}
	if voice.worker != nil {
		t.Fatal("closing kept the engine")
	}
	if _, err := voice.Recognize(context.Background(), []byte("RIFF-later")); !errors.Is(err, errClosed) {
		t.Fatalf("a dictation after closing answered %v", err)
	}
	voice.Close()
}

func TestTheEngineIsEndedWhenThereIsNoRoomOrNothingToRun(t *testing.T) {
	for _, failure := range []string{"memory under the mark", "commit under the mark", "memory unreadable", "model removed"} {
		voice, _ := engine(t, "hears", 8<<30)
		if _, err := voice.Recognize(context.Background(), []byte("RIFF-first")); err != nil {
			t.Fatal(err)
		}
		loaded := current(t, voice)
		want := "1 GB"
		switch failure {
		case "memory under the mark":
			voice.Memory = func() (uint64, uint64, error) { return Room - 1, 8 << 30, nil }
		case "commit under the mark":
			voice.Memory = func() (uint64, uint64, error) { return 8 << 30, Room - 1, nil }
		case "memory unreadable":
			voice.Memory = func() (uint64, uint64, error) { return 0, 0, errors.New("no reading") }
			want = "read the machine's memory"
		case "model removed":
			want = "not fetched"
			if err := os.Remove(filepath.Join(voice.folder(voice.Settings.Model), verifiedName)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := voice.Recognize(context.Background(), []byte("RIFF-next")); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: answered %v", failure, err)
		}
		if !exited(loaded) || current(t, voice) != nil {
			t.Fatalf("%s: the engine was kept", failure)
		}
	}
}

func TestAnEngineThatBreaksIsEndedAndTheNextDictationStartsAnother(t *testing.T) {
	for role, want := range map[string]string{
		"crashes":   "the stand-in engine crashed",
		"malformed": "its reply could not be read",
	} {
		voice, record := engine(t, role, 8<<30)
		text, err := voice.Recognize(context.Background(), []byte("RIFF-broken"))
		if err == nil || text != "" || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: answered %q, %v", role, text, err)
		}
		if current(t, voice) != nil {
			t.Fatalf("%s: the broken engine was kept", role)
		}
		writeWorkerFixture(t, voice, "hears", record)
		if text, err := voice.Recognize(context.Background(), []byte("RIFF-next")); err != nil || text == "" {
			t.Fatalf("%s: the next dictation answered %q, %v", role, text, err)
		}
	}
}

// What the engine said during an earlier dictation is not given as the
// reason a later one failed.
func TestABrokenEngineIsReportedWithWhatItSaidThatTimeOnly(t *testing.T) {
	voice, _ := engine(t, "dies silently later", 8<<30)
	if _, err := voice.Recognize(context.Background(), []byte("RIFF-first")); err != nil {
		t.Fatal(err)
	}
	_, err := voice.Recognize(context.Background(), []byte("RIFF-second"))
	if err == nil || strings.Contains(err.Error(), "not words") {
		t.Fatalf("the engine that died answered %v", err)
	}
}

// An engine that exited between dictations, by itself or ended by something
// else, is replaced rather than handed the next sound.
func TestAnEngineThatExitedWhileIdleIsReplaced(t *testing.T) {
	voice, record := engine(t, "hears", 8<<30)
	if _, err := voice.Recognize(context.Background(), []byte("RIFF-first")); err != nil {
		t.Fatal(err)
	}
	first := ran(t, record).PID
	loaded := current(t, voice)
	if err := loaded.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-loaded.exited
	text, err := voice.Recognize(context.Background(), []byte("RIFF-second"))
	if err != nil || text == "" {
		t.Fatalf("the dictation after the engine exited answered %q, %v", text, err)
	}
	if ran(t, record).PID == first {
		t.Fatal("the dictation was handed to the engine that had exited")
	}
	voice.Close()
	if !exited(loaded) {
		t.Fatal("closing did not wait for the engine")
	}
}

func TestAnIdleEngineIsEndedAndTheNextDictationLoadsItAgain(t *testing.T) {
	idle := workerIdle
	workerIdle = 2 * time.Second
	t.Cleanup(func() { workerIdle = idle })
	voice, _ := engine(t, "hears", 8<<30)
	if _, err := voice.Recognize(context.Background(), []byte("RIFF-first")); err != nil {
		t.Fatal(err)
	}
	loaded := current(t, voice)
	// Dictating within the idle time keeps it past the first one's end.
	time.Sleep(1300 * time.Millisecond)
	if _, err := voice.Recognize(context.Background(), []byte("RIFF-second")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1300 * time.Millisecond)
	if exited(loaded) || current(t, voice) != loaded {
		t.Fatal("the engine was ended although it was used within its idle time")
	}
	select {
	case <-loaded.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("the idle engine was never ended")
	}
	if current(t, voice) != nil {
		t.Fatal("the ended engine was kept")
	}
	if _, err := voice.Recognize(context.Background(), []byte("RIFF-third")); err != nil {
		t.Fatal(err)
	}
	if count := loads(t, voice); count != 2 {
		t.Fatalf("the engine was loaded %d times, want twice", count)
	}
}

// An idle time that ran out while a dictation had the turn is out of date by
// the time it gets the turn, and ends nothing.
func TestAnIdleEndADictationOvertookEndsNothing(t *testing.T) {
	idle := workerIdle
	workerIdle = 100 * time.Millisecond
	t.Cleanup(func() { workerIdle = idle })
	voice, _ := engine(t, "hears", 8<<30)
	if _, err := voice.Recognize(context.Background(), []byte("RIFF-first")); err != nil {
		t.Fatal(err)
	}
	if err := voice.take(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	workerIdle = time.Minute
	_, err := voice.exchange(context.Background(), []byte("RIFF-second"))
	voice.give()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if loaded := current(t, voice); loaded == nil || exited(loaded) {
		t.Fatal("an idle end that a dictation overtook ended the engine")
	}
}

func TestTheEngineGetsNoTokenOrSettingOfTheFleet(t *testing.T) {
	for _, name := range []string{"CFO_HOME", "CFO_STATE_OVERRIDE", "CFO_ROLE", "NO_MISTAKES_GATE", "GITHUB_TOKEN", "OPENAI_API_KEY"} {
		t.Setenv(name, "secret")
	}
	voice, record := engine(t, "hears", 8<<30)
	if _, err := voice.Recognize(context.Background(), []byte("RIFF-sound")); err != nil {
		t.Fatal(err)
	}
	for _, entry := range ran(t, record).Environment {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.Contains(" "+strings.Join(workerEnvironment, " ")+" ", " "+strings.ToUpper(name)+" ") {
			t.Errorf("the engine was given %s", name)
		}
	}
}

func TestDictationRefusesNoSoundAndTooMuchBeforeStartingTheEngine(t *testing.T) {
	voice, _ := engine(t, "hears", 8<<30)
	for _, sound := range [][]byte{nil, make([]byte, MAX_SOUND_BYTES+1)} {
		if _, err := voice.Recognize(context.Background(), sound); err == nil {
			t.Fatalf("%d bytes of sound were taken", len(sound))
		}
	}
	if loads(t, voice) != 0 {
		t.Fatal("a refused sound started the engine")
	}
}

func TestTheWorkerRefusesABrokenFrameWithoutRecognising(t *testing.T) {
	var oversized [4]byte
	binary.LittleEndian.PutUint32(oversized[:], MAX_SOUND_BYTES+1)
	for _, input := range [][]byte{{0, 0, 0, 0}, {1, 0}, {1, 0, 0, 0}, {2, 0, 0, 0, 1}, oversized[:]} {
		recognised := false
		err := RunWorker(bytes.NewReader(input), io.Discard, func([]byte) (string, error) {
			recognised = true
			return "words", nil
		})
		if err == nil || recognised {
			t.Fatalf("the frame %v answered %v, recognised %t", input, err, recognised)
		}
	}
}

func TestTheWorkerAnswersEachSoundAndKeepsGoingAfterOneItCannotRecognise(t *testing.T) {
	var input, output bytes.Buffer
	for _, sound := range []string{"first", "second"} {
		if err := writeFrame(&input, []byte(sound)); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunWorker(&input, &output, func(sound []byte) (string, error) {
		if string(sound) == "first" {
			return "", errors.New("Failed to read sound")
		}
		return " second words ", nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []workerReply{{Error: "Failed to read sound"}, {Text: "second words"}} {
		data, err := readFrame(&output, MAX_REPLY_BYTES)
		if err != nil {
			t.Fatal(err)
		}
		var reply workerReply
		if err := json.Unmarshal(data, &reply); err != nil || reply != want {
			t.Fatalf("the worker answered %s, want %+v", data, want)
		}
	}
	if output.Len() != 0 {
		t.Fatalf("the worker wrote %d bytes more", output.Len())
	}
}
