package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// hookFailuresNamed bounds the failures one wake names; the rest are counted.
const hookFailuresNamed = 5

// ingestHookFailures tells the CEO, in one wake, of the native hooks pi
// sessions could not run since the last cycle, earliest first, and removes
// their records. A pi extension writes each into nativehook.FailureDir rather
// than onto its session's prompt line, where the board read it as unsent text
// and left the session alone (#2428). A record that cannot be read is still
// counted, since a hook failed either way.
func (s *Store) ingestHookFailures() error {
	dir := nativehook.FailureDir(s.Home.State)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var failures []nativehook.Failure
	var taken []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".failure.json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := fsx.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		taken = append(taken, path)
		var failure nativehook.Failure
		if err := json.Unmarshal(data, &failure); err != nil || failure.Event == "" || failure.Outcome == "" {
			failure = nativehook.Failure{}
		}
		failures = append(failures, failure)
	}
	if len(taken) == 0 {
		return nil
	}
	slices.SortStableFunc(failures, func(a, b nativehook.Failure) int { return a.At.Compare(b.At) })
	var named []string
	for _, failure := range failures[:min(len(failures), hookFailuresNamed)] {
		if failure.Event == "" {
			named = append(named, "a failure whose record could not be read")
			continue
		}
		named = append(named, fmt.Sprintf("%s's native hook for %s %s", bounded(failure.Harness, 40), bounded(failure.Event, 80), bounded(failure.Outcome, 200)))
	}
	detail := "native_hook_failed: " + strings.Join(named, " | ")
	if more := len(taken) - len(named); more > 0 {
		detail += fmt.Sprintf(" | and %d more", more)
	}
	if _, err := wake.Append(s.Home.State, "notify", "native-hook", detail+"; next: check that the goblins hook program is in place, then look at the sessions that raised these events"); err != nil {
		return err
	}
	for _, path := range taken {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
