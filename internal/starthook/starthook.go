// Package starthook finds the code a Python environment runs at every
// interpreter start, before any module a program imports: an executable line
// of a .pth file, and a sitecustomize or usercustomize module. A hijacked
// release ships one so that it runs even where its own package is never
// imported: on 2026-10-21 a release of litellm carried a .pth file that
// started a stealer at every Python start.
package starthook

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Hook is one start hook that no known package ships.
type Hook struct {
	// Path is the file or module folder that holds it.
	Path string
	// Package is the distribution whose RECORD installs it, as its
	// .dist-info folder names it, such as litellm-1.107.0, or empty when no
	// installed distribution names it.
	Package string
	// Installing reports that no installed distribution names it while its
	// environment's site-packages changed within installQuiet, as it does
	// while an install has written a file and not yet the RECORD that names
	// it.
	Installing bool
}

// installQuiet is how long an environment's site-packages stays unchanged
// before a hook no distribution names is taken as left there rather than as
// one an install is still writing.
const installQuiet = time.Minute

// knownHooks are the start hooks an environment carries with no release
// hijacked: each by its file, its executable line, and the distribution
// whose RECORD installs that file, so a release that writes one of these
// lines into a file of its own, or over the file of the package that ships
// it, is still found.
var knownHooks = []struct{ file, owner, line string }{
	// uv and virtualenv write it when they make the environment, and say so
	// in its pyvenv.cfg; no distribution installs it.
	{file: "_virtualenv.pth", line: "import _virtualenv"},
	{file: "distutils-precedence.pth", owner: "setuptools", line: "import os; var = 'SETUPTOOLS_USE_DISTUTILS'; enabled = os.environ.get(var, 'local') == 'local'; enabled and __import__('_distutils_hack').add_shim();"},
	{file: "pywin32.pth", owner: "pywin32", line: "import pywin32_bootstrap"},
	{file: "a1_coverage.pth", owner: "coverage", line: `import sys; exec('import os\n\nif os.getenv("COVERAGE_PROCESS_START") or os.getenv("COVERAGE_PROCESS_CONFIG"):\n try:\n  import coverage\n except:\n  pass\n else:\n  coverage.process_startup(slug="pth")')`},
	{file: "pytest-cov.pth", owner: "pytest_cov", line: `import os, sys;exec('if \'COV_CORE_SOURCE\' in os.environ:\n try:\n  from pytest_cov.embed import init\n  init()\n except Exception as exc:\n  sys.stderr.write(\n   "pytest-cov: Failed to setup subprocess coverage. "\n   "Environ: {0!r} "\n   "Exception: {1!r}\\n".format(\n    dict((k, v) for k, v in os.environ.items() if k.startswith(\'COV_CORE\')),\n    exc\n   )\n  )\n')`},
}

// lineBreaks are the runes Python's str.splitlines splits a line at.
var lineBreaks = []rune{'\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029}

// Find returns the start hooks no known package ships in the Python
// environments of worktree: each folder at its root that holds pyvenv.cfg,
// and the .venv of each folder one or two levels below its root, where uv
// makes one for a project in a subfolder such as services/api. The second
// level is looked for under the root's own folders, never under a dot
// folder, node_modules or a linked folder. A worktree with no environment
// costs a listing of its root and of each such folder, and a look at each
// entry they hold.
func Find(worktree string) ([]Hook, error) {
	entries, err := os.ReadDir(worktree)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var folders []string
	for _, entry := range entries {
		// A linked dependency folder is a junction, which is no directory
		// entry, and is looked into all the same.
		folder := filepath.Join(worktree, entry.Name())
		folders = append(folders, folder, filepath.Join(folder, ".venv"))
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
			continue
		}
		subfolders, err := os.ReadDir(folder)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, subfolder := range subfolders {
			if subfolder.IsDir() {
				folders = append(folders, filepath.Join(folder, subfolder.Name(), ".venv"))
			}
		}
	}
	var hooks []Hook
	for _, folder := range folders {
		if _, err := os.Lstat(filepath.Join(folder, "pyvenv.cfg")); err != nil {
			continue
		}
		found, err := environmentHooks(worktree, folder)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, found...)
	}
	return hooks, nil
}

