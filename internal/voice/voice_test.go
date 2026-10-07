package voice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fixtureSHA256 is the SHA-256 of testdata/part.tar.bz2, which holds
// part-1.0/bin/program.txt, part-1.0/bin/library.txt, part-1.0/docs/readme.txt
// and part-1.0/tokens.txt.
const fixtureSHA256 = "261765dae87693ca85c35908f16e592be0df4fda0001fbd1dedcede88f0e33e1"

// programSHA256 is the SHA-256 of testdata/program.tar.bz2.
const programSHA256 = "c6b75f138a27c197666042c48bc996a94ed72c11870014b33bb19b36a9e5120b"

// TestMain lets the test binary stand in for cfo as the engine's worker:
// started as `voice-worker` it answers as standInWorker does and exits.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "voice-worker" {
		os.Exit(standInWorker(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// quiet is a progress nobody listens to, and quietPart one of a part's.
func quiet(int64, int64) {}

func quietPart(int64) {}

// refusesEverything fails the test that sends a request through it.
type refusesEverything struct{ t *testing.T }

func (r refusesEverything) RoundTrip(request *http.Request) (*http.Response, error) {
	r.t.Errorf("the network was asked for %s", request.URL)
	return nil, errors.New("no network in this test")
}

// fixtureSize is the size in bytes of testdata/part.tar.bz2.
const fixtureSize = 288

func part(name, url string) Part {
	return Part{Name: name, Version: "1.0", URL: url, SHA256: fixtureSHA256, Size: fixtureSize, Files: []string{"bin/program.txt", "bin/library.txt", "tokens.txt"}}
}

// served answers every request with the fixture archive and counts them.
func served(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	archive, err := os.ReadFile(filepath.Join("testdata", "part.tar.bz2"))
	if err != nil {
		t.Fatal(err)
	}
	var asked atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		w.Header().Set("Content-Length", fmt.Sprint(len(archive)))
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)
	return server, &asked
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			relative, _ := filepath.Rel(dir, path)
			names = append(names, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return names
}

func TestSettingsAreReadWholeAndRefusedWhenTheyDoNotPinADownload(t *testing.T) {
	valid := `{"engine":{"name":"engine","version":"1","url":"https://example.test/e.tar.bz2","sha256":"` + fixtureSHA256 + `","size":1,"files":["bin/e.exe"]},
"model":{"name":"model","version":"2","url":"https://example.test/m.tar.bz2","sha256":"` + fixtureSHA256 + `","size":1,"files":["tokens.txt"]},
"program":"e.exe","args":["--num-threads=2","--encoder={model}/e.onnx","--decoder={model}/d.onnx","--joiner={model}/j.onnx","--tokens={model}/tokens.txt","--model-type=nemo_transducer"]}`
	path := filepath.Join(t.TempDir(), "voice.json")
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Model.Name != "model" || settings.Engine.Files[0] != "bin/e.exe" || settings.Args[0] != "--num-threads=2" || settings.Program != "e.exe" {
		t.Fatalf("settings read as %+v", settings)
	}
	for name, change := range map[string][2]string{
		"a download that is not https":   {"https://example.test/m.tar.bz2", "http://example.test/m.tar.bz2"},
		"a checksum that is not SHA-256": {`"sha256":"` + fixtureSHA256 + `","size":1,"files":["tokens.txt"]`, `"sha256":"abc","size":1,"files":["tokens.txt"]`},
		"a part with no size":            {`"size":1,"files":["tokens.txt"]`, `"files":["tokens.txt"]`},
		"a part with no files":           {`"files":["tokens.txt"]`, `"files":[]`},
		"a file that leaves its folder":  {`"files":["tokens.txt"]`, `"files":["../tokens.txt"]`},
		"an archive that is not tar.bz2": {"https://example.test/m.tar.bz2", "https://example.test/m.zip"},
		"a field it does not know":       {`"program":"e.exe"`, `"program":"e.exe","cloud":true`},
		"a program that is not a file":   {`"program":"e.exe"`, `"program":"bin/e.exe"`},
		// Settings the engine's worker would refuse, such as those of the
		// program each dictation once started, fail here rather than at
		// every dictation.
		"args the worker does not take": {`"--model-type=nemo_transducer"`, `"--model-type=nemo_transducer","--debug=1"`},
		"args missing one it needs":     {`"--joiner={model}/j.onnx",`, ``},
	} {
		if !strings.Contains(valid, change[0]) {
			t.Fatalf("%s: the valid settings do not hold %q", name, change[0])
		}
		if err := os.WriteFile(path, []byte(strings.Replace(valid, change[0], change[1], 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestAFetchTellsHowMuchOfTheWholeDownloadHasArrived(t *testing.T) {
	archive, err := os.ReadFile(filepath.Join("testdata", "part.tar.bz2"))
	if err != nil {
		t.Fatal(err)
	}
	// Each archive arrives in two halves, so progress is told inside each part.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(archive)))
		_, _ = w.Write(archive[:len(archive)/2])
		w.(http.Flusher).Flush()
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(archive[len(archive)/2:])
	}))
	t.Cleanup(server.Close)
	voice := &Voice{Settings: Settings{Engine: part("engine", server.URL+"/engine.tar.bz2"), Model: part("model", server.URL+"/model.tar.bz2"), Program: "library.txt"}, Dir: t.TempDir(), Client: server.Client()}
	if missing := voice.Missing(); missing != 2*fixtureSize {
		t.Fatalf("with nothing fetched %d bytes are missing, want both parts' %d", missing, 2*fixtureSize)
	}
	var told [][2]int64
	if err := voice.Fetch(context.Background(), func(done, total int64) { told = append(told, [2]int64{done, total}) }); err != nil {
		t.Fatal(err)
	}
	var engineDone, insideModel bool
	for index, progress := range told {
		if progress[1] != 2*fixtureSize || progress[0] < 0 || progress[0] > progress[1] || index > 0 && progress[0] < told[index-1][0] {
			t.Fatalf("progress %v is not a count of the whole download that only grows: %v", progress, told)
		}
		engineDone = engineDone || progress[0] == fixtureSize
		insideModel = insideModel || progress[0] > fixtureSize && progress[0] < 2*fixtureSize
	}
	if !engineDone || !insideModel || told[len(told)-1] != [2]int64{2 * fixtureSize, 2 * fixtureSize} {
		t.Fatalf("progress did not pass the engine's end, count on through the model and finish whole: %v", told)
	}
	if missing := voice.Missing(); missing != 0 {
		t.Fatalf("after the fetch %d bytes are missing", missing)
	}
}

func TestAFetchKeepsOnlyTheNamedFilesOfADownloadThatMatchesItsChecksum(t *testing.T) {
	server, asked := served(t)
	dir := t.TempDir()
	var progress []int64
	voice := &Voice{Dir: dir, Client: server.Client()}
	wanted := part("engine", server.URL+"/part.tar.bz2")
	if err := voice.ready(wanted); err == nil {
		t.Fatal("a part that was never fetched reads ready")
	}
	if err := voice.fetch(context.Background(), wanted, func(done int64) { progress = append(progress, done) }); err != nil {
		t.Fatal(err)
	}
	got := entries(t, dir)
	want := []string{"engine-1.0/library.txt", "engine-1.0/program.txt", "engine-1.0/tokens.txt", "engine-1.0/verified.json"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("the folder holds %v, want %v: the archive and the unwanted file are not kept", got, want)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "engine-1.0", "program.txt")); string(data) != "the program\n" {
		t.Fatalf("program.txt holds %q", data)
	}
	if err := voice.ready(wanted); err != nil {
		t.Fatalf("a fetched part is not ready: %v", err)
	}
	if asked.Load() != 1 {
		t.Fatalf("the download was asked for %d times", asked.Load())
	}
	if len(progress) == 0 || progress[len(progress)-1] != fixtureSize {
		t.Fatalf("progress ended at %v, want all %d bytes", progress, fixtureSize)
	}
	// A part whose file was changed or removed is not ready.
	if err := os.WriteFile(filepath.Join(dir, "engine-1.0", "library.txt"), []byte("longer than before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := voice.ready(wanted); err == nil {
		t.Fatal("a part with a changed file reads ready")
	}
	// Another version of the same part is another folder, not ready.
	newer := wanted
	newer.Version = "2.0"
	if err := voice.ready(newer); err == nil {
		t.Fatal("a version that was never fetched reads ready")
	}
}

// The model's real archive names its entries ./folder/file, the engine's
// folder/file; testdata/dotted.tar.bz2 is the fixture in the first shape.
func TestAnArchiveWhoseEntriesStartWithADotFolderIsUnpackedTheSame(t *testing.T) {
	archive, err := os.ReadFile(filepath.Join("testdata", "dotted.tar.bz2"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) }))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	voice := &Voice{Dir: dir, Client: server.Client()}
	wanted := part("model", server.URL+"/dotted.tar.bz2")
	wanted.SHA256 = "61a0b071b5a67cfaa02ea47e90c7648a106a8afc6172aff97b431c428270f80b"
	if err := voice.fetch(context.Background(), wanted, quietPart); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(entries(t, dir), " "), "model-1.0/library.txt model-1.0/program.txt model-1.0/tokens.txt model-1.0/verified.json"; got != want {
		t.Fatalf("the folder holds %s, want %s", got, want)
	}
}

func TestADownloadIsSentOnOnlyToHTTPSAndNoMoreThanTenTimes(t *testing.T) {
	for name, test := range map[string]struct {
		next    string
		hops    int
		refused string
	}{
		"a tenth hop to https":    {"https://example.test/part.tar.bz2", 9, ""},
		"a hop that is not https": {"http://example.test/part.tar.bz2", 1, "not https"},
		"an eleventh hop":         {"https://example.test/part.tar.bz2", 10, "more than 10 times"},
	} {
		next, err := http.NewRequest(http.MethodGet, test.next, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = redirect(next, make([]*http.Request, test.hops))
		if test.refused == "" && err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
		if test.refused != "" && (err == nil || !strings.Contains(err.Error(), test.refused)) {
			t.Errorf("%s answered %v, want a refusal saying %q", name, err, test.refused)
		}
	}
}

func TestADownloadThatDoesNotMatchItsChecksumIsRefusedAndNothingIsKept(t *testing.T) {
	server, _ := served(t)
	dir := t.TempDir()
	voice := &Voice{Dir: dir, Client: server.Client()}
	wrong := part("engine", server.URL+"/part.tar.bz2")
	wrong.SHA256 = strings.Repeat("0", 64)
	err := voice.fetch(context.Background(), wrong, quietPart)
	if err == nil {
		t.Fatal("a download with the wrong checksum was accepted")
	}
	for _, word := range []string{"checksum", fixtureSHA256[:12], "nothing was kept"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the refusal %q does not say %q", err, word)
		}
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Fatalf("the refused download left %v", got)
	}
	if err := voice.ready(wrong); err == nil {
		t.Fatal("a refused part reads ready")
	}
}

func TestADownloadWithoutAWantedFileIsRefusedAndNothingIsKept(t *testing.T) {
	server, _ := served(t)
	dir := t.TempDir()
	voice := &Voice{Dir: dir, Client: server.Client()}
	missing := part("engine", server.URL+"/part.tar.bz2")
	missing.Files = append(missing.Files, "bin/absent.txt")
	err := voice.fetch(context.Background(), missing, quietPart)
	if err == nil || !strings.Contains(err.Error(), "bin/absent.txt") {
		t.Fatalf("a download without bin/absent.txt answered %v", err)
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Fatalf("the refused download left %v", got)
	}
}

func TestOfflineTheFetchSaysWhatToDoAndAFilePlacedByHandIsUsedWithoutTheNetwork(t *testing.T) {
	server, asked := served(t)
	client := server.Client()
	url := server.URL + "/part.tar.bz2"
	server.Close()
	dir := t.TempDir()
	voice := &Voice{Dir: dir, Client: client}
	wanted := part("model", url)
	err := voice.fetch(context.Background(), wanted, quietPart)
	if err == nil {
		t.Fatal("a fetch with no network succeeded")
	}
	place := filepath.Join(dir, "part.tar.bz2")
	for _, word := range []string{"could not be downloaded", url, place} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the offline answer %q does not say %q", err, word)
		}
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Fatalf("the failed download left %v", got)
	}
	archive, err := os.ReadFile(filepath.Join("testdata", "part.tar.bz2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(place, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := voice.fetch(context.Background(), wanted, quietPart); err != nil {
		t.Fatalf("the file placed by hand was not used: %v", err)
	}
	if err := voice.ready(wanted); err != nil {
		t.Fatal(err)
	}
	if asked.Load() != 0 {
		t.Fatalf("the network was asked %d times", asked.Load())
	}
	// A file placed by hand is held to the same checksum.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "part.tar.bz2"), append(archive, 0), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (&Voice{Dir: other, Client: client}).fetch(context.Background(), wanted, quietPart); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a changed file placed by hand answered %v", err)
	}
}

