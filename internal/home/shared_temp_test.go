package home

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestSharedTempCanBeNoTasksScratchFolder(t *testing.T) {
	if err := state.ValidTaskID(SharedTempDir); err == nil {
		t.Fatalf("%q is a valid task id, so a task's scratch folder could be the shared temporary folder", SharedTempDir)
	}
	h := Home{Root: systemPath(`C:\home`), DevDrive: systemPath(`D:\CodeGoblins`)}
	if got, want := SharedTempBeside(filepath.Join(h.Scratch(), "task-1")), systemPath(`D:\CodeGoblins\scratch\.tmp`); got != want {
		t.Errorf("SharedTempBeside() on the Dev Drive = %q, want %q", got, want)
	}
	if got, want := SharedTempBeside(systemPath(`C:\home\scratch\task-1\`)), systemPath(`C:\home\scratch\.tmp`); got != want {
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
	// A folder in another case is the folder where the file system opens it
	// by that spelling, as Windows does, and another folder where it does not.
	_, upperErr := os.Stat(strings.ToUpper(scratch))
	tests := []struct {
		name   string
		tmp    string
		isHeld bool
	}{
		{name: "the folder itself", tmp: scratch, isHeld: true},
		{name: "the folder in another case", tmp: strings.ToUpper(scratch), isHeld: upperErr == nil},
		{name: "a folder inside it", tmp: filepath.Join(scratch, "inner"), isHeld: true},
		{name: "a folder inside it that is gone", tmp: filepath.Join(scratch, "gone", "deeper"), isHeld: true},
		{name: "a task whose id it begins", tmp: filepath.Join(root, "scratch", "task-10")},
		{name: "the shared folder beside it", tmp: filepath.Join(root, "scratch", ".tmp")},
		{name: "the folder holding it", tmp: filepath.Join(root, "scratch")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			temps := []LiveTemp{{Runtime: systemPath(`C:\other\usr\bin`), Folder: systemPath(`C:\Users\someone\AppData\Local\Temp`)}, {Runtime: systemPath(`C:\Git\usr\bin`), Folder: test.tmp}}

			held, isHeld := LiveTempIn(scratch, temps)

			if isHeld != test.isHeld {
				t.Fatalf("LiveTempIn(%s) with /tmp at %s = %v, want %v", scratch, test.tmp, isHeld, test.isHeld)
			}
			if isHeld && held.Runtime != systemPath(`C:\Git\usr\bin`) {
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
			return []LiveTemp{{Runtime: systemPath(`C:\Git\usr\bin`), Folder: systemPath(`C:\Users\someone\AppData\Local\Temp`)}}, nil
		}},
		{name: "a runtime's /tmp is the folder", temps: func(scratch string) ([]LiveTemp, error) {
			return []LiveTemp{{Runtime: systemPath(`C:\Git\usr\bin`), Folder: scratch}}, nil
		}, wantHeldBy: systemPath(`C:\Git\usr\bin`), isKept: true},
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

			// Act
			heldBy, err := RemoveScratch(context.Background(), scratch, func(context.Context) ([]LiveTemp, error) {
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
		})
	}
}

func TestRemoveScratchAsksNothingForAFolderAlreadyGone(t *testing.T) {
	heldBy, err := RemoveScratch(context.Background(), filepath.Join(t.TempDir(), "scratch", "gone"), func(context.Context) ([]LiveTemp, error) {
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

// Two programs in folders whose names differ only in case run from one
// runtime exactly where the file system reads the names as one folder, so it
// is asked once there, and each is a runtime of its own elsewhere.
func TestLiveTempsAsksARuntimeInAFolderSpelledInAnotherCaseByTheFileSystemsCase(t *testing.T) {
	// Arrange
	lower, upper, isOnePath := caseSpellings(t, t.TempDir(), "bin")
	if !isOnePath {
		if err := os.Mkdir(upper, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(upper, msysRuntime), []byte("no library"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	images := []string{filepath.Join(lower, "notepad.exe"), filepath.Join(upper, "bash.exe")}

	// Act
	temps, err := liveTemps(context.Background(), images, filepath.Join(t.TempDir(), ".tmp"))

	// Assert
	if isOnePath && (err != nil || len(temps) != 0) {
		t.Fatalf("liveTemps() = %v, %v, want %s asked once, as no runtime, where it is %s", temps, err, upper, lower)
	}
	if !isOnePath && (err == nil || !strings.Contains(err.Error(), upper)) {
		t.Fatalf("liveTemps() = %v, %v, want the runtime in %s asked apart from %s", temps, err, upper, lower)
	}
}

// The names below the deepest folder that exists are compared by the file
// system's case: a /tmp in a folder that is gone, named in another case, is
// in the folder about to be removed exactly where the two names are one.
func TestLiveTempInComparesTheNamesOfFoldersThatAreGoneByTheFileSystemsCase(t *testing.T) {
	// Arrange
	root := t.TempDir()
	_, _, isOnePath := caseSpellings(t, root, "probe")
	folder := filepath.Join(root, "gone")
	temps := []LiveTemp{{Runtime: systemPath(`C:\Git\usr\bin`), Folder: filepath.Join(root, "GONE", "deeper")}}

	// Act
	_, isHeld := LiveTempIn(folder, temps)

	// Assert
	if isHeld != isOnePath {
		t.Fatalf("LiveTempIn(%s) with /tmp at %s = %t, want %t", folder, temps[0].Folder, isHeld, isOnePath)
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

// privateRuntime is a msys runtime of the test's own: the library and
// cygpath copied from the machine's Git, with the fstab line that makes /tmp
// the user's temporary folder. The runtime keys what its processes share by
// where its library is installed, so this copy has a mount table of its own
// and nothing the test does reaches a shell of the machine's Git.
//
// cygpath is the one program copied because it needs nothing but the
// library. This fixture once copied sleep too, which also needs
// msys-intl-8.dll, and found it only where Git's usr\bin was on the PATH:
// on a runner, where it is not, sleep ended at once, the runtime had no
// first process, and the test read the fixture's failure as the product's.
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
	for _, name := range []string{msysRuntime, "cygpath.exe"} {
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
	for _, folder := range []string{scratch, safeTemp} {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The first process is a cygpath that answers the paths it reads and
	// runs until its input closes. Its first answer proves the premise the
	// rest stands on: the runtime is up, and this process decided its /tmp.
	first := exec.Command(filepath.Join(runtime, "cygpath.exe"), "-w", "-f", "-")
	first.Env = append(os.Environ(), "TMP="+scratch, "TEMP="+scratch)
	asks, err := first.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	answers, err := first.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	isRunning := true
	stop := func() {
		if isRunning {
			_ = asks.Close()
			_ = first.Wait()
			isRunning = false
		}
	}
	t.Cleanup(func() {
		_ = first.Process.Kill()
		stop()
	})
	answered := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(answers).ReadString('\n')
		answered <- strings.TrimSpace(line)
	}()
	if _, err := io.WriteString(asks, "/tmp\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case own := <-answered:
		if !fsx.SamePath(own, scratch) {
			t.Fatalf("the runtime's first process has /tmp at %q, want its own TMP %s", own, scratch)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the runtime's first process never answered, so no runtime is up to ask")
	}
	images := []string{filepath.Join(runtime, "cygpath.exe")}

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

// No test's result may rest on what happens to run on the machine, so the
// exported reader answers a test binary without looking.
func TestLiveTempsAsksNoRuntimeFromATestBinary(t *testing.T) {
	temps, err := LiveTemps(context.Background())

	if err != nil || len(temps) != 0 {
		t.Fatalf("LiveTemps() in a test binary = %v, %v, want no runtime and no error", temps, err)
	}
}
