// Package fleetconfig reads the fleet's machine settings, config/fleet.json in
// the home: whether the board looks for a newer release, the free disk under
// which no goblin or gate starts and the lower mark at which the CFO is woken,
// how large the shared caches may grow, which temporary folders the janitor
// treats as the fleet's leaks, and the GitHub organizations whose pull
// requests the fleet watches as its own. How many goblins run is no setting:
// memory alone decides it. A missing file is the defaults; a file that does
// not mean what it says is refused rather than half read.
package fleetconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Settings are config/fleet.json.
type Settings struct {
	// DiskFloorGB is the free disk, in gigabytes, under which spawn, Start,
	// Resume and gate test runs are refused.
	DiskFloorGB float64 `json:"disk_floor_gb"`
	// DiskWakeGB is the lower mark under which the CFO is woken.
	DiskWakeGB float64 `json:"disk_wake_gb"`
	// CachesCapGB is the size, in gigabytes, the home's caches folder is
	// trimmed back under.
	CachesCapGB float64 `json:"caches_cap_gb"`
	// TempPatterns name the folders in the machine's temporary folder the
	// fleet's tests and tools leave behind, as filepath.Match patterns.
	TempPatterns []string `json:"temp_patterns"`
	// CheckForUpdates lets the board look for a newer release of Code
	// Goblins when it starts and every few hours; false turns the look off.
	CheckForUpdates bool `json:"check_for_updates"`
	// GitHubOwners name the GitHub organizations, and any account besides
	// the one gh works as, whose repositories the fleet owns: a teammate's
	// pull request there is watched as the fleet's own are.
	GitHubOwners []string `json:"github_owners"`
}

// Defaults are the settings a home with no config/fleet.json runs under.
func Defaults() Settings {
	return Settings{
		DiskFloorGB: 15,
		DiskWakeGB:  10,
		CachesCapGB: 20,
		// The leaks the CFO removed by hand on 2026-10-02: Go test homes,
		// Go's own build folders, cfo fixtures, Playwright and Chrome
		// profiles, audit runs and PrecisionDocs test homes.
		TempPatterns:    []string{"Test*", "go-build*", "cfo-*", "playwright*", "scoped_dir*", "fallow-audit*", "pd-*"},
		CheckForUpdates: true,
	}
}

// githubLogin is a GitHub account or organization name.
var githubLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// gigabyte is a GiB, the unit the meter and the floors are read in.
const gigabyte = 1 << 30

// Bytes is gigabytes as a byte count.
func Bytes(gigabytes float64) uint64 {
	return uint64(gigabytes * gigabyte)
}

// Read reads root's config/fleet.json over the defaults: a key it holds
// replaces the default and a key it leaves out keeps it.
func Read(root string) (Settings, error) {
	settings := Defaults()
	data, err := fsx.ReadFile(filepath.Join(root, "config", "fleet.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return Settings{}, err
	}
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return Settings{}, errors.New("config/fleet.json must contain one JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return Settings{}, fmt.Errorf("config/fleet.json: %w", err)
	}
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return Settings{}, errors.New("config/fleet.json must contain one JSON object")
	}
	if settings.DiskWakeGB < 0 || settings.DiskFloorGB < settings.DiskWakeGB {
		return Settings{}, errors.New("disk_wake_gb must be at least 0 and no more than disk_floor_gb")
	}
	if settings.CachesCapGB <= 0 {
		return Settings{}, errors.New("caches_cap_gb must be more than 0")
	}
	for _, pattern := range settings.TempPatterns {
		if _, err := filepath.Match(pattern, ""); err != nil || pattern == "" || pattern == "*" || filepath.Base(pattern) != pattern {
			return Settings{}, fmt.Errorf("temp_patterns: %q is not a folder-name pattern narrower than *", pattern)
		}
	}
	for _, owner := range settings.GitHubOwners {
		if !githubLogin.MatchString(owner) {
			return Settings{}, fmt.Errorf("github_owners: %q is not a GitHub account or organization name", owner)
		}
	}
	return settings, nil
}
