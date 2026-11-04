// Package crawler is the crawler connector every goblin carries: the
// goblin-crawl command, built on Crawlee, which finds every route and file of
// a site and reads each page as headless Chrome draws it, obeying robots.txt,
// a few requests at a time, under a user agent that names it
// (docs/crawler.md). The home keeps it in connectors\crawler, its packages
// installed from the lockfile the build ships, and a goblin's terminal finds
// it on PATH unless the goblin's brief leaves it out.
package crawler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

const (
	// Name is the connector's name on a goblin's Connections.
	Name = "crawler"
	// Command is what a goblin runs to crawl a site.
	Command = "goblin-crawl"
	// PathVariable names, in a goblin's launch, the folder of Command, which
	// its terminal puts on PATH. A launch without it carries no crawler.
	PathVariable = "CFO_CRAWLER_BIN"
	// SourceDir is where the build carries the connector's files.
	SourceDir = "connectors/crawler"
)

// files are the connector's own files, which the build carries and the home
// keeps: its package, the lockfile that pins every package it installs, and
// its code.
var files = []string{"package.json", "package-lock.json", "goblin-crawl.mjs", "crawl-site.mjs"}

// shims start Command from any shell a goblin runs: cmd and PowerShell find
// the .cmd, and Git Bash the script without an extension.
var shims = map[string]string{
	Command + ".cmd": "@node \"%~dp0..\\goblin-crawl.mjs\" %*\r\n",
	Command:          "#!/bin/sh\nexec node \"$(dirname \"$0\")/../goblin-crawl.mjs\" \"$@\"\n",
}

// installedPackage is a file npm ci leaves only once it has installed the
// packages the connector runs on.
var installedPackage = filepath.Join("node_modules", "@crawlee", "playwright", "package.json")

// Dir is where the home keeps the connector.
func Dir(homeRoot string) string {
	return filepath.Join(homeRoot, "connectors", Name)
}

// BinDir is the folder of Command, which a goblin's terminal puts on PATH.
func BinDir(homeRoot string) string {
	return filepath.Join(Dir(homeRoot), "bin")
}

// IsCommandIn reports whether dir holds Command, as a goblin's terminal
// would find it there.
func IsCommandIn(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, Command+".cmd"))
	return err == nil && info.Mode().IsRegular()
}

// State is how far the home's connector is from the one the build ships.
type State int

const (
	// Ready is the build's connector, installed.
	Ready State = iota
	// Missing is a home with no connector, or one whose install did not end.
	Missing
	// Stale is a connector an earlier build installed, whose packages the
	// build pins otherwise.
	Stale
)

// Check reads how the connector in homeRoot stands against the one source
// carries.
func Check(homeRoot string, source fs.FS) (State, error) {
	dir := Dir(homeRoot)
	if !IsCommandIn(BinDir(homeRoot)) {
		return Missing, nil
	}
	if _, err := os.Stat(filepath.Join(dir, installedPackage)); err != nil {
		return Missing, nil
	}
	for _, name := range files {
		want, err := fs.ReadFile(source, SourceDir+"/"+name)
		if err != nil {
			return Missing, fmt.Errorf("crawler: the build carries no %s: %w", name, err)
		}
		have, err := fsx.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			return Stale, nil
		}
		if err != nil {
			return Missing, fmt.Errorf("crawler: read %s: %w", name, err)
		}
		if !bytes.Equal(have, want) {
			return Stale, nil
		}
	}
	return Ready, nil
}

// Setup makes the connector in homeRoot the one source carries: it writes
// the connector's files, installs its packages with npm ci, which installs
// exactly what the lockfile names, and last writes the commands that start
// it, so a goblin finds Command only once everything it runs on is in place.
// A connector already Ready is left as it is. It returns the state it found.
func Setup(ctx context.Context, homeRoot string, source fs.FS, runner execx.Runner) (State, error) {
	found, err := Check(homeRoot, source)
	if err != nil || found == Ready {
		return found, err
	}
	dir, bin := Dir(homeRoot), BinDir(homeRoot)
	for name := range shims {
		if err := fsx.Remove(filepath.Join(bin, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return found, fmt.Errorf("crawler: take out the old %s: %w", name, err)
		}
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return found, fmt.Errorf("crawler: make %s: %w", bin, err)
	}
	for _, name := range files {
		data, err := fs.ReadFile(source, SourceDir+"/"+name)
		if err != nil {
			return found, fmt.Errorf("crawler: the build carries no %s: %w", name, err)
		}
		if err := fsx.AtomicWriteFile(filepath.Join(dir, name), data); err != nil {
			return found, fmt.Errorf("crawler: write %s: %w", name, err)
		}
	}
	result, err := runner.Run(ctx, execx.Request{Name: "npm.cmd", Args: []string{"ci", "--omit=dev", "--no-audit", "--no-fund"}, Dir: dir, KillTree: true})
	if err != nil {
		return found, fmt.Errorf("crawler: npm ci in %s did not run: %w", dir, err)
	}
	if result.ExitCode != 0 {
		return found, fmt.Errorf("crawler: npm ci in %s exited with code %d: %s", dir, result.ExitCode, lastLine(result.Stderr))
	}
	for name, text := range shims {
		if err := fsx.AtomicWriteFile(filepath.Join(bin, name), []byte(text)); err != nil {
			return found, fmt.Errorf("crawler: write %s: %w", name, err)
		}
	}
	return found, nil
}

func lastLine(output []byte) string {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
