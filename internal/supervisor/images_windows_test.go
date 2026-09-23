package supervisor

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A task whose worktree is recorded under its 8.3 short name still reviews
// and serves an image spelled under the short name, the long name, or a mix
// of the two, as GitHub runners spell their temp directory.
func TestReviewImagesAcceptEitherSpellingOfTheRoot(t *testing.T) {
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	shortName := func(long string) string {
		path, err := syscall.UTF16PtrFromString(long)
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]uint16, syscall.MAX_LONG_PATH)
		n, err := syscall.GetShortPathName(path, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 || int(n) > len(buf) {
			t.Fatalf("short name of %s: %v", long, err)
		}
		return syscall.UTF16ToString(buf[:n])
	}
	long := meta.Worktree
	short := shortName(long)
	if short == long {
		t.Skipf("the volume has no 8.3 short name for %s", long)
	}
	// Only the test's own directory, two levels up, is spelled short.
	testDir := filepath.Dir(filepath.Dir(long))
	mixed := filepath.Join(filepath.Dir(testDir), filepath.Base(shortName(testDir)), filepath.Base(filepath.Dir(long)), filepath.Base(long))
	if mixed == long || mixed == short {
		t.Skipf("the test directory %s has no 8.3 short name to mix in", testDir)
	}
	meta.Worktree = short
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	meta, record, _, connection := goblinFixture(t, store)
	data := writePNG(t, filepath.Join(long, "a.png"))
	paths := []string{filepath.Join(short, "a.png"), filepath.Join(long, "a.png"), filepath.Join(mixed, "a.png")}
	images, err := ReviewImages(h, meta.ID, paths)
	if err != nil {
		t.Fatalf("an image under the task's own worktree refused: %v", err)
	}
	if err := SurfaceNotify(context.Background(), h.State, connection.Herdr, meta.ID, record, images); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	q := store.Snapshot().Questions[0]
	handler := NewHTTP(&Service{Store: store}, "board.local", nil)
	for i := range paths {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", fmt.Sprintf("http://board.local/api/questions/%s/images/%d", q.ID, i), nil))
		if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), data) {
			t.Fatalf("image %d (%s) = %d %s", i, paths[i], response.Code, response.Body.String())
		}
	}
}
