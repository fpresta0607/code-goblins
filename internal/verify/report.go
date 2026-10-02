package verify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ReportVersion is the version of the report this build writes.
const ReportVersion = 1

// keptReports is how many of a project's reports the store keeps.
const keptReports = 20

// Report is what one verification run planned and did.
type Report struct {
	Version int `json:"version"`
	// Project names the repository, Root is where it is checked out and Task
	// the fleet task that ran it, when one did.
	Project string `json:"project"`
	Task    string `json:"task,omitempty"`
	Root    string `json:"root"`
	// Commit is the commit checked out, Uncommitted how many files differ
	// from it, modified or untracked, and Base where the branch left the
	// default branch.
	Commit      string `json:"commit"`
	Uncommitted int    `json:"uncommitted"`
	Base        string `json:"base"`
	// Level is the level run, RequiredLevel the level the change requires
	// before it merges and RequiredWhy the reason it requires that one.
	Level         string `json:"level"`
	RequiredLevel string `json:"required_level"`
	RequiredWhy   string `json:"required_why"`
	// Policy says which policy the run followed and where it was read, and
	// Toolchain what built and ran the checks.
	Policy    string `json:"policy"`
	Toolchain string `json:"toolchain"`
	// Selected are the packages the change reaches and Left the checks the
	// level left to a broader one, each with why.
	Selected []Selection `json:"selected,omitempty"`
	Left     []Left      `json:"left,omitempty"`
	// Checks are the commands of the level, each with what became of it.
	Checks []Result `json:"checks"`
	// Status is passed only when every check ran and passed.
	Status          string    `json:"status"`
	Start           time.Time `json:"start"`
	DurationSeconds float64   `json:"duration_seconds"`
	// Log is the file holding everything the checks wrote.
	Log string `json:"log,omitempty"`
}

// Selection is a package a run selected, and why.
type Selection struct {
	Package string `json:"package"`
	Why     string `json:"why"`
}

// Left is a check a run left to a broader level, and why.
type Left struct {
	Check string `json:"check"`
	Why   string `json:"why"`
}

// StoreDir is the folder verification runs record into: the one
// CFO_VERIFY_DIR names, else cfo/verify in the user's cache folder, beside
// the fleet's Go temporary directories. A test binary that names none is
// refused, as internal/home refuses the inherited fleet home, so no test
// writes into the store real runs read.
func StoreDir() (string, error) {
	name := strings.ToLower(filepath.Base(os.Args[0]))
	return storeDir(os.Getenv("CFO_VERIFY_DIR"), strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".test.exe"))
}

func storeDir(override string, testBinary bool) (string, error) {
	if override != "" {
		return override, nil
	}
	if testBinary {
		return "", errors.New("verify: a test must name its own report store in CFO_VERIFY_DIR; the default is the store real runs read")
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("verify: resolve user cache directory: %w", err)
	}
	return filepath.Join(cache, "cfo", "verify"), nil
}

// Begin makes a run's log in the store, open for the run's output, and
// returns it with the path the run's report goes to. Both sit under the
// project's name and are named for when the run started, its commit and its
// level, with a suffix of their own, so runs that start together never share
// a file.
func Begin(project string, start time.Time, commit, level string) (*os.File, string, error) {
	store, err := StoreDir()
	if err != nil {
		return nil, "", err
	}
	dir := filepath.Join(store, "reports", folder(project))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	log, err := os.CreateTemp(dir, fmt.Sprintf("%s-%.8s-%s-*.log", start.UTC().Format("20060102T150405Z"), commit, level))
	if err != nil {
		return nil, "", err
	}
	return log, strings.TrimSuffix(log.Name(), ".log") + ".json", nil
}

// folder is a project's name as the name of one folder: letters, digits,
// dots, underscores and hyphens are kept and anything else becomes an
// underscore, as does a name of dots alone, which would name another folder.
func folder(project string) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, project)
	if strings.Trim(name, ".") == "" {
		return "_"
	}
	return name
}

// Finish writes a run's report to path, the one Begin gave, and keeps the
// project's newest reports: an older one is removed with its log. A log with
// no report belongs to a run that has not finished and is left alone, and a
// file another process holds open stays for a later run to remove.
func Finish(path string, report Report) error {
	if err := Save(path, report); err != nil {
		return err
	}
	reports, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.json"))
	if err != nil {
		return err
	}
	slices.Sort(reports)
	for _, old := range reports[:max(0, len(reports)-keptReports)] {
		os.Remove(strings.TrimSuffix(old, ".json") + ".log")
		os.Remove(old)
	}
	return nil
}
