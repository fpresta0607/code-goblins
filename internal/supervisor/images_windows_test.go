package supervisor

import (
	"bytes"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A path on a network share is refused without a lookup on disk, which would
// reach the host it names and could offer it the user's credentials: only the
// roots are looked up, however the share is spelled (issue 1931).
func TestOpenInsideLooksUpOnlyItsRootsOnDisk(t *testing.T) {
	root := t.TempDir()
	image := filepath.Join(root, "shots", "a.png")
	writePNG(t, image)
	canonical, err := fsx.Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	spellings := folderSpellings
	t.Cleanup(func() { folderSpellings = spellings })
	lookedUp := []string{}
	folderSpellings = func(dir string) (string, string) {
		lookedUp = append(lookedUp, dir)
		if dir != filepath.Clean(root) && dir != canonical {
			// A stray path is recorded and never looked up for real.
			return dir, dir
		}
		return spellings(dir)
	}
	for _, path := range []string{
		`\\host.invalid\share\a.png`,
		`/\host.invalid\share\a.png`,
		`\/host.invalid\share\a.png`,
		`//host.invalid/share/a.png`,
		`\\?\UNC\host.invalid\share\a.png`,
		filepath.Join(t.TempDir(), "a.png"),
	} {
		f, err := openInside([]string{root}, path)
		if !errors.Is(err, errOutsideRoots) {
			if f != nil {
				f.Close()
			}
			t.Fatalf("%s: err = %v, want it outside every allowed folder", path, err)
		}
	}
	f, err := openInside([]string{root}, image)
	if err != nil {
		t.Fatalf("premise: an image inside the root refused: %v", err)
	}
	f.Close()
	if len(lookedUp) == 0 {
		t.Fatal("premise: nothing was looked up on disk, so the roots were never spelled")
	}
	for _, dir := range lookedUp {
		if dir != filepath.Clean(root) && dir != canonical {
			t.Fatalf("%s was looked up on disk, and it is no root", dir)
		}
	}
}

// A file opens under a root folder's long or its 8.3 short name, whichever of
// the two the root itself is recorded under, and a folder beside the root
// matches under neither.
func TestOpenInsideMatchesEitherSpellingOfARootFolder(t *testing.T) {
	long := filepath.Join(fsx.LongPath(t.TempDir()), "a root with a long name")
	writePNG(t, filepath.Join(long, "shots", "a.png"))
	writePNG(t, filepath.Join(long+" beside", "shots", "a.png"))
	short := fsx.ShortPath(long)
	if short == long {
		t.Skipf("the volume keeps no 8.3 short name for %s", long)
	}
	for _, root := range []string{long, short} {
		for _, spelling := range []string{long, short} {
			f, err := openInside([]string{root}, filepath.Join(spelling, "shots", "a.png"))
			if err != nil {
				t.Fatalf("root %s: %s refused: %v", root, spelling, err)
			}
			f.Close()
		}
		f, err := openInside([]string{root}, filepath.Join(fsx.ShortPath(long+" beside"), "shots", "a.png"))
		if !errors.Is(err, errOutsideRoots) {
			if f != nil {
				f.Close()
			}
			t.Fatalf("root %s: a folder beside it: err = %v, want it outside every allowed folder", root, err)
		}
	}
}

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
	meta, record, _, _ := goblinFixture(t, store)
	data := writePNG(t, filepath.Join(long, "a.png"))
	paths := []string{filepath.Join(short, "a.png"), filepath.Join(long, "a.png"), filepath.Join(mixed, "a.png")}
	images, err := ReviewImages(h, meta.ID, paths)
	if err != nil {
		t.Fatalf("an image under the task's own worktree refused: %v", err)
	}
	if err := SurfaceNotify(h.State, meta.ID, record, record.Detail, "", "", images, nil); err != nil {
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
