package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Release is where a harness's publisher offers its newest version: the URL
// answering with it and the source's name for the line. A harness installed
// from npm names its Package, and npm installs the newest version of it; one
// with an installer of its own names the command, Update, that installs the
// newest.
type Release struct {
	URL     string
	Source  string
	Package string
	Update  string
}

// Releases are the harnesses' release sources by harness name.
type Releases map[string]Release

// HarnessReleases are where each harness this fleet starts publishes its
// newest version: Claude Code's own latest channel, which its installer and
// its updater read, and npm, which Codex and pi are installed from. Each is
// one unauthenticated GET that costs nothing.
var HarnessReleases = Releases{
	"claude": {URL: "https://downloads.claude.ai/claude-code-releases/latest", Source: "Claude Code's latest channel", Update: "claude update"},
	"codex":  {URL: "https://registry.npmjs.org/@openai/codex/latest", Source: "npm", Package: "@openai/codex"},
	"pi":     {URL: "https://registry.npmjs.org/@earendil-works/pi-coding-agent/latest", Source: "npm", Package: "@earendil-works/pi-coding-agent"},
}

// ReleasesVariable names a URL that stands in for every harness's release
// source, such as a stub server's in a test or a proof: each harness's newest
// version is read from it with the harness's name as the last path element,
// answered as Claude Code's channel or an npm manifest answers.
const ReleasesVariable = "CODE_GOBLINS_HARNESS_RELEASES"

// ReleasesFromEnvironment is HarnessReleases, or the releases ReleasesVariable
// names.
func ReleasesFromEnvironment() (Releases, error) {
	base := os.Getenv(ReleasesVariable)
	if base == "" {
		return HarnessReleases, nil
	}
	address, err := url.Parse(base)
	if err != nil || (address.Scheme != "https" && address.Scheme != "http") || address.Host == "" {
		return nil, fmt.Errorf("%s is %q, not an http or https address", ReleasesVariable, base)
	}
	releases := Releases{}
	for name, release := range HarnessReleases {
		release.URL = strings.TrimSuffix(base, "/") + "/" + name
		releases[name] = release
	}
	return releases, nil
}

// releaseTimeout bounds one release source's answer.
const releaseTimeout = 10 * time.Second

// HarnessVersion is a harness's installed version against the newest one its
// publisher offers: Update is the command that installs Newest, empty when
// the installed one is the newest, and Problem why the newest could not be
// read.
type HarnessVersion struct {
	Name      string
	Installed string
	Newest    string
	Source    string
	Update    string
	Problem   string
}

// Versions reads, all at once, the newest version of each harness the probes
// found working, in the probes' order. A harness the probe found broken or
// missing has no installed version to set beside it and is left out.
func (releases Releases) Versions(ctx context.Context, client *http.Client, probes []HarnessProbe) []HarnessVersion {
	versions := []HarnessVersion{}
	for _, probe := range probes {
		release, isKnown := releases[probe.Name]
		if installed := versionIn(probe.Detail); probe.OK && isKnown && installed != "" {
			versions = append(versions, HarnessVersion{Name: probe.Name, Installed: installed, Source: release.Source})
		}
	}
	var asked sync.WaitGroup
	for index := range versions {
		asked.Go(func() {
			version := &versions[index]
			release := releases[version.Name]
			newest, err := release.newest(ctx, client)
			if err != nil {
				version.Problem = err.Error()
				return
			}
			version.Newest = newest
			if newerVersion(newest, version.Installed) {
				version.Update = release.Update
				if release.Package != "" {
					version.Update = "npm install -g " + release.Package + "@" + newest
				}
			}
		})
	}
	asked.Wait()
	return versions
}

// newest asks the release source for its newest version.
func (release Release) newest(ctx context.Context, client *http.Client) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, releaseTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, release.URL, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s", release.Source, response.Status)
	}
	version := strings.TrimSpace(string(body))
	if release.Package != "" {
		var manifest struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(body, &manifest); err != nil {
			return "", fmt.Errorf("%s answered with no manifest: %w", release.Source, err)
		}
		version = manifest.Version
	}
	if !wholeVersion.MatchString(version) {
		return "", fmt.Errorf("%s answered with no version", release.Source)
	}
	return version, nil
}

