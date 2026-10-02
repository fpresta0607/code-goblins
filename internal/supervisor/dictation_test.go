package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/voice"
)

// fakeSpeech is a dictation engine that records what it was asked. While
// missing is set it is not ready, and a fetch that returns nil makes it so.
type fakeSpeech struct {
	mu      sync.Mutex
	missing error
	fetches int
	// started is closed once a fetch has told its progress, and the fetch
	// then waits for fetching to close.
	started  chan struct{}
	fetching chan struct{}
	fetchErr error
	sounds   [][]byte
	text     string
	err      error
}

func (f *fakeSpeech) Name() string { return "test-model" }

func (f *fakeSpeech) Ready() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.missing
}

func (f *fakeSpeech) Fetch(ctx context.Context, progress func(part string, done, total int64)) error {
	f.mu.Lock()
	f.fetches++
	wait, err := f.fetching, f.fetchErr
	f.mu.Unlock()
	progress("model", 45<<20, 103<<20)
	if wait != nil {
		close(f.started)
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.missing = nil
	f.mu.Unlock()
	return nil
}

func (f *fakeSpeech) Recognize(_ context.Context, sound []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sounds = append(f.sounds, sound)
	return f.text, f.err
}

func (f *fakeSpeech) heard() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sounds)
}

const dictationHost = "127.0.0.1:4310"

func dictationBoard(t *testing.T, speech Dictation) *HTTP {
	t.Helper()
	store, _ := testStore(t)
	handler := NewHTTP(&Service{Store: store, Instance: "test-instance", Options: Options{Dictation: speech}}, dictationHost, nil)
	t.Cleanup(handler.dictationWork.Wait)
	return handler
}

// dictate posts sound the way the board's own page on this machine does,
// after change has had its say over the request.
func dictate(handler *HTTP, sound []byte, change func(*http.Request)) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "http://"+handler.Host+"/api/dictation", bytes.NewReader(sound))
	request.Host = handler.Host
	request.RemoteAddr = "127.0.0.1:50000"
	request.Header.Set("Origin", "http://"+handler.Host)
	request.Header.Set("X-CFO-Token", "test-instance")
	request.Header.Set("Content-Type", "audio/wav")
	if change != nil {
		change(request)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func dictationStatus(t *testing.T, handler *HTTP) (engine, state, note string) {
	t.Helper()
	request := httptest.NewRequest("GET", "http://"+handler.Host+"/api/dictation", nil)
	request.Host = handler.Host
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var status struct{ Engine, State, Note string }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &status) != nil {
		t.Fatalf("the status answered %d %s", response.Code, response.Body)
	}
	return status.Engine, status.State, status.Note
}

func answer(t *testing.T, response *httptest.ResponseRecorder) (text, engine, problem string) {
	t.Helper()
	var body struct{ Text, Engine, Error string }
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("the answer %q is not JSON: %v", response.Body, err)
	}
	return body.Text, body.Engine, body.Error
}

func TestDictationReturnsTheWordsOfTheSoundThePagePosted(t *testing.T) {
	speech := &fakeSpeech{text: "open the pull request"}
	handler := dictationBoard(t, speech)
	response := dictate(handler, []byte("RIFF-sound"), nil)
	text, engine, _ := answer(t, response)
	if response.Code != 200 || text != "open the pull request" || engine != "test-model" {
		t.Fatalf("answered %d %s", response.Code, response.Body)
	}
	if speech.heard() != 1 || string(speech.sounds[0]) != "RIFF-sound" {
		t.Fatalf("the engine heard %q", speech.sounds)
	}
	if speech.fetches != 0 {
		t.Fatalf("a ready engine was fetched %d times: dictating must ask the network for nothing", speech.fetches)
	}
	if engine, state, _ := dictationStatus(t, handler); engine != "test-model" || state != "ready" {
		t.Fatalf("the status reads %s %s", engine, state)
	}
}

