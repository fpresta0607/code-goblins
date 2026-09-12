package hygiene

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTestsFindsSkippedCandidate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x_test.go")
	os.WriteFile(p, []byte("func TestX(t *testing.T){ t.Skip(\"old\") }"), 0600)
	x, e := Tests(filepath.Dir(p))
	if e != nil || len(x) != 1 || x[0].Kind != "skipped-test" {
		t.Fatal(x, e)
	}
}
