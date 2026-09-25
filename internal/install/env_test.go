package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// With CFO_USER_ENV_FILE set, the user environment is that file: what is set
// lands in it, names compare without case as Windows compares them, and a
// file not yet written reads as an empty scope.
func TestTheUserEnvironmentFileStandsInForTheMachines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user-env.json")
	t.Setenv(UserEnvFileVariable, path)
	store := NewEnvStore(nil)

	if value, set, err := store.Get("Path"); err != nil || set {
		t.Fatalf("Get(Path) before any write = %q, %v, %v; want unset", value, set, err)
	}
	for _, write := range []struct{ name, value string }{{"Path", `C:\one;C:\two`}, {"PATH", `C:\three`}, {"CFO_HOME", `C:\home`}} {
		if err := store.Set(write.name, write.value); err != nil {
			t.Fatalf("Set(%s): %v", write.name, err)
		}
	}
	if value, set, err := store.Get("path"); err != nil || !set || value != `C:\three` {
		t.Errorf("Get(path) = %q, %v, %v; want the last value set under any case", value, set, err)
	}
	if err := store.Unset("pAtH"); err != nil {
		t.Fatalf("Unset: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil || len(values) != 1 || values["CFO_HOME"] != `C:\home` {
		t.Errorf("the file holds %s (%v), want only CFO_HOME", data, err)
	}
}