func TestDictationTakesSoundOnlyFromTheBoardsOwnPageOnThisMachine(t *testing.T) {
	header := func(name, value string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set(name, value) }
	}
	for name, test := range map[string]struct {
		change func(*http.Request)
		code   int
	}{
		"a peer off this machine":    {func(r *http.Request) { r.RemoteAddr = "100.101.102.103:51234" }, http.StatusForbidden},
		"a request through a proxy":  {header("X-Forwarded-For", "100.101.102.103"), http.StatusForbidden},
		"no board token":             {func(r *http.Request) { r.Header.Del("X-CFO-Token") }, http.StatusForbidden},
		"another site's page":        {header("Origin", "https://example.test"), http.StatusForbidden},
		"a body that is not a sound": {header("Content-Type", "application/json"), http.StatusBadRequest},
	} {
		speech := &fakeSpeech{text: "never typed"}
		response := dictate(dictationBoard(t, speech), []byte("RIFF-sound"), test.change)
		if response.Code != test.code {
			t.Errorf("%s: answered %d %s, want %d", name, response.Code, response.Body, test.code)
		}
		if speech.heard() != 0 || strings.Contains(response.Body.String(), "never typed") {
			t.Errorf("%s: the engine was handed the sound", name)
		}
	}
	// A board reached by another name, as one shared over a tailnet is,
	// dictates nothing: the sound would have left the machine it was said on.
	speech := &fakeSpeech{text: "never typed"}
	store, _ := testStore(t)
	shared := NewHTTP(&Service{Store: store, Instance: "test-instance", Options: Options{Dictation: speech}}, "goblins.tailnet.ts.net:443", nil)
	response := dictate(shared, []byte("RIFF-sound"), nil)
	if _, _, problem := answer(t, response); response.Code != http.StatusForbidden || !strings.Contains(problem, "never leaves") || speech.heard() != 0 {
		t.Fatalf("a shared board answered %d %s", response.Code, response.Body)
	}
}

func TestDictationRefusesASoundLongerThanItsLimit(t *testing.T) {
	speech := &fakeSpeech{text: "never typed"}
	response := dictate(dictationBoard(t, speech), make([]byte, dictationLimit+1), nil)
	if response.Code != http.StatusRequestEntityTooLarge || speech.heard() != 0 {
		t.Fatalf("a sound over the limit answered %d and the engine heard %d", response.Code, speech.heard())
	}
}

func TestTheFirstDictationFetchesTheModelOnceAndSaysSo(t *testing.T) {
	speech := &fakeSpeech{missing: errors.New("model 1 is not fetched"), started: make(chan struct{}), fetching: make(chan struct{}), text: "now it types"}
	handler := dictationBoard(t, speech)
	response := dictate(handler, []byte("RIFF-sound"), nil)
	_, _, problem := answer(t, response)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(problem, "being set up") {
		t.Fatalf("the first dictation answered %d %s", response.Code, response.Body)
	}
	// While it downloads, another dictation starts no second download and
	// the status says how far it is.
	<-speech.started
	response = dictate(handler, []byte("RIFF-sound"), nil)
	if _, _, problem = answer(t, response); !strings.Contains(problem, "downloading model, 45 of 103 MB") {
		t.Fatalf("a dictation during the download answered %s", response.Body)
	}
	if _, state, note := dictationStatus(t, handler); state != "fetching" || !strings.Contains(note, "downloading model, 45 of 103 MB") {
		t.Fatalf("the status reads %s %q", state, note)
	}
	close(speech.fetching)
	handler.dictationWork.Wait()
	if speech.fetches != 1 || speech.heard() != 0 {
		t.Fatalf("%d downloads were started and the engine heard %d sounds", speech.fetches, speech.heard())
	}
	response = dictate(handler, []byte("RIFF-sound"), nil)
	if text, _, _ := answer(t, response); response.Code != 200 || text != "now it types" {
		t.Fatalf("after the download a dictation answered %d %s", response.Code, response.Body)
	}
}

