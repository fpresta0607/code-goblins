package supervisor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
// the two the root itself is recorded under, a file that is not there is
// reported missing under either, and a folder beside the root matches under
// neither.
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
			f, err = openInside([]string{root}, filepath.Join(spelling, "shots", "missing.png"))
			if !errors.Is(err, fs.ErrNotExist) {
				if f != nil {
					f.Close()
				}
				t.Fatalf("root %s: a missing file under %s: err = %v, want the file reported missing", root, spelling, err)
			}
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
// of the two, as GitHub runners spell their temp directory, and reports an
// image that is not there as missing under each of them.
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
	for _, spelling := range []string{short, long, mixed} {
		if _, err := ReviewImages(h, meta.ID, []string{filepath.Join(spelling, "missing.png")}); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("a missing image under %s: err = %v, want the image reported missing", spelling, err)
		}
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

// The board serves a question's images by position only, never a path, and
// only while the goblin that asked is the same live task.
func TestQuestionImagesServedOnlyWhileTheAskerLives(t *testing.T) {
	store, h := testStore(t)
	meta, record, _, _ := goblinFixture(t, store)
	paths := []string{filepath.Join(meta.Worktree, "postgres.png"), filepath.Join(h.Data, meta.ID, "sqlite.png"), filepath.Join(meta.Worktree, "mysql.png")}
	var data [][]byte
	for _, path := range paths {
		data = append(data, writePNG(t, path))
	}
	images, err := ReviewImages(h, meta.ID, paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := SurfaceNotify(h.State, meta.ID, record, record.Detail, "", "", images, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	q := store.Snapshot().Questions[0]
	if len(q.Images) != 3 {
		t.Fatalf("question = %+v, want its three images", q)
	}
	s := &Service{Store: store}
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	published, _ := json.Marshal(snapshot)
	if snapshot.Questions[0].ImageCount != 3 || snapshot.Questions[0].Images != nil || bytes.Contains(published, []byte("postgres.png")) {
		t.Fatalf("the board saw %+v, want the image count without paths", snapshot.Questions[0])
	}
	handler := NewHTTP(s, "board.local", nil)
	get := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local"+path, nil))
		return response
	}
	for i := range paths {
		response := get(fmt.Sprintf("/api/questions/%s/images/%d", q.ID, i))
		if response.Code != 200 || response.Header().Get("Content-Type") != "image/png" || !bytes.Equal(response.Body.Bytes(), data[i]) {
			t.Fatalf("image %d = %d %q", i, response.Code, response.Header().Get("Content-Type"))
		}
	}
	for _, path := range []string{"/api/questions/" + q.ID + "/images/3", "/api/questions/" + q.ID + "/images/-1", "/api/questions/" + q.ID + "/images/x", "/api/questions/" + q.ID + "/images/0/more", "/api/questions/unknown/images/0"} {
		if response := get(path); response.Code != 404 {
			t.Fatalf("%s = %d, want 404", path, response.Code)
		}
	}
	if err := os.WriteFile(paths[2], []byte("swapped for text"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths[1]); err != nil {
		t.Fatal(err)
	}
	for i, name := range map[int]string{1: "sqlite.png", 2: "mysql.png"} {
		if response := get(fmt.Sprintf("/api/questions/%s/images/%d", q.ID, i)); response.Code != 403 || strings.Contains(response.Body.String(), name) {
			t.Fatalf("changed image %d = %d %s, want a refusal that names no path", i, response.Code, response.Body.String())
		}
	}
	meta.SpawnGen = "g2"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if response := get("/api/questions/" + q.ID + "/images/0"); response.Code != 410 {
		t.Fatalf("image after respawn = %d, want 410", response.Code)
	}
}
