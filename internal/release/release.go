// Package release finds the newest published release of Code Goblins and
// downloads its programs for an update. It keeps a program only when it
// matches the release's SHA256SUMS and, for a release whose install script
// names a publisher, only when Windows reports it validly signed by that
// publisher: the checks the one-line install makes, so an update never trusts
// a download less than an install of the same release would.
package release

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
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Repository publishes Code Goblins' releases.
const Repository = "fpresta0607/code-goblins"

// APIVariable names a URL that stands in for GitHub's latest release, such as
// a stub release server's in a test or a proof; the release's page and every
// program it lists must then come from that URL's host.
const APIVariable = "CODE_GOBLINS_RELEASE_API"

// Source is where releases are read: the latest release as GitHub's REST API
// answers it, what its page's address starts with, and what each program's
// download address starts with.
type Source struct {
	API       string
	Site      string
	Downloads string
}

// GitHub is the source every install updates from.
func GitHub() Source {
	site := "https://github.com/" + Repository + "/releases/"
	return Source{API: "https://api.github.com/repos/" + Repository + "/releases/latest", Site: site, Downloads: site + "download/"}
}

// SourceFromEnvironment is GitHub, or the source APIVariable names.
func SourceFromEnvironment() (Source, error) {
	api := os.Getenv(APIVariable)
	if api == "" {
		return GitHub(), nil
	}
	address, err := url.Parse(api)
	if err != nil || (address.Scheme != "https" && address.Scheme != "http") || address.Host == "" {
		return Source{}, fmt.Errorf("%s is %q, not an http or https address", APIVariable, api)
	}
	host := address.Scheme + "://" + address.Host + "/"
	return Source{API: api, Site: host, Downloads: host}, nil
}

