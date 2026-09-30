package supervisor

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTaskHandoffRefusesAParentJunction(t *testing.T) {
	store, home := testStore(t)
	outside := t.TempDir()
	if err := os.MkdirAll(home.Data, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "handoff.md"), []byte("outside the task"), 0600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(home.Data, "task-1")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Fatalf("create fixture junction: %v %s", err, out)
	}
	t.Cleanup(func() {
		if err := os.Remove(junction); err != nil {
			t.Error(err)
		}
	})
	handler := NewHTTP(&Service{Store: store}, "board.local", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/tasks/task-1/handoff", nil))

	if response.Code != 422 {
		t.Fatalf("junction handoff returned %d: %s", response.Code, response.Body.String())
	}
}