// environment is one Python environment as the scan reads it.
type environment struct {
	worktree, folder string
	// packages is its site-packages folder, which holds each installed
	// distribution's .dist-info folder, listed in distributions.
	packages      string
	distributions []os.DirEntry
	modified      time.Time
}

// environmentHooks are the start hooks of the environment in folder. Python
// reads its site-packages folder at start and, on Windows, the
// environment's own folder as well, where a wheel's data files land.
func environmentHooks(worktree, folder string) ([]Hook, error) {
	sites, _ := filepath.Glob(filepath.Join(folder, "lib", "python*", "site-packages"))
	for _, packages := range append([]string{filepath.Join(folder, "Lib", "site-packages")}, sites...) {
		info, err := os.Stat(packages)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(packages)
		if err != nil {
			return nil, err
		}
		e := environment{worktree: worktree, folder: folder, packages: packages, distributions: distributions(entries, ""), modified: info.ModTime()}
		hooks, err := e.siteHooks(packages, entries)
		if err != nil {
			return nil, err
		}
		root, err := os.ReadDir(folder)
		if err != nil {
			return nil, err
		}
		rootHooks, err := e.siteHooks(folder, root)
		if err != nil {
			return nil, err
		}
		return append(hooks, rootHooks...), nil
	}
	return nil, nil
}

// siteHooks are the start hooks of one site folder, whose entries are
// listed: each .pth file there, and a customize module there or in a folder
// a .pth file's path line adds.
func (e environment) siteHooks(site string, entries []os.DirEntry) ([]Hook, error) {
	hooks := customizeModules(site, entries)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".pth") {
			continue
		}
		path := filepath.Join(site, name)
		data, err := fsx.ReadFile(path)
		if err != nil {
			return nil, err
		}
		isHook := false
		// Python decodes a .pth file as UTF-8 with an optional byte order
		// mark, or failing that in the locale's encoding, and splits it with
		// str.splitlines. Splitting at every rune that splits a line in either
		// decoding cuts at least where Python does, so a line it runs is
		// always a line here.
		for _, line := range strings.FieldsFunc(strings.TrimPrefix(string(data), string(rune(0xFEFF))), func(r rune) bool { return slices.Contains(lineBreaks, r) }) {
			switch {
			case strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "":
			case strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "import\t"):
				isHook = isHook || !e.known(path, strings.TrimRight(line, " \t"))
			default:
				added := pathLine(site, line)
				info, err := os.Stat(added)
				switch {
				case err != nil:
				case info.IsDir():
					if listed, err := os.ReadDir(added); err == nil {
						hooks = append(hooks, customizeModules(added, listed)...)
					}
				case archiveCustomizes(added):
					hooks = append(hooks, Hook{Path: added})
				}
			}
		}
		if isHook {
			hooks = append(hooks, Hook{Path: path})
		}
	}
	for index := range hooks {
		hooks[index].Package = e.owner(e.distributions, hooks[index].Path)
		hooks[index].Installing = hooks[index].Package == "" && time.Since(e.modified) < installQuiet
	}
	return hooks, nil
}

// pathLine is the folder or archive a .pth file's path line adds to
// sys.path, found as Python's site.addpackage finds it: the line without the
// trailing whitespace str.rstrip strips, joined to site as os.path.join
// joins a Windows path, so a rooted line keeps site's drive and a line that
// names site's drive with no root is relative to site.
func pathLine(site, line string) string {
	line = strings.TrimRightFunc(line, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
	volume := filepath.VolumeName(line)
	rest := line[len(volume):]
	isRooted := rest != "" && os.IsPathSeparator(rest[0])
	switch {
	case volume == "" && isRooted:
		return filepath.Join(filepath.VolumeName(site), rest)
	case volume == "", !isRooted && strings.EqualFold(volume, filepath.VolumeName(site)):
		return filepath.Join(site, rest)
	}
	return filepath.Join(volume, rest)
}

// archiveCustomizes reports whether the file at path is a zip archive that
// holds a sitecustomize or usercustomize module at its root, which Python's
// zipimport serves once a path line adds the archive.
func archiveCustomizes(path string) bool {
	file, err := fsx.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false
	}
	archive, err := zip.NewReader(file, info.Size())
	if err != nil {
		return false
	}
	for _, entry := range archive.File {
		top, _, _ := strings.Cut(entry.Name, "/")
		stem, _, _ := strings.Cut(top, ".")
		if strings.EqualFold(stem, "sitecustomize") || strings.EqualFold(stem, "usercustomize") {
			return true
		}
	}
	return false
}

