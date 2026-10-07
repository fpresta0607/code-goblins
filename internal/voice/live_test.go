package voice

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// With VOICE_LIVE_DIR naming a folder, this test downloads the engine and the
// model config/voice.json pins, about 126 MB, into that folder, where a
// second run finds them, and recognises a spoken line with them. It is the
// one test here that uses the network, so it runs only when asked.
func TestThePinnedEngineRecognisesASpokenLine(t *testing.T) {
	dir := os.Getenv("VOICE_LIVE_DIR")
	if dir == "" {
		t.Skip("set VOICE_LIVE_DIR to a folder to download the pinned engine and model into")
	}
	settings, err := Load(filepath.Join("..", "..", "config", "voice.json"))
	if err != nil {
		t.Fatal(err)
	}
	voice := &Voice{Settings: settings, Dir: dir, Memory: func() (uint64, uint64, error) { return 8 << 30, 8 << 30, nil }}
	var arrived, pinned int64
	if err := voice.Fetch(context.Background(), func(done, total int64) { arrived, pinned = done, total }); err != nil {
		t.Fatal(err)
	}
	if arrived != pinned {
		t.Fatalf("the fetch ended at %d of the %d pinned bytes", arrived, pinned)
	}
	t.Logf("%s; downloaded now: %d bytes", voice.Summary(), arrived)
	sound, err := os.ReadFile(filepath.Join("testdata", "open-the-pull-request.wav"))
	if err != nil {
		t.Fatal(err)
	}
	defer voice.Close()
	// The first dictation loads the engine and the second finds it loaded.
	var loaded *worker
	for _, dictation := range []string{"first", "second"} {
		started := time.Now()
		text, err := voice.Recognize(context.Background(), sound)
		if err != nil {
			t.Fatal(err)
		}
		words := strings.Join(regexp.MustCompile(`[a-z]+`).FindAllString(strings.ToLower(text), -1), " ")
		if words != "open the pull request" {
			t.Fatalf("heard %q", text)
		}
		t.Logf("%s dictation heard %q in %s", dictation, text, time.Since(started).Round(time.Millisecond))
		if loaded == nil {
			loaded = current(t, voice)
		} else if current(t, voice) != loaded {
			t.Fatal("the second dictation loaded the engine again")
		}
	}
}