// engine is a Voice whose engine and model are fetched from the fixture and
// whose worker is this test binary, answering as role says. It returns the
// file the worker records each sound it is handed in.
func engine(t *testing.T, role string, free uint64) (*Voice, string) {
	t.Helper()
	server, _ := served(t)
	dir := t.TempDir()
	settings := Settings{Engine: part("engine", server.URL+"/engine.tar.bz2"), Model: part("model", server.URL+"/model.tar.bz2"), Program: "library.txt", Args: []string{"--num-threads=2", "--encoder={model}/program.txt", "--decoder={model}/library.txt", "--joiner={model}/program.txt", "--tokens={model}/tokens.txt", "--model-type=nemo_transducer"}}
	voice := &Voice{Settings: settings, Dir: dir, Client: server.Client(), Memory: func() (uint64, uint64, error) { return free, free, nil }}
	if err := voice.Fetch(context.Background(), quiet); err != nil {
		t.Fatal(err)
	}
	// From here on the network answers nothing: recognising must not ask it.
	voice.Client = &http.Client{Transport: refusesEverything{t}}
	temporary := t.TempDir()
	t.Setenv("TMP", temporary)
	t.Setenv("TEMP", temporary)
	record := filepath.Join(t.TempDir(), "engine-run.json")
	writeWorkerFixture(t, voice, role, record)
	t.Cleanup(voice.Close)
	return voice, record
}

