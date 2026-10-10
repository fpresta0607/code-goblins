package home

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestSharedTempCanBeNoTasksScratchFolder(t *testing.T) {
	if err := state.ValidTaskID(SharedTempDir); err == nil {
		t.Fatalf("%q is a valid task id, so a task's scratch folder could be the shared temporary folder", SharedTempDir)
	}
	h := Home{Root: `C:\home`, DevDrive: `D:\CodeGoblins`}
	if got, want := h.SharedTemp(), `D:\CodeGoblins\scratch\.tmp`; got != want {
		t.Errorf("SharedTemp() = %q, want %q", got, want)
	}
	if got, want := SharedTempBeside(`C:\home\scratch\task-1\`), `C:\home\scratch\.tmp`; got != want {
		t.Errorf("SharedTempBeside() = %q, want %q", got, want)
	}
}

func TestLiveTempInNamesTheRuntimeWhoseTmpGoesWithTheFolder(t *testing.T) {
	root := t.TempDir()
	scratch := filepath.Join(root, "scratch", "task-1")
	for _, dir := range []string{filepath.Join(scratch, "inner"), filepath.Join(root, "scratch", "task-10"), filepath.Join(root, "scratch", ".tmp")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name   string
		tmp    string
		isHeld bool
	}{
		{name: "the folder itself", tmp: scratch, isHeld: true},
		{name: "the folder in another case", tmp: strings.ToUpper(scratch), isHeld: true},
		{name: "a folder inside it", tmp: filepath.Join(scratch, "inner"), isHeld: true},
		{name: "a folder inside it that is gone", tmp: filepath.Join(scratch, "gone", "deeper"), isHeld: true},
		{name: "a task whose id it begins", tmp: filepath.Join(root, "scratch", "task-10")},
		{name: "the shared folder beside it", tmp: filepath.Join(root, "scratch", ".tmp")},
		{name: "the folder holding it", tmp: filepath.Join(root, "scratch")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			temps := []LiveTemp{{Runtime: `C:\other\usr\bin`, Folder: `C:\Users\someone\AppData\Local\Temp`}, {Runtime: `C:\Git\usr\bin`, Folder: test.tmp}}

			held, isHeld := LiveTempIn(scratch, temps)

			if isHeld != test.isHeld {
				t.Fatalf("LiveTempIn(%s) with /tmp at %s = %v, want %v", scratch, test.tmp, isHeld, test.isHeld)
			}
			if isHeld && held.Runtime != `C:\Git\usr\bin` {
				t.Errorf("held by %q, want the runtime whose /tmp it is", held.Runtime)
			}
		})
	}
}

func TestRemoveScratchLeavesAFolderThatIsALiveTmp(t *testing.T) {
	tests := []struct {
		name       string
		temps      func(scratch string) ([]LiveTemp, error)
		wantHeldBy string
		wantErr    string
		isKept     bool
	}{
		{name: "no runtime runs", temps: func(string) ([]LiveTemp, error) { return nil, nil }},
		{name: "a runtime's /tmp is elsewhere", temps: func(string) ([]LiveTemp, error) {
			return []LiveTemp{{Runtime: `C:\Git\usr\bin`, Folder: `C:\Users\someone\AppData\Local\Temp`}}, nil
		}},
		{name: "a runtime's /tmp is the folder", temps: func(scratch string) ([]LiveTemp, error) {
			return []LiveTemp{{Runtime: `C:\Git\usr\bin`, Folder: scratch}}, nil
		}, wantHeldBy: `C:\Git\usr\bin`, isKept: true},
		{name: "a runtime could not be asked", temps: func(string) ([]LiveTemp, error) {
			return nil, errors.New("cygpath is missing")
		}, wantErr: "cygpath is missing", isKept: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			scratch := filepath.Join(t.TempDir(), "scratch", "task-1")
			if err := os.MkdirAll(filepath.Join(scratch, "go-build1"), 0o755); err != nil {
				t.Fatal(err)
			}
			askedFrom := ""

			// Act
			heldBy, err := RemoveScratch(context.Background(), scratch, func(_ context.Context, safeTemp string) ([]LiveTemp, error) {
				askedFrom = safeTemp
				return test.temps(scratch)
			})

			// Assert
			if heldBy != test.wantHeldBy {
				t.Errorf("held by %q, want %q", heldBy, test.wantHeldBy)
			}
			if test.wantErr == "" && err != nil || test.wantErr != "" && (!errors.Is(err, ErrLiveTempUnknown) || !strings.Contains(err.Error(), test.wantErr)) {
				t.Errorf("error = %v, want an ErrLiveTempUnknown naming %q", err, test.wantErr)
			}
			if _, statErr := os.Stat(scratch); (statErr == nil) != test.isKept {
				t.Errorf("the folder is there: %v, want %v", statErr == nil, test.isKept)
			}
			if want := SharedTempBeside(scratch); askedFrom != want {
				t.Errorf("the runtimes were asked from %q, want the shared folder %q, which is never removed", askedFrom, want)
			}
		})
	}
}

func TestRemoveScratchAsksNothingForAFolderAlreadyGone(t *testing.T) {
	heldBy, err := RemoveScratch(context.Background(), filepath.Join(t.TempDir(), "scratch", "gone"), func(context.Context, string) ([]LiveTemp, error) {
		t.Fatal("the running runtimes were asked about a folder that is not there")
		return nil, nil
	})
	if heldBy != "" || err != nil {
		t.Fatalf("RemoveScratch() = %q, %v", heldBy, err)
	}
}

func TestLiveTempsPassesOverProgramsThatAreNoMsysRuntimes(t *testing.T) {
	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	temps, err := liveTemps(context.Background(), []string{filepath.Join(plain, "notepad.exe"), filepath.Join(plain, "other.exe")}, filepath.Join(t.TempDir(), ".tmp"))

	if err != nil || len(temps) != 0 {
		t.Fatalf("liveTemps() = %v, %v, want no runtime and no error", temps, err)
	}
}

// A runtime that runs and cannot be asked may have any folder as its /tmp, so
// it is an error, never a runtime passed over.
func TestLiveTempsRefusesARunningRuntimeItCannotAsk(t *testing.T) {
	runtime := filepath.Join(t.TempDir(), "usr", "bin")
	if err := os.MkdirAll(runtime, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime, msysRuntime), []byte("no library"), 0o644); err != nil {
		t.Fatal(err)
	}

	temps, err := liveTemps(context.Background(), []string{filepath.Join(runtime, "bash.exe")}, filepath.Join(t.TempDir(), ".tmp"))

	if err == nil || !strings.Contains(err.Error(), runtime) {
		t.Fatalf("liveTemps() = %v, %v, want an error naming the runtime %s", temps, err, runtime)
	}
}

func TestProcessImagesListsThisProcess(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	images, err := processImages()

	if err != nil {
		t.Fatal(err)
	}
	for _, image := range images {
		if strings.EqualFold(filepath.Base(image), filepath.Base(self)) && fsx.SamePath(image, self) {
			return
		}
	}
	t.Fatalf("%d running programs listed, none of them this test's own %s", len(images), self)
}

// privateRuntime is a msys runtime of the test's own: the library, cygpath
// and sleep copied from the machine's Git, with the fstab line that makes
// /tmp the user's temporary folder. The runtime keys what its processes
// share by where its library is installed, so this copy has a mount table of
// its own and nothing the test does reaches a shell of the machine's Git.
func privateRuntime(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on PATH to copy a msys runtime from")
	}
	// git is <Git>\cmd\git.exe from a Windows shell and
	// <Git>\mingw64\bin\git.exe from Git Bash.
	source := ""
	for dir := filepath.Dir(git); source == "" && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "usr", "bin", msysRuntime)); err == nil {
			source = filepath.Join(dir, "usr", "bin")
		}
	}
	if source == "" {
		t.Skipf("the git on PATH, %s, has no msys runtime above it", git)
	}
	root := t.TempDir()
	runtime := filepath.Join(root, "usr", "bin")
	if err := os.MkdirAll(runtime, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{msysRuntime, "cygpath.exe", "sleep.exe"} {
		in, err := os.Open(filepath.Join(source, name))
		if err != nil {
			t.Skipf("the msys runtime in %s lacks %s: %v", source, name, err)
		}
		out, err := os.Create(filepath.Join(runtime, name))
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(out, in)
		in.Close()
		if err := errors.Join(copyErr, out.Close()); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "fstab"), []byte("none /tmp usertemp binary,posix=0,noacl 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return runtime
}

// The 2026-10-09 defect, in a runtime of the test's own: the first process
// starts with a task's scratch folder as its TMP, and while it runs that
// folder is /tmp for every later process, whatever TMP that one has.
func TestLiveTempsReadsWhatTheFirstProcessOfARuntimeMadeTmp(t *testing.T) {
	// Arrange
	runtime := privateRuntime(t)
	scratch := filepath.Join(t.TempDir(), "scratch", "task-1")
	safeTemp := SharedTempBeside(scratch)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	first := exec.Command(filepath.Join(runtime, "sleep.exe"), "120")
	first.Env = append(os.Environ(), "TMP="+scratch, "TEMP="+scratch)
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	isRunning := true
	stop := func() {
		if isRunning {
			_ = first.Process.Kill()
			_ = first.Wait()
			isRunning = false
		}
	}
	t.Cleanup(stop)
	images := []string{filepath.Join(runtime, "sleep.exe")}

	// Act
	temps, err := liveTemps(context.Background(), images, safeTemp)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 1 || !fsx.SamePath(temps[0].Folder, scratch) || temps[0].Runtime != runtime {
		t.Fatalf("liveTemps() = %+v, want the runtime %s with /tmp at the first process's TMP %s", temps, runtime, scratch)
	}
	if held, isHeld := LiveTempIn(scratch, temps); !isHeld || held.Runtime != runtime {
		t.Errorf("LiveTempIn() = %+v, %v, want the scratch folder held by the runtime", held, isHeld)
	}

	// Once its last process ends the mount is gone: the next process decides
	// again, and it is cygpath with the shared folder as its TMP.
	stop()
	temps, err = liveTemps(context.Background(), images, safeTemp)
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 1 || !fsx.SamePath(temps[0].Folder, safeTemp) {
		t.Fatalf("liveTemps() with no process left = %+v, want /tmp at the shared folder %s", temps, safeTemp)
	}
	if _, isHeld := LiveTempIn(scratch, temps); isHeld {
		t.Error("the scratch folder still reads as a live /tmp after the runtime's last process ended")
	}
}

// No test in any package may start a program of the machine's own Git Bash
// with a folder of the test's as its TMP, so the exported reader answers a
// test binary without looking.
func TestLiveTempsAsksNoRuntimeFromATestBinary(t *testing.T) {
	safeTemp := filepath.Join(t.TempDir(), "scratch", SharedTempDir)

	temps, err := LiveTemps(context.Background(), safeTemp)

	if err != nil || len(temps) != 0 {
		t.Fatalf("LiveTemps() in a test binary = %v, %v, want no runtime and no error", temps, err)
	}
	if _, err := os.Stat(safeTemp); !os.IsNotExist(err) {
		t.Errorf("LiveTemps() in a test binary made %s: %v, so it went on to ask", safeTemp, err)
	}
}