func TestAFetchThatFailsIsExplainedAndTriedAgain(t *testing.T) {
	speech := &fakeSpeech{missing: errors.New("model 1 is not fetched"), fetchErr: errors.New("model 1 could not be downloaded: no such host. Connect to the internet and dictate again")}
	handler := dictationBoard(t, speech)
	dictate(handler, []byte("RIFF-sound"), nil)
	handler.dictationWork.Wait()
	if _, state, note := dictationStatus(t, handler); state != "missing" || !strings.Contains(note, "no such host") {
		t.Fatalf("after a failed download the status reads %s %q", state, note)
	}
	response := dictate(handler, []byte("RIFF-sound"), nil)
	handler.dictationWork.Wait()
	if _, _, problem := answer(t, response); response.Code != http.StatusServiceUnavailable || !strings.Contains(problem, "Connect to the internet") {
		t.Fatalf("the dictation after a failed download answered %d %s", response.Code, response.Body)
	}
	if speech.fetches != 2 {
		t.Fatalf("%d downloads were started, want the first and one more try", speech.fetches)
	}
}

func TestADownloadThatStallsIsGivenUpSoTheNextDictationCanTryAgain(t *testing.T) {
	speech := &fakeSpeech{missing: errors.New("model 1 is not fetched"), started: make(chan struct{}), fetching: make(chan struct{})}
	handler := dictationBoard(t, speech)
	handler.dictationPatience = 20 * time.Millisecond
	dictate(handler, []byte("RIFF-sound"), nil)
	handler.dictationWork.Wait()
	if _, state, note := dictationStatus(t, handler); state != "missing" || !strings.Contains(note, "was given up") {
		t.Fatalf("after a stalled download the status reads %s %q", state, note)
	}
}

func TestANetworkTimeoutInsideTheLimitKeepsTheFetchsOwnWords(t *testing.T) {
	_, timeout := net.DialTimeout("tcp", "127.0.0.1:1", time.Nanosecond)
	if !errors.Is(timeout, context.DeadlineExceeded) {
		t.Fatalf("the premise does not hold: the dial answered %v, which is not read as a deadline", timeout)
	}
	speech := &fakeSpeech{missing: errors.New("model 1 is not fetched"), fetchErr: fmt.Errorf("model 1 could not be downloaded: %w. Download https://example.test/m.tar.bz2 yourself and save it as m.tar.bz2", timeout)}
	handler := dictationBoard(t, speech)
	dictate(handler, []byte("RIFF-sound"), nil)
	handler.dictationWork.Wait()
	_, state, note := dictationStatus(t, handler)
	if state != "missing" || !strings.Contains(note, "save it as m.tar.bz2") || strings.Contains(note, "was given up") {
		t.Fatalf("after a network timeout inside the limit the status reads %s %q", state, note)
	}
}

func TestDictationSaysSoWhenTheMachineHasNoRoom(t *testing.T) {
	speech := &fakeSpeech{err: voice.NoRoom{Available: 700 << 20, Commit: 3 << 30}}
	response := dictate(dictationBoard(t, speech), []byte("RIFF-sound"), nil)
	_, _, problem := answer(t, response)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(problem, "1 GB of free memory") || !strings.Contains(problem, "0.7 GB") {
		t.Fatalf("with no room it answered %d %s", response.Code, response.Body)
	}
}

func TestASupervisorWithoutAnEngineSaysDictationIsNotSetUp(t *testing.T) {
	handler := dictationBoard(t, nil)
	response := dictate(handler, []byte("RIFF-sound"), nil)
	if _, _, problem := answer(t, response); response.Code != http.StatusServiceUnavailable || !strings.Contains(problem, "cfo doctor") {
		t.Fatalf("without an engine it answered %d %s", response.Code, response.Body)
	}
	if _, state, _ := dictationStatus(t, handler); state != "none" {
		t.Fatalf("the status reads %s", state)
	}
}
