package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fixtureSHA256 is the SHA-256 of testdata/part.tar.bz2, which holds
// part-1.0/bin/program.txt, part-1.0/bin/library.txt, part-1.0/docs/readme.txt
// and part-1.0/tokens.txt.
const fixtureSHA256 = "261765dae87693ca85c35908f16e592be0df4fda0001fbd1dedcede88f0e33e1"

// programSHA256 is the SHA-256 of testdata/program.tar.bz2.
const programSHA256 = "c6b75f138a27c197666042c48bc996a94ed72c11870014b33bb19b36a9e5120b"

// TestMain lets the test binary stand in for the engine program: started
// with VOICE_TEST_ENGINE set it answers as the engine would and exits.
func TestMain(m *testing.M) {
	if role := os.Getenv("VOICE_TEST_ENGINE"); role != "" {
		os.Exit(standInEngine(role, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// standInEngine records the arguments it was started with beside the sound
// file it was handed, then answers by role.
func standInEngine(role string, args []string) int {
	sound := args[len(args)-1]
	data, err := os.ReadFile(sound)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	record, _ := json.Marshal(struct {
		Args  []string `json:"args"`
		Sound string   `json:"sound"`
		Bytes int      `json:"bytes"`
	}{args, sound, len(data)})
	if err := os.WriteFile(os.Getenv("VOICE_TEST_RECORD"), record, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	if role == "fails" {
		fmt.Fprintln(os.Stderr, "Failed to read '"+sound+"'")
		return 255
	}
	// As the engine does: what it says about the run on its standard error,
	// here with a line no word may be read from, and the words on its
	// standard output.
	fmt.Fprintln(os.Stderr, `OfflineRecognizerConfig(model_config=OfflineModelConfig(tokens="tokens.txt"), "text": "from the log")`)
	fmt.Fprintln(os.Stderr, "recognizer created in 0.882 s")
	fmt.Fprintln(os.Stderr, "Started")
	fmt.Fprintln(os.Stderr, "Done!")
	fmt.Fprintln(os.Stderr, sound)
	fmt.Fprintln(os.Stdout, `{"lang": "", "emotion": "", "event": "", "text": " Check the \"Vercel\" deployment.", "timestamps": [0.00, 0.32], "tokens":[" Check", " the"], "words": []}`)
	fmt.Fprintln(os.Stderr, "----")
	fmt.Fprintln(os.Stderr, "num threads: 2")
	return 0
}

// quiet is a progress nobody listens to.
func quiet(string, int64, int64) {}

// refusesEverything fails the test that sends a request through it.
type refusesEverything struct{ t *testing.T }

func (r refusesEverything) RoundTrip(request *http.Request) (*http.Response, error) {
	r.t.Errorf("the network was asked for %s", request.URL)
	return nil, errors.New("no network in this test")
}

func part(name, url string) Part {
	return Part{Name: name, Version: "1.0", URL: url, SHA256: fixtureSHA256, Files: []string{"bin/program.txt", "bin/library.txt", "tokens.txt"}}
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
	valid := `{"engine":{"name":"engine","version":"1","url":"https://example.test/e.tar.bz2","sha256":"` + fixtureSHA256 + `","files":["bin/e.exe"]},
"model":{"name":"model","version":"2","url":"https://example.test/m.tar.bz2","sha256":"` + fixtureSHA256 + `","files":["tokens.txt"]},
"program":"e.exe","args":["--num-threads=2","--tokens={model}/tokens.txt"]}`
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
		"a checksum that is not SHA-256": {`"sha256":"` + fixtureSHA256 + `","files":["tokens.txt"]`, `"sha256":"abc","files":["tokens.txt"]`},
		"a part with no files":           {`"files":["tokens.txt"]`, `"files":[]`},
		"a file that leaves its folder":  {`"files":["tokens.txt"]`, `"files":["../tokens.txt"]`},
		"an archive that is not tar.bz2": {"https://example.test/m.tar.bz2", "https://example.test/m.zip"},
		"a field it does not know":       {`"program":"e.exe"`, `"program":"e.exe","cloud":true`},
		"a program that is not a file":   {`"program":"e.exe"`, `"program":"bin/e.exe"`},
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

func TestAFetchKeepsOnlyTheNamedFilesOfADownloadThatMatchesItsChecksum(t *testing.T) {
	server, asked := served(t)
	dir := t.TempDir()
	var progress []int64
	voice := &Voice{Dir: dir, Client: server.Client()}
	wanted := part("engine", server.URL+"/part.tar.bz2")
	if err := voice.ready(wanted); err == nil {
		t.Fatal("a part that was never fetched reads ready")
	}
	if err := voice.fetch(context.Background(), wanted, func(_ string, done, total int64) { progress = append(progress, done, total) }); err != nil {
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
	if len(progress) < 2 || progress[len(progress)-2] != 288 || progress[len(progress)-1] != 288 {
		t.Fatalf("progress ended at %v, want 288 of 288 bytes", progress)
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
	if err := voice.fetch(context.Background(), wanted, quiet); err != nil {
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
	err := voice.fetch(context.Background(), wrong, quiet)
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
	err := voice.fetch(context.Background(), missing, quiet)
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
	err := voice.fetch(context.Background(), wanted, quiet)
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
	if err := voice.fetch(context.Background(), wanted, quiet); err != nil {
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
	if err := (&Voice{Dir: other, Client: client}).fetch(context.Background(), wanted, quiet); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a changed file placed by hand answered %v", err)
	}
}

// engine is a Voice whose engine and model are fetched from the fixture and
// whose program is this test binary standing in for the engine.
func engine(t *testing.T, role string, free uint64) (*Voice, string) {
	t.Helper()
	server, _ := served(t)
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	settings := Settings{Engine: part("engine", server.URL+"/engine.tar.bz2"), Model: part("model", server.URL+"/model.tar.bz2"), Program: filepath.Base(self), Args: []string{"--num-threads=2", "--tokens={model}/tokens.txt", "--model-type=test"}}
	voice := &Voice{Settings: settings, Dir: dir, Client: server.Client(), Memory: func() (uint64, uint64, error) { return free, free, nil }}
	if err := voice.Fetch(context.Background(), quiet); err != nil {
		t.Fatal(err)
	}
	// From here on the network answers nothing: recognising must not ask it.
	voice.Client = &http.Client{Transport: refusesEverything{t}}
	// The stand-in engine is this test binary, placed where the engine's
	// program is looked for.
	program, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "engine-1.0", filepath.Base(self)), program, 0o700); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(t.TempDir(), "engine-run.json")
	t.Setenv("VOICE_TEST_ENGINE", role)
	t.Setenv("VOICE_TEST_RECORD", record)
	return voice, record
}

type engineRun struct {
	Args  []string `json:"args"`
	Sound string   `json:"sound"`
	Bytes int      `json:"bytes"`
}

func ran(t *testing.T, record string) engineRun {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the engine did not run: %v", err)
	}
	var run engineRun
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	return run
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
	want := []string{"--num-threads=2", "--tokens=" + model + "/tokens.txt", "--model-type=test", run.Sound}
	if strings.Join(run.Args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the engine was started with %q, want %q", run.Args, want)
	}
	if run.Bytes != len("RIFF-sound") {
		t.Fatalf("the engine read %d bytes of sound", run.Bytes)
	}
	if _, err := os.Stat(run.Sound); !os.IsNotExist(err) {
		t.Fatalf("the sound file %s was left behind: %v", run.Sound, err)
	}
}

func TestAnEngineThatFailsIsReportedAndItsSoundFileIsStillRemoved(t *testing.T) {
	voice, record := engine(t, "fails", 8<<30)
	_, err := voice.Recognize(context.Background(), []byte("RIFF-sound"))
	if err == nil || !strings.Contains(err.Error(), "Failed to read") {
		t.Fatalf("a failed engine answered %v", err)
	}
	run := ran(t, record)
	if _, err := os.Stat(run.Sound); !os.IsNotExist(err) {
		t.Fatalf("the sound file %s was left behind: %v", run.Sound, err)
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
	err = voice.fetch(context.Background(), wanted, quiet)
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
	if !strings.Contains(strings.Join(settings.Engine.Files, " "), "bin/"+settings.Program) {
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
	want := "parakeet-tdt-110m en-36000-int8 on sherpa-onnx 1.13.8, not fetched yet: the first dictation downloads it once into " + voice.Dir
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

// testdata/sherpa-onnx-1.13.8-stdout.txt is what the pinned engine printed
// on its standard output for one spoken line, as it was captured.
func TestTheWordsAreReadFromWhatThePinnedEnginePrints(t *testing.T) {
	printed, err := os.ReadFile(filepath.Join("testdata", "sherpa-onnx-1.13.8-stdout.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text, err := heard(printed)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Check the Versal Deployment for the website and rebase the branch on main."; text != want {
		t.Fatalf("heard %q, want %q", text, want)
	}
	if _, err := heard([]byte("Done!\n")); err == nil {
		t.Fatal("an answer without words was read as words")
	}
}
