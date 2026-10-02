// Package voice is the board's dictation engine on this machine: a pinned
// speech model and the program that runs it, fetched once and checked against
// their checksums, then run on a sound file with no network.
package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Room is the free memory and the free commit a dictation needs. The engine
// takes about a quarter of it for about a second, so dictation refuses only
// when the machine has no room at all, never because the fleet is busy.
const Room = 1 << 30

// Part is one pinned download, the engine or the model: an archive, its
// SHA-256 and the files of it that are kept.
type Part struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	URL     string   `json:"url"`
	SHA256  string   `json:"sha256"`
	Files   []string `json:"files"`
}

// Settings is the one setting dictation has: which engine, which model, and
// how the engine is started on the model. In Args, {model} stands for the
// folder the model's files are kept in.
type Settings struct {
	Engine  Part     `json:"engine"`
	Model   Part     `json:"model"`
	Program string   `json:"program"`
	Args    []string `json:"args"`
	Threads int      `json:"threads"`
}

// Voice runs Settings from Dir, the folder its downloads are kept in. Memory
// reads the machine's free memory and free commit, in bytes.
type Voice struct {
	Settings Settings
	Dir      string
	Client   *http.Client
	Memory   func() (available, commit uint64, err error)
}

// NoRoom refuses a dictation the machine has no memory for.
type NoRoom struct{ Available, Commit uint64 }

func (e NoRoom) Error() string {
	gigabytes := func(bytes uint64) float64 { return float64(bytes) / (1 << 30) }
	return fmt.Sprintf("dictation needs 1 GB of free memory and 1 GB of free commit, and this PC has %.1f GB and %.1f GB", gigabytes(e.Available), gigabytes(e.Commit))
}

var (
	folderName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	sha256Hex  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// For returns the dictation engine of the home at root: the home's own
// config/voice.json when it keeps one, else the settings this build pins.
// Settings a home keeps and that cannot be read are an error, never a reason
// to use the build's.
func For(root string, builtIn []byte) (*Voice, error) {
	settings, err := Load(filepath.Join(root, "config", "voice.json"))
	if errors.Is(err, os.ErrNotExist) {
		settings, err = parse(builtIn)
	}
	if err != nil {
		return nil, err
	}
	return &Voice{Settings: settings, Dir: filepath.Join(root, "caches", "voice")}, nil
}

// Load reads the settings and refuses any that do not pin both downloads.
func Load(file string) (Settings, error) {
	data, err := fsx.ReadFile(file)
	if err != nil {
		return Settings{}, err
	}
	settings, err := parse(data)
	if err != nil {
		return Settings{}, fmt.Errorf("%s: %w", file, err)
	}
	return settings, nil
}

func parse(data []byte) (Settings, error) {
	var settings Settings
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return Settings{}, err
	}
	return settings, settings.check()
}

func (s Settings) check() error {
	for _, part := range []Part{s.Engine, s.Model} {
		if err := part.check(); err != nil {
			return err
		}
	}
	if s.Program == "" || filepath.Base(s.Program) != s.Program {
		return fmt.Errorf("program %q must be the name of one of the engine's files", s.Program)
	}
	if s.Threads < 1 {
		return errors.New("threads must be at least 1")
	}
	return nil
}

func (p Part) check() error {
	if !folderName.MatchString(p.Name) || !folderName.MatchString(p.Version) {
		return fmt.Errorf("a part needs a name and a version of letters, digits, dots and dashes; got %q %q", p.Name, p.Version)
	}
	address, err := url.Parse(p.URL)
	if err != nil || address.Scheme != "https" || address.Host == "" || !strings.HasSuffix(address.Path, ".tar.bz2") {
		return fmt.Errorf("%s: its download must be an https address of a .tar.bz2 archive, not %q", p.Name, p.URL)
	}
	if !sha256Hex.MatchString(p.SHA256) {
		return fmt.Errorf("%s: its sha256 must be 64 lowercase hexadecimal digits", p.Name)
	}
	if len(p.Files) == 0 {
		return fmt.Errorf("%s: it names no files to keep", p.Name)
	}
	kept := map[string]bool{}
	for _, file := range p.Files {
		if file == "" || path.Clean(file) != file || path.IsAbs(file) || file == ".." || strings.HasPrefix(file, "../") || strings.ContainsAny(file, `\:`) {
			return fmt.Errorf("%s: %q is not a file inside the archive", p.Name, file)
		}
		if kept[path.Base(file)] {
			return fmt.Errorf("%s: two files are named %s, and the files are kept in one folder", p.Name, path.Base(file))
		}
		kept[path.Base(file)] = true
	}
	return nil
}