func TestRecognizeHandsTheSoundToTheEngineAndReturnsItsWords(t *testing.T) {
	voice, record := engine(t, "hears", 8<<30)
	text, err := voice.Recognize(context.Background(), []byte("RIFF-sound"))
	if err != nil {
		t.Fatal(err)
	}
	if text != `Check the "Vercel" deployment.` {
		t.Fatalf("heard %q", text)
	}
	run := ran(t, record)
	model := filepath.Join(voice.Dir, "model-1.0")
	want := []string{"voice-worker", filepath.Join(voice.folder(voice.Settings.Engine), "library.txt"), "--num-threads=2", "--encoder=" + model + "/program.txt", "--decoder=" + model + "/library.txt", "--joiner=" + model + "/program.txt", "--tokens=" + model + "/tokens.txt", "--model-type=nemo_transducer"}
	if strings.Join(run.Args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the engine was started with %q, want %q", run.Args, want)
	}
	if run.Payload != "RIFF-sound" {
		t.Fatalf("the engine was handed %q", run.Payload)
	}
	// The sound went through the pipe: it was never written to a file.
	if got := entries(t, os.TempDir()); len(got) != 0 {
		t.Fatalf("dictation left %v in the temporary folder", got)
	}
}

func TestASoundTheEngineCannotRecogniseIsReportedAndTheEngineIsKept(t *testing.T) {
	voice, record := engine(t, "fails", 8<<30)
	for range 2 {
		_, err := voice.Recognize(context.Background(), []byte("RIFF-sound"))
		if err == nil || !strings.Contains(err.Error(), "Failed to read sound") {
			t.Fatalf("an engine that could not recognise the sound answered %v", err)
		}
	}
	if ran(t, record).Payload != "RIFF-sound" || loads(t, voice) != 1 {
		t.Fatalf("the engine was loaded %d times, want once", loads(t, voice))
	}
}

