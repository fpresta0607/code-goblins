package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
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

// shownLines is how many of the install's last lines a failure shows.
const shownLines = 8

// Setup installs Code Goblins by running the release's own install script,
// the one the one-line install runs, so there is one install and this
// program is only its window. Every destination is a field, so a test names
// a script and folders of its own and never installs anything.
type Setup struct {
	// Script is the address of the install script.
	Script string
	// Shell is the PowerShell that runs it.
	Shell string
	// Log is the file every line of the install is kept in.
	Log string
	// Client downloads the script.
	Client *http.Client
}

// Install downloads the install script and runs it out of sight, handing
// each line it prints to report as it comes. It returns nil once the script
// has ended well, and otherwise an error that says in plain words what
// stopped the install, with the script's own last lines.
func (s Setup) Install(ctx context.Context, report func(line string)) error {
	log, err := os.Create(s.Log)
	if err != nil {
		return fmt.Errorf("the install's log %s could not be written: %w", s.Log, err)
	}
	defer log.Close()
	say := func(line string) {
		fmt.Fprintln(log, line)
		report(line)
	}

	say("Downloading the installer from " + s.Script + " ...")
	folder, err := os.MkdirTemp("", "code-goblins-setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(folder)
	script := filepath.Join(folder, "install.ps1")
	if err := s.download(ctx, script); err != nil {
		return err
	}

	// -File runs the script as the one-line install does, in a session of its
	// own. With no console and no input it asks nothing: it records no
	// projects folder and its quick start stops at its first question, after
	// starting the supervisor, and the app's first-run page takes it from
	// there.
	command := execx.CommandContext(ctx, s.Shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
	command.Dir = folder
	// Closing this program ends the install and everything it started.
	command.Cancel = func() error {
		return execx.Command("taskkill", "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F").Run()
	}
	output, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		return fmt.Errorf("Windows PowerShell, which runs the install, could not be started: %w", err)
	}
	var last []string
	lines := bufio.NewScanner(output)
	lines.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for lines.Scan() {
		// A tool that draws its progress over itself ends each state with a
		// carriage return; the last one is what its line came to.
		line := strings.TrimRight(lines.Text(), " \t\r")
		if at := strings.LastIndexByte(line, '\r'); at >= 0 {
			line = line[at+1:]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		say(line)
		if last = append(last, line); len(last) > shownLines {
			last = last[1:]
		}
	}
	if err := command.Wait(); err != nil {
		if ctx.Err() != nil {
			return errors.New("the install was stopped before it finished; run this again to finish it")
		}
		return fmt.Errorf("the install stopped (%v). Its last lines:\n\n%s", err, strings.Join(last, "\n"))
	}
	return nil
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
		return fmt.Errorf("the installer could not be downloaded from %s: %w. Check the connection to the internet, then run this again", s.Script, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("the installer could not be downloaded from %s: the server answered %s", s.Script, response.Status)
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, response.Body); err != nil {
		file.Close()
		return fmt.Errorf("the installer could not be downloaded from %s: %w", s.Script, err)
	}
	return file.Close()
}

// appName is the desktop app an install puts in the home; started with no
// arguments it opens Code Goblins.
const appName = "goblins-window.exe"

// openApp starts the app the install put in home, and outlives this program.
func openApp(home string) error {
	app := filepath.Join(home, appName)
	if _, err := os.Stat(app); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("Code Goblins is installed in %s, but this release has no desktop app in it; open a terminal and run: goblins", home)
	}
	command := execx.Command(app)
	command.Dir = home
	if err := command.Start(); err != nil {
		return fmt.Errorf("Code Goblins is installed, but its app %s did not start: %w", app, err)
	}
	return command.Process.Release()
}