// folder is where a part's files are kept: another version is another folder.
func (v *Voice) folder(part Part) string {
	return filepath.Join(v.Dir, part.Name+"-"+part.Version)
}

// Ready says what is missing, or nil when the engine and the model are there.
func (v *Voice) Ready() error {
	for _, part := range []Part{v.Settings.Engine, v.Settings.Model} {
		if err := v.ready(part); err != nil {
			return err
		}
	}
	return nil
}

// Name is the model's name.
func (v *Voice) Name() string { return v.Settings.Model.Name }

// Summary names the model and the engine and says whether they are there.
func (v *Voice) Summary() string {
	name := fmt.Sprintf("%s %s on %s %s", v.Settings.Model.Name, v.Settings.Model.Version, v.Settings.Engine.Name, v.Settings.Engine.Version)
	if v.Ready() != nil {
		return name + ", not fetched yet: the first dictation downloads it once into " + v.Dir
	}
	return name + ", ready in " + v.Dir
}

// Fetch downloads whichever of the engine and the model is not there,
// telling progress how much of a part's download has arrived.
func (v *Voice) Fetch(ctx context.Context, progress func(part string, done, total int64)) error {
	for _, part := range []Part{v.Settings.Engine, v.Settings.Model} {
		if v.ready(part) == nil {
			continue
		}
		if err := v.fetch(ctx, part, progress); err != nil {
			return err
		}
	}
	return nil
}

// Recognize returns the words in sound, a WAV file's bytes. The sound is
// written where only this user can read it, for as long as the engine runs.
func (v *Voice) Recognize(ctx context.Context, sound []byte) (string, error) {
	if err := v.Ready(); err != nil {
		return "", err
	}
	available, commit, err := v.Memory()
	if err != nil {
		return "", fmt.Errorf("read the machine's memory: %w", err)
	}
	if available < Room || commit < Room {
		return "", NoRoom{Available: available, Commit: commit}
	}
	file, err := os.CreateTemp("", "cfo-dictation-*.wav")
	if err != nil {
		return "", err
	}
	defer func() { _ = fsx.Remove(file.Name()) }()
	_, err = file.Write(sound)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	engine := v.folder(v.Settings.Engine)
	args := []string{fmt.Sprintf("--num-threads=%d", v.Settings.Threads)}
	for _, arg := range v.Settings.Args {
		args = append(args, strings.ReplaceAll(arg, "{model}", v.folder(v.Settings.Model)))
	}
	command := execx.CommandContext(ctx, filepath.Join(engine, v.Settings.Program), append(args, file.Name())...)
	command.Dir = engine
	var log bytes.Buffer
	command.Stderr = &log
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("the dictation engine failed: %w: %s", err, lastLines(log.Bytes(), 3))
	}
	return heard(output)
}

// heard reads the words from what the engine printed on its standard output:
// one JSON object with the text beside its timestamps. What it says about the
// run goes to its standard error and is not read for words.
func heard(output []byte) (string, error) {
	_, after, found := strings.Cut(string(output), `"text":`)
	if !found {
		return "", fmt.Errorf("the dictation engine answered without words: %s", lastLines(output, 3))
	}
	var text string
	if err := json.NewDecoder(strings.NewReader(after)).Decode(&text); err != nil {
		return "", fmt.Errorf("the dictation engine's words could not be read: %w", err)
	}
	return strings.TrimSpace(text), nil
}

func lastLines(output []byte, count int) string {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(output), "\r\n", "\n")), "\n")
	return strings.Join(lines[max(0, len(lines)-count):], "; ")
}
