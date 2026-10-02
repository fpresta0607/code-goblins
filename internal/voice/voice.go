// Package voice is the board's dictation engine on this machine: a pinned
// speech model and the program that runs it, fetched once and checked against
// their checksums, then run on a sound file with no network.
package voice

import (
	"context"
	"net/http"
)

// Room is the free memory and the free commit a dictation needs.
const Room = 1 << 30

// Part is one pinned download, the engine or the model: an archive, its
// SHA-256 and the files of it that are kept.
type Part struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	URL     string   `json:"url"`
	SHA256  string   `json:"sha256"`
	Files   []string `json:"files"`
}

// Settings is the one setting dictation has: which engine, which model, and
// how the engine is started on the model.
type Settings struct {
	Engine  Part     `json:"engine"`
	Model   Part     `json:"model"`
	Program string   `json:"program"`
	Args    []string `json:"args"`
	Threads int      `json:"threads"`
}

// Voice runs Settings from Dir.
type Voice struct {
	Settings Settings
	Dir      string
	Client   *http.Client
	Progress func(part string, done, total int64)
	Memory   func() (available, commit uint64, err error)
}

func Load(path string) (Settings, error) { return Settings{}, nil }

func (v *Voice) Fetch(ctx context.Context) error { return nil }

func (v *Voice) fetch(ctx context.Context, part Part) error { return nil }

func (v *Voice) ready(part Part) error { return nil }

func (v *Voice) Recognize(ctx context.Context, sound []byte) (string, error) { return "", nil }

func offline(dir string) error { return nil }

func imports(path string) ([]string, error) { return nil, nil }