// customizeModules are the sitecustomize and usercustomize modules of one
// folder, in any form Python imports: a source file, compiled code or a
// package folder.
func customizeModules(folder string, entries []os.DirEntry) []Hook {
	var hooks []Hook
	for _, entry := range entries {
		stem, _, _ := strings.Cut(entry.Name(), ".")
		if strings.EqualFold(stem, "sitecustomize") || strings.EqualFold(stem, "usercustomize") {
			hooks = append(hooks, Hook{Path: filepath.Join(folder, entry.Name())})
		}
	}
	return hooks
}

// known reports whether line of the .pth file at path is a start hook a
// known package ships, from that package: one of knownHooks, or any line of
// a distribution installed editable from a folder of this worktree, which is
// the project's own code and which its build backend's editable loader
// needs, as setuptools, meson-python and scikit-build-core write one.
func (e environment) known(path, line string) bool {
	for _, hook := range knownHooks {
		if filepath.Base(path) == hook.file && line == hook.line {
			if hook.owner == "" {
				return filepath.Dir(path) == e.packages && e.madeByVirtualenv()
			}
			return e.owner(distributions(e.distributions, hook.owner+"-"), path) != ""
		}
	}
	for _, distribution := range e.distributions {
		data, err := fsx.ReadFile(filepath.Join(e.packages, distribution.Name(), "direct_url.json"))
		var direct struct {
			URL     string `json:"url"`
			DirInfo struct {
				Editable bool `json:"editable"`
			} `json:"dir_info"`
		}
		if err != nil || json.Unmarshal(data, &direct) != nil || !direct.DirInfo.Editable || !e.inWorktree(direct.URL) {
			continue
		}
		if e.owner([]os.DirEntry{distribution}, path) != "" {
			return true
		}
	}
	return false
}

// madeByVirtualenv reports whether uv or virtualenv made the environment,
// which each records in its pyvenv.cfg, as python -m venv does not.
func (e environment) madeByVirtualenv() bool {
	data, err := fsx.ReadFile(filepath.Join(e.folder, "pyvenv.cfg"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, _, _ := strings.Cut(line, "=")
		if key = strings.TrimSpace(key); key == "uv" || key == "virtualenv" {
			return true
		}
	}
	return false
}

// inWorktree reports whether address, a direct_url.json url, is a folder of
// this worktree. An index serves no such address, and a release cannot know
// the worktree's folder.
func (e environment) inWorktree(address string) bool {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "file" {
		return false
	}
	folder := filepath.FromSlash(parsed.Path)
	if runtime.GOOS == "windows" {
		folder = strings.TrimPrefix(folder, `\`)
	}
	relative, err := filepath.Rel(e.worktree, folder)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// distributions are the .dist-info folders among entries whose name starts
// with prefix, without regard to case. A folder's name spells its
// distribution's with underscores for dashes, so a prefix that ends with the
// dash before a version names one distribution.
func distributions(entries []os.DirEntry, prefix string) []os.DirEntry {
	return slices.DeleteFunc(slices.Clone(entries), func(entry os.DirEntry) bool {
		name := strings.ToLower(entry.Name())
		return !entry.IsDir() || !strings.HasPrefix(name, strings.ToLower(prefix)) || !strings.HasSuffix(name, ".dist-info")
	})
}

// owner is the distribution among dists whose RECORD installs path, which
// a RECORD names relative to the site-packages folder, as ../../name for a
// data file in the environment's own folder, or empty when none does.
func (e environment) owner(dists []os.DirEntry, path string) string {
	relative, err := filepath.Rel(e.packages, path)
	if err != nil {
		return ""
	}
	relative = filepath.ToSlash(relative)
	for _, distribution := range dists {
		data, err := fsx.ReadFile(filepath.Join(e.packages, distribution.Name(), "RECORD"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			recorded, _, _ := strings.Cut(line, ",")
			if recorded == relative || strings.HasPrefix(recorded, relative+"/") {
				return strings.TrimSuffix(distribution.Name(), ".dist-info")
			}
		}
	}
	return ""
}
