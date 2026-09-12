package supersede

import (
	"os"
	"strings"
	"testing"
)

func TestRecord(t *testing.T) {
	p, i, e := Record(t.TempDir(), "x", "wrong architecture")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(p); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(i, "delete artifacts") || !strings.Contains(i, "Do not carry the rejected approach") {
		t.Fatal(i)
	}
}
