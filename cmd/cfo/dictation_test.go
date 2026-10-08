package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/voice"
)

// partArchiveSHA256 is the SHA-256 of internal/voice/testdata/part.tar.bz2,
// which holds part-1.0/bin/program.txt, part-1.0/bin/library.txt and
// part-1.0/tokens.txt, and partArchiveSize its size in bytes.
const (
	partArchiveSHA256 = "261765dae87693ca85c35908f16e592be0df4fda0001fbd1dedcede88f0e33e1"
	partArchiveSize   = 288
)

// servedParts serves the fixture archive for every request and counts them.
func servedParts(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	archive, err := os.ReadFile(filepath.Join("..", "..", "internal", "voice", "testdata", "part.tar.bz2"))
	if err != nil {
		t.Fatal(err)
	}
	var asked atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)
	return server, &asked
}

// dictationHome is a home whose config/voice.json pins an engine and a model
// downloaded from base, each checked against sum.
func dictationHome(t *testing.T, base, sum string) home.Home {
	t.Helper()
	root := t.TempDir()
	settings := fmt.Sprintf(`{
  "engine": {"name": "engine", "version": "1.0", "url": %q, "sha256": %q, "size": %d, "files": ["bin/program.txt", "bin/library.txt"]},
  "model": {"name": "model", "version": "1.0", "url": %q, "sha256": %q, "size": %d, "files": ["tokens.txt"]},
  "program": "library.txt",
  "args": ["--num-threads=2", "--encoder={model}/tokens.txt", "--decoder={model}/tokens.txt", "--joiner={model}/tokens.txt", "--tokens={model}/tokens.txt", "--model-type=nemo_transducer"]
}`, base+"/engine.tar.bz2", sum, partArchiveSize, base+"/model.tar.bz2", sum, partArchiveSize)
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "voice.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	return home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
}

func dictationRuntime(h home.Home, client *http.Client) commandRuntime {
	return commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }, dictationClient: client}
}

// noNetwork fails the test that sends a request through it.
type noNetwork struct{ t *testing.T }

func (n noNetwork) RoundTrip(request *http.Request) (*http.Response, error) {
	n.t.Errorf("the network was asked for %s", request.URL)
	return nil, errors.New("no network in this test")
}

func dictationReady(t *testing.T, h home.Home) error {
	t.Helper()
	speech, err := voice.For(h.Root, codegoblins.Voice)
	if err != nil {
		t.Fatal(err)
	}
	return speech.Ready()
}

func TestDictationSetupPutsTheEngineAndTheModelWhereDictationLooks(t *testing.T) {
	// Arrange
	server, asked := servedParts(t)
	h := dictationHome(t, server.URL, partArchiveSHA256)
	var stdout, stderr bytes.Buffer

	// Act
	code := runWithRuntime([]string{"dictation", "setup"}, &stdout, &stderr, dictationRuntime(h, server.Client()))

	// Assert
	if code != 0 {
		t.Fatalf("cfo dictation setup = %d, want 0\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	if err := dictationReady(t, h); err != nil {
		t.Fatalf("after the setup dictation is not ready: %v", err)
	}
	if asked.Load() != 2 {
		t.Errorf("%d downloads, want the engine's and the model's", asked.Load())
	}
	want := "dictation: ready: model 1.0 on engine 1.0, in " + voice.Folder(h.Root)
	if !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
	}
}

// A machine that already has the engine and the model, as the first
// dictation or an earlier install left them, downloads nothing again.
func TestDictationSetupDownloadsNothingWhenTheEngineAndTheModelAreThere(t *testing.T) {
	// Arrange
	server, _ := servedParts(t)
	h := dictationHome(t, server.URL, partArchiveSHA256)
	if code := runWithRuntime([]string{"dictation", "setup"}, &bytes.Buffer{}, &bytes.Buffer{}, dictationRuntime(h, server.Client())); code != 0 {
		t.Fatalf("the first setup = %d", code)
	}
	var stdout, stderr bytes.Buffer

	// Act
	code := runWithRuntime([]string{"dictation", "setup"}, &stdout, &stderr, dictationRuntime(h, &http.Client{Transport: noNetwork{t}}))

	// Assert
	if code != 0 {
		t.Fatalf("cfo dictation setup = %d, want 0\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	want := "dictation: ready: model 1.0 on engine 1.0, already in " + voice.Folder(h.Root) + ", so nothing was downloaded"
	if !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
	}
}

// What is downloaded is kept only when it matches the SHA-256 the settings
// pin, as the install keeps cfo.exe only when it matches the release's.
func TestDictationSetupRefusesADownloadThatDoesNotMatchItsPinnedSHA256(t *testing.T) {
	// Arrange
	server, _ := servedParts(t)
	h := dictationHome(t, server.URL, strings.Repeat("0", 64))
	var stdout, stderr bytes.Buffer

	// Act
	code := runWithRuntime([]string{"dictation", "setup"}, &stdout, &stderr, dictationRuntime(h, server.Client()))

	// Assert
	if code != 1 {
		t.Fatalf("cfo dictation setup = %d, want 1\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"does not match its checksum", partArchiveSHA256, "nothing was kept"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}
	kept, _ := os.ReadDir(voice.Folder(h.Root))
	if len(kept) != 0 {
		t.Errorf("the refused download left %d entries in %s", len(kept), voice.Folder(h.Root))
	}
	if dictationReady(t, h) == nil {
		t.Error("a refused download reads ready")
	}
}

func TestDictationSetupSaysWhyWhenItCannotDownload(t *testing.T) {
	// Arrange
	server, _ := servedParts(t)
	client := server.Client()
	h := dictationHome(t, server.URL, partArchiveSHA256)
	server.Close()
	var stdout, stderr bytes.Buffer

	// Act
	code := runWithRuntime([]string{"dictation", "setup"}, &stdout, &stderr, dictationRuntime(h, client))

	// Assert
	if code != 1 || !strings.Contains(stderr.String(), "could not be downloaded") {
		t.Fatalf("cfo dictation setup offline = %d, want 1 saying it could not be downloaded\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
}

func TestDictationTakesOnlySetup(t *testing.T) {
	for _, args := range [][]string{{"dictation"}, {"dictation", "fetch"}, {"dictation", "setup", "now"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			// Arrange
			var stdout, stderr bytes.Buffer
			runtime := commandRuntime{resolveHome: func() (home.Home, error) {
				t.Error("a refused command resolved the home")
				return home.Home{}, errors.New("no home")
			}}

			// Act
			code := runWithRuntime(args, &stdout, &stderr, runtime)

			// Assert
			if code != 2 || !strings.Contains(stderr.String(), "usage: cfo dictation setup") {
				t.Errorf("cfo %s = %d, want 2 with the usage\nstderr: %s", strings.Join(args, " "), code, stderr.String())
			}
		})
	}
}
