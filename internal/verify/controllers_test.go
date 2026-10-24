package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestControllersReadQueueSlotsAndWorkerCustody(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	controller, err := currentController()
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{"line", "custody"} {
		if err := os.MkdirAll(filepath.Join(dir, folder), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"slot-1", "line/waiter", "custody/execution.json", "line/foreign"} {
		record := controller
		record.Session = name
		if name == "line/foreign" {
			record.Hostname += "-another-machine"
		}
		data, err := json.Marshal(record)
		if name == "custody/execution.json" {
			data, err = json.Marshal(Custody{RunID: "execution", Token: "slot-1:fixture", Controller: record})
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), data, 0600); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	controllers, err := Controllers(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var sessions []string
	for _, read := range controllers {
		if read.PID != controller.PID || !read.Start.Equal(controller.Start) {
			t.Errorf("controller identity changed: %+v", read)
		}
		sessions = append(sessions, read.Session)
	}
	slices.Sort(sessions)

	// Assert
	if !slices.Equal(sessions, []string{"custody/execution.json", "line/waiter", "slot-1"}) {
		t.Fatalf("controller sources = %v", sessions)
	}
}

func TestControllersRefuseUnreadableCustody(t *testing.T) {
	for _, test := range []struct{ name, body string }{
		{"slot-1", "unreadable"},
		{"line/waiter", "unreadable"},
		{"custody/execution.json", "unreadable"},
		{"slot-2", "{}"},
		{"custody/incomplete.json", `{"run_id":"incomplete","token":"slot-1:fixture"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			path := filepath.Join(dir, filepath.FromSlash(test.name))
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(test.body), 0600); err != nil {
				t.Fatal(err)
			}

			// Act
			_, err := Controllers(t.Context(), dir)

			// Assert
			if err == nil {
				t.Fatal("unreadable custody treated as absent")
			}
		})
	}
}