// aVersion is a dotted version with an optional pre-release, as harnesses
// print theirs: "2.1.293 (Claude Code)", "codex-cli 0.160.0", "0.85.1".
var aVersion = regexp.MustCompile(`\b\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?\b`)

var wholeVersion = regexp.MustCompile(`^` + aVersion.String() + `$`)

// versionIn is the first version a harness's version line carries, or empty.
func versionIn(line string) string {
	return aVersion.FindString(line)
}

// newerVersion says whether version is newer than than: by major, minor and
// patch number, and at equal numbers a release is newer than a pre-release
// of it, since 0.158.0-alpha comes before 0.158.0.
func newerVersion(version, than string) bool {
	number, release, _ := strings.Cut(version, "-")
	thanNumber, thanRelease, _ := strings.Cut(than, "-")
	parts, thanParts := strings.Split(number, "."), strings.Split(thanNumber, ".")
	for index := range 3 {
		part, _ := strconv.Atoi(parts[index])
		thanPart, _ := strconv.Atoi(thanParts[index])
		if part != thanPart {
			return part > thanPart
		}
	}
	return release == "" && thanRelease != ""
}

// codexPackageFamily is the Codex desktop app's Windows package family, its
// name and publisher, which every version of the app keeps.
const codexPackageFamily = "OpenAI.Codex_2p2nqsd0c76g0"

var (
	packagesByFamily  = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetPackagesByPackageFamily")
	packagePathByName = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetPackagePathByFullName")
)

// DesktopCodex is the codex.exe the Codex desktop app bundles in its
// app\resources folder, found by the app's package family for this user, and
// whether the app is installed at all. Its folder under WindowsApps cannot be
// listed, and its name changes with every version of the app.
func DesktopCodex() (string, bool, error) {
	family, err := windows.UTF16PtrFromString(codexPackageFamily)
	if err != nil {
		return "", false, err
	}
	var count, length uint32
	result, _, _ := packagesByFamily.Call(uintptr(unsafe.Pointer(family)), uintptr(unsafe.Pointer(&count)), 0, uintptr(unsafe.Pointer(&length)), 0)
	if windows.Errno(result) != windows.ERROR_INSUFFICIENT_BUFFER {
		if result != 0 {
			return "", false, fmt.Errorf("the Codex desktop app's packages could not be listed: %w", windows.Errno(result))
		}
		return "", false, nil
	}
	names := make([]*uint16, count)
	buffer := make([]uint16, length)
	result, _, _ = packagesByFamily.Call(uintptr(unsafe.Pointer(family)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&names[0])), uintptr(unsafe.Pointer(&length)), uintptr(unsafe.Pointer(&buffer[0])))
	if result != 0 {
		return "", false, fmt.Errorf("the Codex desktop app's packages could not be listed: %w", windows.Errno(result))
	}
	var problems error
	for _, name := range names[:count] {
		var size uint32
		result, _, _ := packagePathByName.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&size)), 0)
		if windows.Errno(result) != windows.ERROR_INSUFFICIENT_BUFFER {
			problems = errors.Join(problems, fmt.Errorf("the Codex desktop app's folder could not be read: %w", windows.Errno(result)))
			continue
		}
		path := make([]uint16, size)
		result, _, _ = packagePathByName.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&path[0])))
		if result != 0 {
			problems = errors.Join(problems, fmt.Errorf("the Codex desktop app's folder could not be read: %w", windows.Errno(result)))
			continue
		}
		program := filepath.Join(windows.UTF16ToString(path), "app", "resources", "codex.exe")
		if info, err := os.Stat(program); err == nil && info.Mode().IsRegular() {
			return program, true, nil
		}
	}
	if problems != nil {
		return "", true, problems
	}
	return "", true, errors.New("the Codex desktop app is installed without a bundled codex.exe in app\\resources")
}
