package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetupFolderPickerUsesTypedOrDefaultFolderAndReturnsSelection(t *testing.T) {
	for _, name := range []string{"typed", "recorded", "home", "cancelled", "failed"} {
		t.Run(name, func(t *testing.T) {
			store, _ := testStore(t)
			machine := newFirstRunMachine(t)
			typed, chosen := t.TempDir(), t.TempDir()
			root, initial := typed, typed
			machine.run.ProjectsRoot = func() (string, error) { return machine.root, nil }
			if name == "recorded" {
				root, initial = "", machine.root
			}
			if name == "home" {
				root, initial = "", machine.home
				machine.run.ProjectsRoot = func() (string, error) { return "", nil }
			}
			if name == "cancelled" {
				chosen = ""
			}
			handler := NewHTTP(&Service{Store: store, Instance: "instance", Options: Options{FirstRun: machine.run}}, "board.local", nil)
			calls := 0
			handler.pickFolder = func(_ context.Context, folder string) (string, error) {
				calls++
				if folder != initial {
					t.Errorf("initial folder = %q, want %q", folder, initial)
				}
				if name == "failed" {
					return "", errors.New("picker unavailable")
				}
				return chosen, nil
			}
			body, err := json.Marshal(map[string]string{"root": root})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "http://board.local/api/setup/folder", strings.NewReader(string(body)))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "http://board.local")
			request.Header.Set("X-CFO-Token", "instance")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			status := http.StatusOK
			if name == "failed" {
				status = http.StatusServiceUnavailable
			}
			if response.Code != status || calls != 1 {
				t.Fatalf("status %d, calls %d: %s", response.Code, calls, response.Body.String())
			}
			if name != "failed" {
				var result struct {
					Root string `json:"root"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Root != chosen {
					t.Errorf("selected %q, want %q", result.Root, chosen)
				}
			}
			if len(machine.recorded) != 0 || len(machine.started) != 0 {
				t.Fatal("choosing a folder changed setup or started the CFO")
			}
		})
	}
}

func TestSetupFolderPickerKeepsOnlyOneDialogOpen(t *testing.T) {
	store, _ := testStore(t)
	machine := newFirstRunMachine(t)
	handler := NewHTTP(&Service{Store: store, Instance: "instance", Options: Options{FirstRun: machine.run}}, "board.local", nil)
	opened, finish := make(chan struct{}), make(chan struct{})
	handler.pickFolder = func(context.Context, string) (string, error) {
		close(opened)
		<-finish
		return "", nil
	}
	post := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"root": machine.root})
		request := httptest.NewRequest(http.MethodPost, "http://board.local/api/setup/folder", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://board.local")
		request.Header.Set("X-CFO-Token", "instance")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	completed := make(chan *httptest.ResponseRecorder, 1)
	go func() { completed <- post() }()
	defer func() {
		close(finish)
		select {
		case response := <-completed:
			if response.Code != http.StatusOK {
				t.Errorf("first picker = %d: %s", response.Code, response.Body.String())
			}
		case <-time.After(time.Minute):
			t.Error("first picker did not finish")
		}
	}()
	select {
	case <-opened:
	case <-time.After(time.Minute):
		t.Fatal("picker did not start")
	}
	if response := post(); response.Code != http.StatusConflict {
		t.Fatalf("second picker = %d: %s", response.Code, response.Body.String())
	}
}

func TestSetupFolderPickerRefusesUnsafeRequestsWithoutOpeningAWindow(t *testing.T) {
	store, _ := testStore(t)
	machine := newFirstRunMachine(t)
	handler := NewHTTP(&Service{Store: store, Instance: "instance", Options: Options{FirstRun: machine.run}}, "board.local", nil)
	handler.pickFolder = func(context.Context, string) (string, error) {
		t.Fatal("refused request opened the picker")
		return "", nil
	}
	for _, test := range []struct {
		name, root, origin, token string
		status                    int
	}{
		{"foreign origin", machine.root, "https://other.invalid", "instance", 403},
		{"wrong token", machine.root, "http://board.local", "wrong", 403},
		{"relative path", "relative", "http://board.local", "instance", 400},
		{"missing directory", filepath.Join(machine.root, "missing"), "http://board.local", "instance", 400},
		{"file", filepath.Join(machine.root, "readme.txt"), "http://board.local", "instance", 400},
		{"control character", machine.root + "\x00", "http://board.local", "instance", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"root": test.root})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "http://board.local/api/setup/folder", strings.NewReader(string(body)))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", test.origin)
			request.Header.Set("X-CFO-Token", test.token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
		})
	}
}