func TestAFetchRefusesAProgramItCannotCheckForNetworking(t *testing.T) {
	archive, err := os.ReadFile(filepath.Join("testdata", "program.tar.bz2"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) }))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	voice := &Voice{Dir: dir, Client: server.Client()}
	// testdata/program.tar.bz2 holds part-1.0/bin/engine.exe, which is text.
	wanted := Part{Name: "engine", Version: "1.0", URL: server.URL + "/program.tar.bz2", SHA256: programSHA256, Files: []string{"bin/engine.exe"}}
	err = voice.fetch(context.Background(), wanted, quietPart)
	if err == nil || !strings.Contains(err.Error(), "engine.exe") {
		t.Fatalf("a program that cannot be read answered %v", err)
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Fatalf("the refused download left %v", got)
	}
}

func TestRecognizeRefusesBeforeStartingTheEngineWhenTheMachineHasNoRoom(t *testing.T) {
	for name, free := range map[string][2]uint64{
		"memory under the mark": {Room - 1, 8 << 30},
		"commit under the mark": {8 << 30, Room - 1},
	} {
		voice, record := engine(t, "hears", 8<<30)
		voice.Memory = func() (uint64, uint64, error) { return free[0], free[1], nil }
		_, err := voice.Recognize(context.Background(), []byte("RIFF-sound"))
		if err == nil || !strings.Contains(err.Error(), "1 GB") {
			t.Fatalf("%s: answered %v", name, err)
		}
		if _, statErr := os.Stat(record); !os.IsNotExist(statErr) {
			t.Fatalf("%s: the engine was started", name)
		}
	}
	// At the mark it runs.
	voice, _ := engine(t, "hears", Room)
	if _, err := voice.Recognize(context.Background(), []byte("RIFF-sound")); err != nil {
		t.Fatalf("with exactly the mark free it answered %v", err)
	}
}

