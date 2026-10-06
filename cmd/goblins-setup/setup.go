package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// repository and tag are the release that publishes this program, stamped in
// when it is built with -ldflags -X. A build that names no tag, as one from a
// clone, installs the latest release of Code Goblins' own repository.
var (
	repository = "fpresta0607/code-goblins"
	tag        = ""
)

// scriptURL is where the install script this program runs is published: its
// own release's, which installs that release's programs and no other, or the
// latest release's for a build that names no release.
func scriptURL() string {
	if tag == "" {
		return "https://github.com/" + repository + "/releases/latest/download/install.ps1"
	}
	return "https://github.com/" + repository + "/releases/download/" + tag + "/install.ps1"
}

// downloadWait bounds the download of the install script, which is small.
const downloadWait = time.Minute

// logVariable names the log the install script keeps every detail in, so the
// script writes to the one this program shows.
const logVariable = "CODE_GOBLINS_LOG"

// shownLines is how many of the log's last lines Show details shows.
const shownLines = 40

// The install script says what it is doing in plain lines, the ones the
// one-line install prints in a terminal, and keeps every other detail in its
// log:
//
//	[2/4] Check the download        step 2 of 4 begins; the steps before it are done
//	      Installing Claude Code    what the step under way is doing now
//	Note: <sentence>                something to know, such as that a home in use is kept
//	Failed: <sentence>              the install stopped: what happened and what to do
//
// Its other plain lines, such as the one that ends an install and the one that
// names the log, say what the window already shows.
var stepLine = regexp.MustCompile(`^\[(\d+)/\d+\] `)

const (
	doingIndent  = "      "
	notePrefix   = "Note: "
	failedPrefix = "Failed: "
)

// Progress is what one of the install's plain lines tells the window.
type Progress struct {
	// Step is the step under way, from 1, or 0 when the line begins none.
	Step int
	// Doing says what the step under way is doing now.
	Doing string
	// Note is a sentence to show until the install ends.
	Note string
}

// Setup installs Code Goblins by running the release's own install script,
// the one the one-line install runs, so there is one install and this
// program is only its window. Every destination is a field, so a test names
// a script and folders of its own and never installs anything.
type Setup struct {
	// Script is the address of the install script.
	Script string
	// Shell is the PowerShell that runs it.
	Shell string
	// Log is the file the install keeps every detail in.
	Log string
	// Client downloads the script.
	Client *http.Client
	// Env is the environment the script runs in; nil is this program's own.
	Env []string
}

// Install downloads the install script and runs it out of sight, handing
// what each of its plain lines says to report as it comes. It returns nil
// once the script has ended well, and otherwise an error whose text is one
// sentence: what stopped the install and what to do. The details are in the
// log.
func (s Setup) Install(ctx context.Context, report func(Progress)) error {
	if err := os.WriteFile(s.Log, []byte("Code Goblins Setup "+version()+", "+time.Now().Format(time.RFC1123)+"\r\n"), 0o644); err != nil {
		return fmt.Errorf("Setup could not write its log %s (%v); free some disk space, then try again.", s.Log, err)
	}
	report(Progress{Step: 1})
	folder, err := os.MkdirTemp("", "code-goblins-setup-")
	if err != nil {
		return s.fail("Setup could not make a folder in the temp folder; free some disk space, then try again.", err)
	}
	defer os.RemoveAll(folder)
	script := filepath.Join(folder, "install.ps1")
	s.keep("Downloading the install script from " + s.Script)
	if err := s.download(ctx, script); err != nil {
		return s.fail("Code Goblins could not be downloaded. Check the internet connection, then try again.", err)
	}

	// -File runs the script as the one-line install does, in a session of its
	// own. With no console and no input it asks nothing.
	command := execx.CommandContext(ctx, s.Shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
	command.Dir = folder
	environment := s.Env
	if environment == nil {
		environment = os.Environ()
	}
	command.Env = append(environment, logVariable+"="+s.Log)
	// Closing this program ends the install and everything it started.
	command.Cancel = func() error {
		return execx.Command("taskkill", "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F").Run()
	}
	output, err := command.StdoutPipe()
	if err != nil {
		return s.fail("Setup could not start the install; try again.", err)
	}
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		return s.fail("Windows PowerShell, which runs the install, could not be started; try again.", err)
	}
	failed := ""
	// What the script printed that is none of its plain lines, such as an
	// error PowerShell raised before the script could log it, is kept once
	// the script ends: until then the log is the script's to write.
	var stray []string
	lines := bufio.NewScanner(output)
	lines.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for lines.Scan() {
		line := strings.TrimRight(lines.Text(), " \t\r")
		switch progress, sentence, plain := readLine(line); {
		case sentence != "":
			failed = sentence
		case progress != Progress{}:
			report(progress)
		case !plain && strings.TrimSpace(line) != "":
			stray = append(stray, line)
		}
	}
	err = command.Wait()
	s.keep(stray...)
	switch {
	case ctx.Err() != nil:
		return s.fail("The install was stopped before it finished; run Setup again to finish it.", ctx.Err())
	case err != nil && failed != "":
		return s.fail(failed, err)
	case err != nil:
		return s.fail("The install stopped before it finished; Show details says where, and Try again picks up from there.", err)
	}
	return nil
}

