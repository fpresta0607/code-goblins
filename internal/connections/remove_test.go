package connections

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/state"
)

var removedAt = time.Date(2026, 11, 27, 3, 0, 0, 0, time.UTC)

// Remove takes the one service out of the project's auth.json and keeps it,
// word for word, in auth.removed.json beside it: the other services stay as
// they were written, and the credentials stored for it stay in the vault.
func TestRemoveServiceTakesOnlyThatServiceOutAndKeepsItBeside(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	e.manifest(t, "acme", `[
		{"name":"hosted","method":"oauth","url":"https://login.acme.example/start","note":"kept as written"},
		{"name":"keyed","method":"env","env":["ACME_KEY"],"optional":true,"note":"the one removed"},
		{"name":"tool","method":"cli","login":["acme-cli","login"]}
	]`)
	e.store(t, "acme", "ACME_KEY", "stand-in-value")
	writeFile(t, filepath.Join(filepath.Dir(auth.ManifestPath(e.DataDir, "acme")), RemovedFileName), `[{"removed_at":"2026-11-01T00:00:00Z","service":{"name":"older","method":"env"}}]`)

	// Act
	err := e.RemoveService("acme", "keyed", removedAt)

	// Assert
	if err != nil {
		t.Fatalf("RemoveService = %v, want nil", err)
	}
	manifest, err := auth.LoadManifest(e.DataDir, "acme")
	if err != nil {
		t.Fatalf("the manifest no longer loads: %v", err)
	}
	if got := serviceNames(manifest); got != "hosted,tool" || manifest.Services[0].Note != "kept as written" {
		t.Fatalf("services = %s %+v, want hosted and tool as written", got, manifest.Services)
	}
	var kept []struct {
		RemovedAt time.Time       `json:"removed_at"`
		Service   json.RawMessage `json:"service"`
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(manifest.Path), RemovedFileName))
	if err != nil || json.Unmarshal(data, &kept) != nil || len(kept) != 2 {
		t.Fatalf("removed file = %s (%v), want the older entry and the new one", data, err)
	}
	var service auth.Service
	if err := json.Unmarshal(kept[1].Service, &service); err != nil || service.Name != "keyed" || service.Note != "the one removed" || !service.Optional || !kept[1].RemovedAt.Equal(removedAt) {
		t.Fatalf("kept = %s at %v (%v), want keyed word for word", kept[1].Service, kept[1].RemovedAt, err)
	}
	if value, isStored, err := e.credentials.Get(auth.Scoped("acme", "ACME_KEY")); err != nil || !isStored || value != "stand-in-value" {
		t.Fatalf("the stored credential = %v %v, want it untouched", isStored, err)
	}
}

// A Remove that cannot be done says why and changes nothing: no manifest, a
// service the manifest no longer declares, a manifest that does not load, and a
// removed file that cannot be read, whose copy would otherwise be lost.
func TestRemoveServiceRefusesAndChangesNothing(t *testing.T) {
	cases := []struct {
		name, services, removed, service, want string
	}{
		{name: "no manifest", service: "keyed", want: "has no auth.json"},
		{name: "not declared", services: `[{"name":"keyed","method":"env","env":["ACME_KEY"]}]`, service: "other", want: "no longer declares other"},
		{name: "manifest does not load", services: `[{"name":"keyed","method":"unknown"}]`, service: "keyed", want: "could not be read"},
		{name: "removed file unreadable", services: `[{"name":"keyed","method":"env","env":["ACME_KEY"]}]`, removed: `not json`, service: "keyed", want: "could not be kept"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			e := newTestEnvironment(t)
			path := auth.ManifestPath(e.DataDir, "acme")
			if test.services != "" {
				e.manifest(t, "acme", test.services)
			}
			removedPath := filepath.Join(filepath.Dir(path), RemovedFileName)
			if test.removed != "" {
				writeFile(t, removedPath, test.removed)
			}
			before, _ := os.ReadFile(path)

			// Act
			err := e.RemoveService("acme", test.service, removedAt)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("RemoveService = %v, want %q", err, test.want)
			}
			if after, _ := os.ReadFile(path); string(after) != string(before) {
				t.Fatalf("manifest = %s, want it unchanged from %s", after, before)
			}
			if removed, _ := os.ReadFile(removedPath); string(removed) != test.removed {
				t.Fatalf("removed file = %q, want it unchanged", removed)
			}
		})
	}
}

func serviceNames(manifest auth.Manifest) string {
	names := make([]string, 0, len(manifest.Services))
	for _, service := range manifest.Services {
		names = append(names, service.Name)
	}
	return strings.Join(names, ",")
}

// fakeUserEnv is this PC's user environment in a map, which can fail a read or
// a delete.
type fakeUserEnv struct {
	values             map[string]string
	readErr, deleteErr error
	broadcasts         int
}