func TestRecognizeSaysSoWhenTheModelWasNeverFetched(t *testing.T) {
	voice := &Voice{Settings: Settings{Engine: part("engine", "https://example.test/e.tar.bz2"), Model: part("model", "https://example.test/m.tar.bz2"), Program: "program.txt"}, Dir: t.TempDir(), Memory: func() (uint64, uint64, error) { return 8 << 30, 8 << 30, nil }}
	_, err := voice.Recognize(context.Background(), []byte("RIFF-sound"))
	if err == nil || !strings.Contains(err.Error(), "not fetched") {
		t.Fatalf("an engine that was never fetched answered %v", err)
	}
}

func TestTheShippedSettingsPinBothDownloads(t *testing.T) {
	settings, err := Load(filepath.Join("..", "..", "config", "voice.json"))
	if err != nil {
		t.Fatal(err)
	}
	if settings.Model.Name != "parakeet-tdt-110m" || settings.Engine.Name != "sherpa-onnx" {
		t.Fatalf("the shipped settings name %s on %s", settings.Model.Name, settings.Engine.Name)
	}
	if !slices.ContainsFunc(settings.Engine.Files, func(file string) bool { return path.Base(file) == settings.Program }) {
		t.Fatalf("the program %s is not one of the engine's files %v", settings.Program, settings.Engine.Files)
	}
	for _, arg := range settings.Args {
		_, file, found := strings.Cut(arg, "{model}/")
		if found && !strings.Contains(" "+strings.Join(settings.Model.Files, " ")+" ", " "+file+" ") {
			t.Errorf("%s names %s, which is not one of the model's files %v", arg, file, settings.Model.Files)
		}
	}
}

func TestAHomeUsesItsOwnSettingsAndOtherwiseTheBuilds(t *testing.T) {
	builtIn, err := os.ReadFile(filepath.Join("..", "..", "config", "voice.json"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	voice, err := For(root, builtIn)
	if err != nil {
		t.Fatal(err)
	}
	if voice.Settings.Model.Name != "parakeet-tdt-110m" || voice.Dir != filepath.Join(root, "caches", "voice") {
		t.Fatalf("a home with no settings of its own got %s in %s", voice.Settings.Model.Name, voice.Dir)
	}
	want := "parakeet-tdt-110m en-36000-int8 on sherpa-onnx 1.13.8, not fetched yet: the first dictation downloads it once, 125 MB, into " + voice.Dir
	if got := voice.Summary(); got != want {
		t.Fatalf("summary %q, want %q", got, want)
	}
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	own := strings.Replace(string(builtIn), `"parakeet-tdt-110m"`, `"another-model"`, 1)
	if err := os.WriteFile(filepath.Join(root, "config", "voice.json"), []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}
	if voice, err = For(root, builtIn); err != nil || voice.Settings.Model.Name != "another-model" {
		t.Fatalf("a home with settings of its own got %+v, %v", voice, err)
	}
	// Settings the home keeps and that pin nothing are refused, not replaced
	// by the build's.
	if err := os.WriteFile(filepath.Join(root, "config", "voice.json"), []byte(`{"engine":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if voice, err = For(root, builtIn); err == nil {
		t.Fatalf("a home whose settings pin nothing got %s", voice.Settings.Model.Name)
	}
}

func TestSummarySaysWhenTheModelIsThere(t *testing.T) {
	voice, _ := engine(t, "hears", 8<<30)
	want := "model 1.0 on engine 1.0, ready in " + voice.Dir
	if got := voice.Summary(); got != want {
		t.Fatalf("summary %q, want %q", got, want)
	}
}
