package voice

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Two fetches of one part at once, as the install's and the board's are
// when someone dictates while the install sets dictation up, each download
// on their own and both end with the part in place and no download left.
func TestTwoFetchesOfOnePartAtOnceBothLeaveItReady(t *testing.T) {
	// Arrange
	archive, err := os.ReadFile(filepath.Join("testdata", "part.tar.bz2"))
	if err != nil {
		t.Fatal(err)
	}
	var arrived atomic.Int32
	both := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if arrived.Add(1) == 2 {
			close(both)
		}
		select {
		case <-both:
		case <-time.After(10 * time.Second):
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(archive)))
		_, _ = w.Write(archive[:len(archive)/2])
		w.(http.Flusher).Flush()
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(archive[len(archive)/2:])
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	wanted := part("model", server.URL+"/part.tar.bz2")
	results := make(chan error, 2)

	// Act
	for range 2 {
		go func() {
			results <- (&Voice{Dir: dir, Client: server.Client()}).fetch(context.Background(), wanted, quietPart)
		}()
	}

	// Assert
	for range 2 {
		if err := <-results; err != nil {
			t.Errorf("a fetch beside another failed: %v", err)
		}
	}
	if arrived.Load() != 2 {
		t.Fatalf("%d downloads were asked for, want both fetches downloading at once", arrived.Load())
	}
	if err := (&Voice{Dir: dir}).ready(wanted); err != nil {
		t.Fatalf("after both fetches the part is not ready: %v", err)
	}
	want := "model-1.0/library.txt model-1.0/program.txt model-1.0/tokens.txt model-1.0/verified.json"
	if got := strings.Join(entries(t, dir), " "); got != want {
		t.Fatalf("the folder holds %s, want %s", got, want)
	}
}

// When the settings pin another version of a part, as a newer build does, a
// fetch puts that version in place and removes the folder of the one it
// replaces. A part whose pin did not change is kept and not downloaded, and
// a folder the fetches did not make is left alone.
func TestAFetchReplacesAVersionTheSettingsNoLongerPin(t *testing.T) {
	// Arrange
	server, asked := served(t)
	dir := t.TempDir()
	voice := &Voice{Settings: Settings{Engine: part("engine", server.URL+"/engine.tar.bz2"), Model: part("model", server.URL+"/model.tar.bz2")}, Dir: dir, Client: server.Client()}
	if err := voice.Fetch(context.Background(), quiet); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(dir, "notes")
	if err := os.MkdirAll(own, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "mine.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	voice.Settings.Model.Version = "2.0"

	// Act
	err := voice.Fetch(context.Background(), quiet)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if asked.Load() != 3 {
		t.Fatalf("%d downloads in all, want the engine and the model once and the newer model once", asked.Load())
	}
	want := "engine-1.0/library.txt engine-1.0/program.txt engine-1.0/tokens.txt engine-1.0/verified.json " +
		"model-2.0/library.txt model-2.0/program.txt model-2.0/tokens.txt model-2.0/verified.json notes/mine.txt"
	if got := strings.Join(entries(t, dir), " "); got != want {
		t.Fatalf("the folder holds %s, want %s", got, want)
	}
}

// A folder of a version no longer pinned that is still in use, as an engine
// an earlier build's worker has loaded, cannot be removed now, so it is left
// whole rather than half removed, and goes at the next fetch.
func TestAFetchLeavesWholeAReplacedFolderStillInUseAndRemovesItNextTime(t *testing.T) {
	// Arrange
	server, asked := served(t)
	dir := t.TempDir()
	voice := &Voice{Settings: Settings{Engine: part("engine", server.URL+"/engine.tar.bz2"), Model: part("model", server.URL+"/model.tar.bz2")}, Dir: dir, Client: server.Client()}
	if err := voice.Fetch(context.Background(), quiet); err != nil {
		t.Fatal(err)
	}
	earlier := voice.Settings.Model
	held, err := os.Open(filepath.Join(dir, "model-1.0", "program.txt"))
	if err != nil {
		t.Fatal(err)
	}
	voice.Settings.Model.Version = "2.0"

	// Act
	fetched := voice.Fetch(context.Background(), quiet)
	inUse := voice.ready(earlier)
	_ = held.Close()
	again := voice.Fetch(context.Background(), quiet)

	// Assert
	if fetched != nil || again != nil {
		t.Fatalf("the fetches answered %v and %v, want both to succeed", fetched, again)
	}
	if inUse != nil {
		t.Fatalf("the replaced folder in use was not left whole: %v", inUse)
	}
	if asked.Load() != 3 {
		t.Fatalf("%d downloads in all, want the second fetch to download nothing", asked.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, "model-1.0")); !os.IsNotExist(err) {
		t.Fatalf("the replaced folder outlived the next fetch: %v", err)
	}
	if got := entries(t, dir); len(got) != 8 {
		t.Fatalf("the folder holds %v, want the engine and the newer model alone", got)
	}
}