func (f *fakeUserEnv) Get(name string) (string, bool, error) {
	value, isSet := f.values[name]
	return value, isSet, f.readErr
}
func (f *fakeUserEnv) Set(name, value string) error { f.values[name] = value; return nil }
func (f *fakeUserEnv) Unset(name string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.values, name)
	return nil
}
func (f *fakeUserEnv) Names() ([]string, error) { return nil, nil }
func (f *fakeUserEnv) Broadcast() error         { f.broadcasts++; return nil }

// Remove of a credential of this PC keeps its value in the vault's shared
// scope, then deletes it from the user environment and tells Windows, and a
// vault that holds the same value already is enough.
func TestRemoveUserCredentialKeepsTheValueInTheVaultFirst(t *testing.T) {
	for _, isStoredAlready := range []bool{false, true} {
		t.Run(map[bool]string{false: "not yet in the vault", true: "same value in the vault"}[isStoredAlready], func(t *testing.T) {
			// Arrange
			e := newTestEnvironment(t)
			userEnv := &fakeUserEnv{values: map[string]string{"ACME_API_KEY": "stand-in-value", "PATH": "kept"}}
			e.UserEnv = userEnv
			if isStoredAlready {
				e.store(t, "", "ACME_API_KEY", "stand-in-value")
			}

			// Act
			err := e.RemoveUserCredential("ACME_API_KEY")

			// Assert
			if err != nil {
				t.Fatalf("RemoveUserCredential = %v, want nil", err)
			}
			if _, isSet := userEnv.values["ACME_API_KEY"]; isSet || userEnv.values["PATH"] != "kept" || userEnv.broadcasts != 1 {
				t.Fatalf("user environment = %v after %d broadcasts, want only ACME_API_KEY gone and Windows told", userEnv.values, userEnv.broadcasts)
			}
			if value, isStored, err := e.credentials.Get(auth.Shared("ACME_API_KEY")); err != nil || !isStored || value != "stand-in-value" {
				t.Fatalf("vault holds it = %v (%v), want the value kept", isStored, err)
			}
		})
	}
}

// A credential Remove cannot take away safely stays where it is, and the
// reason never holds its value: another value in the vault under its name, a
// name the page does not list, one already gone, an unreadable environment and
// a delete that fails.
func TestRemoveUserCredentialRefusesAndKeepsTheVariable(t *testing.T) {
	cases := []struct {
		name, variable, vault, want string
		readErr, deleteErr          error
	}{
		{name: "another value in the vault", variable: "ACME_API_KEY", vault: "other-stand-in", want: "already holds another value"},
		{name: "not a listed credential", variable: "PATH", want: "Only a credential this page lists"},
		{name: "already gone", variable: "GONE_API_KEY", want: "no longer in this PC's user environment"},
		{name: "environment unreadable", variable: "ACME_API_KEY", readErr: os.ErrPermission, want: "could not be read"},
		{name: "delete fails", variable: "ACME_API_KEY", deleteErr: os.ErrPermission, want: "could not be deleted"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			e := newTestEnvironment(t)
			userEnv := &fakeUserEnv{values: map[string]string{"ACME_API_KEY": "stand-in-value", "PATH": "kept"}, readErr: test.readErr, deleteErr: test.deleteErr}
			e.UserEnv = userEnv
			if test.vault != "" {
				e.store(t, "", "ACME_API_KEY", test.vault)
			}

			// Act
			err := e.RemoveUserCredential(test.variable)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "stand-in-value") {
				t.Fatalf("RemoveUserCredential = %v, want %q and no value", err, test.want)
			}
			if userEnv.values["ACME_API_KEY"] != "stand-in-value" || userEnv.values["PATH"] != "kept" {
				t.Fatalf("user environment = %v, want it unchanged", userEnv.values)
			}
			if test.vault != "" {
				if value, _, _ := e.credentials.Get(auth.Shared("ACME_API_KEY")); value != test.vault {
					t.Fatal("the vault's own value was replaced")
				}
			}
		})
	}
}

// A server a goblin's session named is the goblin's to give back only when its
// task record gives it by name: one every goblin of the project gets is the
// project's, one that needs a grant the record no longer gives is revoked, and
// one the project does not declare comes from elsewhere.
func TestGoblinServerSourceSaysWhereAServerComesFrom(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	checkout := e.checkout(t, "acme")
	writeFile(t, filepath.Join(checkout, ".mcp.json"), `{"mcpServers":{"docs":{"command":"npx"},"held":{"command":"npx","env":{"HELD_MODE":"literal"}}}}`)
	cases := []struct {
		name, server, want string
		granted            []string
	}{
		{name: "granted", server: "held", granted: []string{"held"}, want: ServerGranted},
		{name: "grant revoked", server: "held", want: ServerRevoked},
		{name: "project's", server: "docs", granted: []string{"held"}, want: ServerProject},
		{name: "elsewhere", server: "claude.ai Gmail", granted: []string{"held"}, want: ServerElsewhere},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Act
			source, err := GoblinServerSource(state.TaskMeta{ID: "task-1", Project: checkout, MCPServers: test.granted}, test.server)

			// Assert
			if err != nil || source != test.want {
				t.Fatalf("source = %q (%v), want %q", source, err, test.want)
			}
		})
	}
}
