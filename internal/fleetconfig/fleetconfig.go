// Package fleetconfig reads the fleet's machine settings, config/fleet.json in
// the home: whether the board looks for a newer release, the free disk under
// which no goblin or gate starts and the lower mark at which the CFO is woken,
// how large the shared caches may grow, which temporary folders the janitor
// treats as the fleet's leaks, the GitHub organizations whose pull requests
// the fleet watches as its own, and the part of each provider's weekly
// allowance the fleet keeps back. How many goblins run is no setting:
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
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

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
	// WeeklyFloorPercent is, by provider, the percent of its weekly
	// allowance the fleet keeps back: once that little is left, its goblins
	// pause and none starts until the week resets. A provider it does not
	// name keeps DefaultWeeklyFloorPercent back; read it with WeeklyFloor.
	WeeklyFloorPercent map[string]float64 `json:"weekly_floor_percent"`
}

// DefaultWeeklyFloorPercent is the part of a provider's week a home that never
// set its floor keeps back.
const DefaultWeeklyFloorPercent = 5

// WeeklyFloorProviders are the providers whose week the fleet measures, the
// only ones a floor can be set for.
var WeeklyFloorProviders = []string{"claude", "codex"}

// WeeklyFloor is the percent of provider's weekly allowance the fleet keeps
// back. At 0 it keeps none: goblins run on the week until the provider itself
// refuses.
func (s Settings) WeeklyFloor(provider string) float64 {
	if floor, isSet := s.WeeklyFloorPercent[provider]; isSet {
		return floor
	}
	return DefaultWeeklyFloorPercent
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

// retiredKeys are settings an older build read and this one does not, with
// why. Read refuses a key it does not know, so an install or an update takes
// these out of a home's file, never leaving every read, and with it every
// start, refusing a setting the Overlord's home was given before.
var retiredKeys = []struct{ key, why string }{
	{"max_live_goblins", "goblin slots go by memory alone"},
}

// RetireKeys takes out of root's config/fleet.json the keys this build no
// longer reads, keeps every other key as it was, and says what it took out,
// or nothing when there was nothing to take. A missing file, and one that is
// not one JSON object, is left for Read to report.
func RetireKeys(root string) (string, error) {
	path := filepath.Join(root, "config", "fleet.json")
	data, err := fsx.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil || settings == nil {
		return "", nil
	}
	var taken []string
	for _, retired := range retiredKeys {
		if _, found := settings[retired.key]; found {
			delete(settings, retired.key)
			taken = append(taken, retired.key+" ("+retired.why+")")
		}
	}
	if len(taken) == 0 {
		return "", nil
	}
	kept, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	if err := fsx.AtomicWriteFile(path, append(kept, '\n')); err != nil {
		return "", fmt.Errorf("take retired settings out of %s: %w", path, err)
	}
	return "took " + strings.Join(taken, ", ") + " out of " + path + ", since this build no longer reads it", nil
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
	data, err := fsx.ReadFile(filepath.Join(root, "config", "fleet.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	return parse(data)
}

// parse reads data, config/fleet.json's content, over the defaults, refusing
// a file that does not mean what it says.
func parse(data []byte) (Settings, error) {
	settings := Defaults()
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
	for provider, floor := range settings.WeeklyFloorPercent {
		if !slices.Contains(WeeklyFloorProviders, provider) {
			return Settings{}, fmt.Errorf("weekly_floor_percent: %q is not a provider whose week the fleet measures, which are %s", provider, strings.Join(WeeklyFloorProviders, " and "))
		}
		if !(floor >= 0 && floor < 100) {
			return Settings{}, fmt.Errorf("weekly_floor_percent: %s's floor must be at least 0 and under 100 percent, not %v", provider, floor)
		}
	}
	return settings, nil
}

// SetWeeklyFloor sets provider's weekly floor in root's config/fleet.json to
// percent and keeps every other key as it was, writing the file when the home
// has none. It refuses, writing nothing, what Read would refuse after it.
func SetWeeklyFloor(root, provider string, percent float64) error {
	path := filepath.Join(root, "config", "fleet.json")
	data, err := fsx.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		data, err = []byte("{}"), nil
	}
	if err != nil {
		return err
	}
	if _, err := parse(data); err != nil {
		return err
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("config/fleet.json: %w", err)
	}
	floors := map[string]float64{}
	if kept, isSet := settings["weekly_floor_percent"]; isSet {
		if err := json.Unmarshal(kept, &floors); err != nil {
			return fmt.Errorf("config/fleet.json: weekly_floor_percent: %w", err)
		}
	}
	floors[provider] = percent
	if settings["weekly_floor_percent"], err = json.Marshal(floors); err != nil {
		return err
	}
	changed, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	changed = append(changed, '\n')
	if _, err := parse(changed); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, changed)
}