// readLine reads one line the install script printed: the progress it
// reports, the sentence a failure says, and whether it is one of the
// script's plain lines at all.
func readLine(line string) (Progress, string, bool) {
	if match := stepLine.FindStringSubmatch(line); match != nil {
		step, _ := strconv.Atoi(match[1])
		return Progress{Step: step}, "", true
	}
	if doing, ok := strings.CutPrefix(line, doingIndent); ok && doing != "" && doing[0] != ' ' {
		return Progress{Doing: doing}, "", true
	}
	if note, ok := strings.CutPrefix(line, notePrefix); ok {
		return Progress{Note: note}, "", true
	}
	if sentence, ok := strings.CutPrefix(line, failedPrefix); ok {
		return Progress{}, sentence, true
	}
	return Progress{}, "", plainEnding.MatchString(line)
}

// plainEnding matches the script's other plain lines, which end an install
// and name its log.
var plainEnding = regexp.MustCompile(`^(Done: |The full log is )`)

// keep adds lines to the end of the log.
func (s Setup) keep(lines ...string) {
	if len(lines) == 0 {
		return
	}
	log, err := os.OpenFile(s.Log, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer log.Close()
	for _, line := range lines {
		fmt.Fprint(log, line+"\r\n")
	}
}

// fail keeps what went wrong in the log and returns sentence as the error.
func (s Setup) fail(sentence string, cause error) error {
	s.keep("Failed: "+sentence, "Because: "+cause.Error())
	return errors.New(sentence)
}

// Details is the log's last lines, which Show details shows.
func (s Setup) Details() string {
	data, err := fsx.ReadFile(s.Log)
	if err != nil {
		return "The log " + s.Log + " could not be read: " + err.Error()
	}
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), "\n")
	if len(lines) > shownLines {
		lines = lines[len(lines)-shownLines:]
	}
	return strings.Join(lines, "\n")
}

// download saves the install script to path.
func (s Setup) download(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, downloadWait)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Script, nil)
	if err != nil {
		return err
	}
	response, err := s.Client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", s.Script, response.Status)
	}
	var script bytes.Buffer
	if _, err := io.Copy(&script, response.Body); err != nil {
		return err
	}
	return os.WriteFile(path, script.Bytes(), 0o644)
}

// version is the release this program installs.
func version() string {
	if tag == "" {
		return "(latest release)"
	}
	return tag
}

// appName is the desktop app an install puts in the home; started with no
// arguments it opens Code Goblins.
const appName = "goblins-window.exe"

// openApp starts the app the install put in home, and outlives this program.
func openApp(home string) error {
	app := filepath.Join(home, appName)
	if _, err := os.Stat(app); errors.Is(err, fs.ErrNotExist) {
		return errors.New("Code Goblins is installed, but this release has no app to open; open Code Goblins from the Start menu.")
	}
	command := execx.Command(app)
	command.Dir = home
	if err := command.Start(); err != nil {
		return fmt.Errorf("Code Goblins is installed, but its app did not open (%v); open Code Goblins from the Start menu.", err)
	}
	return command.Process.Release()
}