// Release is a published release as GitHub's API describes it.
type Release struct {
	Tag        string    `json:"tag_name"`
	Page       string    `json:"html_url"`
	Notes      string    `json:"body"`
	Published  time.Time `json:"published_at"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []Asset   `json:"assets"`
}

// Asset is one file a release publishes.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Standing is how this build stands against the newest release.
type Standing string

const (
	// Available: the release is newer than this build.
	Available Standing = "available"
	// UpToDate: this build is the release.
	UpToDate Standing = "up-to-date"
	// Ahead: this build is newer than any release.
	Ahead Standing = "ahead"
	// FromSource: this build was made from a clone, with no release version,
	// so it updates from its clone.
	FromSource Standing = "source"
)

// version is a release's vMAJOR.MINOR.PATCH.
var version = regexp.MustCompile(`^v(\d{1,6})\.(\d{1,6})\.(\d{1,6})$`)

func parseVersion(text string) ([3]int, bool) {
	match := version.FindStringSubmatch(text)
	if match == nil {
		return [3]int{}, false
	}
	var parts [3]int
	for i := range parts {
		parts[i], _ = strconv.Atoi(match[i+1])
	}
	return parts, true
}

// StandingOf compares installed, this build's version, with latest, the
// newest release's tag, part by part as numbers.
func StandingOf(installed, latest string) Standing {
	have, ok := parseVersion(installed)
	if !ok {
		return FromSource
	}
	newest, _ := parseVersion(latest)
	for i := range have {
		switch {
		case newest[i] > have[i]:
			return Available
		case newest[i] < have[i]:
			return Ahead
		}
	}
	return UpToDate
}

// kept is the source's last answer, kept in the home's state, so a check
// that changes nothing costs GitHub nothing against its limit.
type kept struct {
	API     string    `json:"api"`
	ETag    string    `json:"etag"`
	Fetched time.Time `json:"fetched_at"`
	Release Release   `json:"release"`
}

func keptPath(stateDir string) string {
	return filepath.Join(stateDir, "update", "release.json")
}

// Bounds on what a release source and a release may send.
const (
	maxRecord  = 1 << 20
	maxSums    = 64 << 10
	maxScript  = 1 << 20
	maxProgram = 512 << 20
)

// Latest asks source for its latest release, with the ETag of the answer it
// keeps, and keeps the answer. A release it cannot offer, a draft, a
// prerelease, one whose tag is no version or whose page is on another site,
// is an error, as is a source that cannot be reached.
func Latest(ctx context.Context, client *http.Client, source Source, stateDir string) (Release, error) {
	previous, err := readKept(stateDir)
	if err != nil || previous.API != source.API {
		previous = kept{}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.API, nil)
	if err != nil {
		return Release{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "code-goblins-update")
	if previous.ETag != "" {
		request.Header.Set("If-None-Match", previous.ETag)
	}
	response, err := client.Do(request)
	if err != nil {
		return Release{}, fmt.Errorf("the release could not be read: %w", err)
	}
	defer response.Body.Close()
	now := time.Now().UTC()
	switch response.StatusCode {
	case http.StatusNotModified:
		if previous.ETag == "" {
			return Release{}, errors.New("the release source answered not modified to a first ask")
		}
		previous.Fetched = now
		return previous.Release, writeKept(stateDir, previous)
	case http.StatusOK:
	default:
		return Release{}, fmt.Errorf("the release source answered HTTP %d", response.StatusCode)
	}
	var release Release
	if err := json.NewDecoder(io.LimitReader(response.Body, maxRecord)).Decode(&release); err != nil {
		return Release{}, fmt.Errorf("the release source's answer is not a release: %w", err)
	}
	if err := offerable(release, source); err != nil {
		return Release{}, err
	}
	return release, writeKept(stateDir, kept{API: source.API, ETag: response.Header.Get("ETag"), Fetched: now, Release: release})
}

// Cached is the release source's last answer kept in the home's state, and
// when it was read, without asking the source.
func Cached(stateDir string, source Source) (Release, time.Time, bool) {
	previous, err := readKept(stateDir)
	if err != nil || previous.API != source.API || offerable(previous.Release, source) != nil {
		return Release{}, time.Time{}, false
	}
	return previous.Release, previous.Fetched, true
}

func offerable(release Release, source Source) error {
	switch {
	case release.Draft || release.Prerelease:
		return fmt.Errorf("release %s is not published for everyone", release.Tag)
	case !version.MatchString(release.Tag):
		return fmt.Errorf("the latest release's tag %q is not a version", release.Tag)
	case !strings.HasPrefix(release.Page, source.Site):
		return fmt.Errorf("release %s's page %q is not on %s", release.Tag, release.Page, source.Site)
	}
	return nil
}

func readKept(stateDir string) (kept, error) {
	data, err := fsx.ReadFile(keptPath(stateDir))
	if err != nil {
		return kept{}, err
	}
	var previous kept
	return previous, json.Unmarshal(data, &previous)
}

func writeKept(stateDir string, answer kept) error {
	data, err := json.MarshalIndent(answer, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(keptPath(stateDir)), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(keptPath(stateDir), data)
}

// whatsChanged opens the list of changes GitHub writes into a release's
// notes, and a heading of the same kind a person writes.
var whatsChanged = regexp.MustCompile(`(?i)^#{1,6}\s*what'?s\s+(changed|new)\b`)

// credit is what GitHub adds after each change: who made it and where.
var credit = regexp.MustCompile(`\s+by @\S+ in \S+$`)

// commitKind is a change's conventional-commit prefix, such as feat(board):.
var commitKind = regexp.MustCompile(`^[a-z]+(\([^)]*\))?!?:\s*`)

// WhatsNew is up to max lines of what a release changed, in plain words:
// the items under its notes' What's Changed or What's New heading, without
// who made each, the commit kind or Markdown marks. Notes with no such
// heading give none.
func WhatsNew(notes string, max int) []string {
	var lines []string
	listing := false
	for _, line := range strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			listing = whatsChanged.MatchString(line)
			continue
		}
		item, ok := strings.CutPrefix(line, "* ")
		if !ok {
			item, ok = strings.CutPrefix(line, "- ")
		}
		if !listing || !ok {
			continue
		}
		item = commitKind.ReplaceAllString(credit.ReplaceAllString(item, ""), "")
		item = strings.NewReplacer("**", "", "__", "", "`", "").Replace(item)
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		first, size := utf8.DecodeRuneInString(item)
		item = string(unicode.ToUpper(first)) + item[size:]
		if len(lines) == max {
			break
		}
		lines = append(lines, item)
	}
	return lines
}
