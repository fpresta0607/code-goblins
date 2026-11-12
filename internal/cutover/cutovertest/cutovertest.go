// Package cutovertest names, for a test outside the cutover, what a build
// from before the rename left in a home, by the names only the cutover
// spells.
package cutovertest

import (
	"encoding/json"
	"path/filepath"
	"time"
)

const (
	// Program is the program a build before the rename installed in the
	// home's bin, beside goblins.exe, by which such a home is known.
	Program = "cfo.exe"
	// HomeSetting is the setting such a build finds its home by.
	HomeSetting = "CFO_HOME"
)

// FinishedUpdate is the journal a build before the rename wrote, in the
// update folder of the state at state, of an update of the programs in bin
// that finished: it names two aliases, its own program and goblins.exe, each
// with its verified copy, as every release up to v0.5.18 wrote it.
func FinishedUpdate(bin, state string, at time.Time) ([]byte, error) {
	folder := filepath.Join(state, "update")
	alias := func(name string) map[string]string {
		return map[string]string{"name": name, "previous_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "backup": filepath.Join(folder, "previous-"+name)}
	}
	return json.MarshalIndent(map[string]any{
		"schema":           "cfo-update.v1",
		"root":             bin,
		"phase":            "done",
		"candidate":        filepath.Join(bin, "..", "download", Program),
		"candidate_copy":   filepath.Join(folder, "candidate.exe"),
		"candidate_sha256": "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
		"aliases":          []map[string]string{alias(Program), alias("goblins.exe")},
		"started":          at.UTC(),
		"updated":          at.UTC(),
		"outcome":          "the candidate serves",
	}, "", "  ")
}
