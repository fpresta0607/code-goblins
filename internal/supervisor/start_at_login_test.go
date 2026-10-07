package supervisor

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postStartAtLogin(handler *HTTP, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "http://board.local/api/start-at-login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://board.local")
	request.Header.Set("X-CFO-Token", orderToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// The board's switch turns Start at login on or off, and the snapshot shows
// it as the home holds it.
func TestTheBoardsSwitchTurnsStartAtLoginOff(t *testing.T) {
	// Arrange
	handler, _ := orderBoard(t)
	isOn := true
	handler.Service.Options.StartAtLogin = &StartAtLogin{
		Read: func() (StartAtLoginView, error) { return StartAtLoginView{On: isOn}, nil },
		Set:  func(on bool) error { isOn = on; return nil },
	}

	// Act
	response := postStartAtLogin(handler, `{"on":false}`)

	// Assert
	if response.Code != http.StatusOK || isOn {
		t.Fatalf("POST = %d %s, Start at login on %v; want 200 and off", response.Code, response.Body, isOn)
	}
	snapshot, err := handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.StartAtLogin == nil || snapshot.StartAtLogin.On {
		t.Errorf("the snapshot shows Start at login %+v, want off", snapshot.StartAtLogin)
	}
}

// A switch that says nothing, a board that cannot change Start at login and a
// change Windows refuses are each refused with the reason, and change nothing.
func TestTheBoardsSwitchRefusesWhatItCannotDo(t *testing.T) {
	for _, test := range []struct {
		name    string
		body    string
		setting *StartAtLogin
		code    int
		says    string
	}{
		{"no choice", `{}`, &StartAtLogin{Set: func(bool) error { t.Error("set without a choice"); return nil }}, http.StatusBadRequest, "Say whether Start at login is on"},
		{"no setting on this board", `{"on":true}`, nil, http.StatusConflict, "This board cannot change Start at login"},
		{"a change Windows refuses", `{"on":true}`, &StartAtLogin{Set: func(bool) error { return errors.New("access is denied") }}, http.StatusInternalServerError, "Start at login was not changed: access is denied"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			handler, _ := orderBoard(t)
			handler.Service.Options.StartAtLogin = test.setting

			// Act
			response := postStartAtLogin(handler, test.body)

			// Assert
			if response.Code != test.code || !strings.Contains(response.Body.String(), test.says) {
				t.Errorf("POST = %d %s, want %d saying %q", response.Code, response.Body, test.code, test.says)
			}
		})
	}
}
