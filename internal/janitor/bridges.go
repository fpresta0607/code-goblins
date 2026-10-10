package janitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/reap"
)

// browserSessionFiles are the files chrome-devtools-axi keeps in a session's
// folder: its bridge's pid and port, and which page and snapshot its
// commands last used.
var browserSessionFiles = []string{"bridge.pid", "snapshot-generation", "selected-page-id"}

// removeStaleBrowserSessions removes the session folders chrome-devtools-axi
// left in cfg.BrowserSessions. The tool makes one for each named session and
// removes none: 233 had gathered by 2026-10-09, every one naming a bridge
// that no longer ran. A folder goes once nothing in it was written for a
// day and the bridge its pid file names no longer runs, and only when it
// holds nothing but the tool's own files, so nothing a person put there is
// ever removed. Its session starts again from nothing the next time it is
// used.
func (cfg Config) removeStaleBrowserSessions(record *Record) {
	if cfg.BrowserSessions == "" {
		return
	}
	entries, err := os.ReadDir(cfg.BrowserSessions)
	if err != nil {
		if !os.IsNotExist(err) {
			record.Notes = append(record.Notes, "the browser sessions folder could not be read, so none was removed: "+err.Error())
		}
		return
	}
	for _, entry := range entries {
		path := filepath.Join(cfg.BrowserSessions, entry.Name())
		if !entry.IsDir() || !cfg.isStaleBrowserSession(path) {
			continue
		}
		newest, bytes, err := newestWrite(path)
		if err != nil || cfg.Now.Sub(newest) < tempAge {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			record.Notes = append(record.Notes, "a stale browser session could not be removed, so the next sweep tries again: "+err.Error())
			continue
		}
		record.Removed = append(record.Removed, Item{Kind: "browser-session", Path: path, Bytes: bytes, Detail: "its bridge no longer runs and nothing wrote to it for a day"})
	}
}

// browserBridgeUse says when each running bridge's session was last used, by
// the bridge's pid: the newest write among the tool's files in the session's
// folder, which a command writes as it reads a page. The tool's unnamed
// session keeps the same files beside the sessions folder.
func (cfg Config) browserBridgeUse() map[int]time.Time {
	used := map[int]time.Time{}
	if cfg.BrowserSessions == "" {
		return used
	}
	folders := []string{filepath.Dir(cfg.BrowserSessions)}
	if entries, err := os.ReadDir(cfg.BrowserSessions); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				folders = append(folders, filepath.Join(cfg.BrowserSessions, entry.Name()))
			}
		}
	}
	for _, folder := range folders {
		data, err := os.ReadFile(filepath.Join(folder, "bridge.pid"))
		var bridge struct {
			PID int `json:"pid"`
		}
		if err != nil || json.Unmarshal(data, &bridge) != nil || bridge.PID <= 0 {
			continue
		}
		var newest time.Time
		for _, name := range browserSessionFiles {
			if info, err := os.Stat(filepath.Join(folder, name)); err == nil && info.ModTime().After(newest) {
				newest = info.ModTime()
			}
		}
		if newest.After(used[bridge.PID]) {
			used[bridge.PID] = newest
		}
	}
	return used
}

// isStaleBrowserSession reports whether the session folder at path holds
// only the tool's own files and names no bridge that still runs. A pid file
// that cannot be read names nothing that can be checked, so its folder
// stays.
func (cfg Config) isStaleBrowserSession(path string) bool {
	files, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, file := range files {
		if file.IsDir() || !slices.Contains(browserSessionFiles, file.Name()) {
			return false
		}
	}
	data, err := os.ReadFile(filepath.Join(path, "bridge.pid"))
	if os.IsNotExist(err) {
		return true
	}
	var bridge struct {
		PID int `json:"pid"`
	}
	if err != nil || json.Unmarshal(data, &bridge) != nil || bridge.PID <= 0 {
		return false
	}
	return !slices.ContainsFunc(cfg.Inventory.Processes, func(process reap.Process) bool {
		return process.PID == bridge.PID && strings.Contains(strings.ToLower(process.CommandLine), "chrome-devtools-axi-bridge")
	})
}
