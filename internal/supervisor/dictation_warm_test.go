package supervisor

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// warm posts the warming the board's page sends as a dictation begins, after
// change has had its say over the request.
func warm(handler *HTTP, change func(*http.Request)) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "http://"+handler.Host+"/api/dictation/warm", nil)
	request.Host = handler.Host
	request.RemoteAddr = "127.0.0.1:50000"
	request.Header.Set("Origin", "http://"+handler.Host)
	request.Header.Set("X-CFO-Token", "test-instance")
	if change != nil {
		change(request)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// A dictation beginning on the board's own page loads the engine, so its
// words come soon after the keys are let go.
func TestADictationBeginningWarmsTheEngine(t *testing.T) {
	// Arrange
	speech := &fakeSpeech{}
	handler := dictationBoard(t, speech)

	// Act
	response := warm(handler, nil)
	handler.dictationWork.Wait()

	// Assert
	if response.Code != http.StatusAccepted || speech.warmed() != 1 {
		t.Fatalf("warming answered %d and warmed %d times, want 202 and once", response.Code, speech.warmed())
	}
}

// Warming is the board's own page's on this machine alone, and an engine
// that is not there yet is not warmed: the dictation that follows says why.
func TestWarmingIsRefusedOffTheMachineAndSkippedWithoutTheModel(t *testing.T) {
	for name, test := range map[string]struct {
		speech *fakeSpeech
		change func(*http.Request)
		code   int
	}{
		"a peer off this machine": {&fakeSpeech{}, func(r *http.Request) { r.RemoteAddr = "100.101.102.103:51234" }, http.StatusForbidden},
		"no board token":          {&fakeSpeech{}, func(r *http.Request) { r.Header.Del("X-CFO-Token") }, http.StatusForbidden},
		"no model yet":            {&fakeSpeech{missing: errors.New("model 1.0 is not fetched")}, nil, http.StatusAccepted},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			handler := dictationBoard(t, test.speech)

			// Act
			response := warm(handler, test.change)
			handler.dictationWork.Wait()

			// Assert
			if response.Code != test.code || test.speech.warmed() != 0 {
				t.Fatalf("answered %d and warmed %d times, want %d and none", response.Code, test.speech.warmed(), test.code)
			}
		})
	}
}

// After an update to a build that pins a newer model, the board fetches it
// as it starts, so the first dictation finds it ready; a home that never set
// dictation up, or holds the model pinned, downloads nothing.
func TestTheBoardFetchesAReplacementModelAsItStartsAndNothingElse(t *testing.T) {
	for name, test := range map[string]struct {
		speech  *fakeSpeech
		fetches int
	}{
		"an earlier pin's model here":  {&fakeSpeech{missing: errors.New("model 2.0 is not fetched"), replaces: true}, 1},
		"never set up":                 {&fakeSpeech{missing: errors.New("model 2.0 is not fetched")}, 0},
		"the pinned model here":        {&fakeSpeech{}, 0},
		"the pinned model and another": {&fakeSpeech{replaces: true}, 0},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			handler := dictationBoard(t, test.speech)

			// Act
			handler.ReplaceDictation()
			handler.dictationWork.Wait()

			// Assert
			if test.speech.fetches != test.fetches {
				t.Fatalf("the board fetched %d times as it started, want %d", test.speech.fetches, test.fetches)
			}
		})
	}
}
