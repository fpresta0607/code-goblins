package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// junction makes link a junction to target, as this workstation can, though
// it cannot make symbolic links.
func junction(t *testing.T, link, target string) {
	t.Helper()
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("cmd", "/d", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, output)
	}
	t.Cleanup(func() { _ = fsx.Remove(link) })
}

// An input that is a junction, or holds one, could change through what it
// points at with no change of its own, so no key is read.
func TestReadReuseKeyRefusesAnInputThatIsALink(t *testing.T) {
	for name, arrange := range map[string]func(t *testing.T, dir string){
		"an input that is a junction": func(t *testing.T, dir string) {
			if err := fsx.RemoveAll(filepath.Join(dir, "build")); err != nil {
				t.Fatal(err)
			}
			junction(t, filepath.Join(dir, "build"), filepath.Join(t.TempDir(), "elsewhere"))
		},
		"a junction inside an input": func(t *testing.T, dir string) {
			junction(t, filepath.Join(dir, "build", "linked"), filepath.Join(t.TempDir(), "elsewhere"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := reuseWorktree(t)
			arrange(t, dir)

			// Act
			key, err := ReadReuseKey(context.Background(), execx.OSRunner{}, dir, namedKey(), reuseDeclared())

			// Assert
			if err == nil || !strings.Contains(err.Error(), "is a link") {
				t.Errorf("ReadReuseKey = %+v, %v; want it refused for the link", key, err)
			}
		})
	}
}
