package voice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Warming loads the engine as a dictation begins, so the words are ready
// soon after the keys are let go: the dictation finds it loaded and loads
// nothing more.
func TestWarmingLoadsTheEngineTheNextDictationFindsLoaded(t *testing.T) {
	// Arrange
	voice, _ := engine(t, "hears", 8<<30)

	// Act
	warmed := voice.Warm(context.Background())
	loaded := current(t, voice)
	again := voice.Warm(context.Background())
	_, recognized := voice.Recognize(context.Background(), []byte("RIFF-sound"))

	// Assert
	if warmed != nil || again != nil || recognized != nil {
		t.Fatalf("warm, warm again and dictate answered %v, %v, %v", warmed, again, recognized)
	}
	if loaded == nil {
		t.Fatal("warming started no engine")
	}
	if current(t, voice) != loaded || loads(t, voice) != 1 {
		t.Fatalf("the engine was loaded %d times, want once, by the warming", loads(t, voice))
	}
}

// Warming starts nothing where a dictation could not run: no model yet, or
// no room for the engine.
func TestWarmingStartsNoEngineWithoutTheModelOrRoom(t *testing.T) {
	// Arrange
	missing := &Voice{Settings: Settings{Engine: part("engine", "https://example.test/e.tar.bz2"), Model: part("model", "https://example.test/m.tar.bz2"), Program: "library.txt"}, Dir: t.TempDir(), Memory: func() (uint64, uint64, error) { return 8 << 30, 8 << 30, nil }}
	t.Cleanup(missing.Close)
	crowded, _ := engine(t, "hears", Room-1)

	// Act
	notFetched := missing.Warm(context.Background())
	noRoom := crowded.Warm(context.Background())

	// Assert
	var room NoRoom
	if notFetched == nil || !errors.As(noRoom, &room) {
		t.Fatalf("warming answered %v without the model and %v without room", notFetched, noRoom)
	}
	if current(t, missing) != nil || current(t, crowded) != nil || loads(t, crowded) != 0 {
		t.Fatal("warming started an engine that could not run")
	}
}

// A warmed engine nobody dictates to is ended once idle, as one a dictation
// loaded is.
func TestAWarmedEngineNobodyUsesIsEndedWhenIdle(t *testing.T) {
	// Arrange
	idle := workerIdle
	workerIdle = 200 * time.Millisecond
	t.Cleanup(func() { workerIdle = idle })
	voice, _ := engine(t, "hears", 8<<30)

	// Act
	if err := voice.Warm(context.Background()); err != nil {
		t.Fatal(err)
	}
	loaded := current(t, voice)

	// Assert
	select {
	case <-loaded.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("the warmed engine was never ended")
	}
}

// Replaces says the engine or model in place is one these settings no
// longer pin, as after an update that pins a newer one; a home that never
// set dictation up, or set up the one pinned, replaces nothing.
func TestReplacesSaysAnEarlierPinsEngineOrModelIsHere(t *testing.T) {
	// Arrange
	server, _ := served(t)
	dir := t.TempDir()
	voice := &Voice{Settings: Settings{Engine: part("engine", server.URL+"/engine.tar.bz2"), Model: part("model", server.URL+"/model.tar.bz2")}, Dir: dir, Client: server.Client()}
	if err := os.MkdirAll(filepath.Join(dir, "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	never := voice.Replaces()
	if err := voice.Fetch(context.Background(), quiet); err != nil {
		t.Fatal(err)
	}
	pinned := voice.Replaces()
	voice.Settings.Model.Version = "2.0"

	// Act
	updated := voice.Replaces()
	if err := voice.Fetch(context.Background(), quiet); err != nil {
		t.Fatal(err)
	}
	replaced := voice.Replaces()

	// Assert
	if never || pinned || !updated || replaced {
		t.Fatalf("replaces: never set up %v, pinned set up %v, after the pin changed %v, once replaced %v; want only the third", never, pinned, updated, replaced)
	}
}
