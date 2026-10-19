package readcache

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// maxEntries bounds how many entries a walk of a folder reads, refs or a
// worktree, before it gives up and runs the read.
const maxEntries = 100_000

// read is a request the cache answers: its key, the exit codes that are its
// answer rather than a failure, whether it starts no other program, and how
// to sum the state of the files its answer depends on, given what it last
// returned, if anything.
type read struct {
	key      string
	answers  []int
	isSilent bool
	stamp    func(s *stamper, config []string, previous *execx.Result)
}

func planOf(req execx.Request) (read, bool) {
	switch req.Name {
	case "git":
		return gitRead(req)
	case "sqlite3":
		return databaseRead(req)
	case "gh":
		return accountRead(req)
	}
	return read{}, false
}

// accountRead plans gh's answer of the account it works as, which changes
// only when gh signs in, signs out or switches accounts, and each of those
// rewrites its hosts file. Asked with the board's own environment only, where
// a token gh is given stays the same.
func accountRead(req execx.Request) (read, bool) {
	if !slices.Equal(req.Args, []string{"api", "user", "--jq", ".login"}) || req.Env != nil {
		return read{}, false
	}
	hosts := filepath.Join(ghConfigFolder(), "hosts.yml")
	return read{
		key:     "gh\x00" + strings.Join(req.Args, "\x00"),
		answers: []int{0},
		stamp:   func(s *stamper, _ []string, _ *execx.Result) { s.content(hosts) },
	}, true
}

// ghConfigFolder is where gh keeps its configuration and its hosts file.
func ghConfigFolder() string {
	if folder := os.Getenv("GH_CONFIG_DIR"); folder != "" {
		return folder
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "gh")
	}
	if appData := os.Getenv("AppData"); runtime.GOOS == "windows" && appData != "" {
		return filepath.Join(appData, "GitHub CLI")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gh")
}

// databaseRead plans a read-only query of a SQLite database: its answer
// depends on the database file and, in write-ahead mode, the log's file and
// the header of its index, which every commit changes. sqlite3 starts no
// other program.
func databaseRead(req execx.Request) (read, bool) {
	args := req.Args
	if len(args) != 4 || args[0] != "-readonly" || args[1] != "-json" || !filepath.IsAbs(args[2]) || req.Env != nil {
		return read{}, false
	}
	database := args[2]
	return read{
		key:      "sqlite3\x00" + strings.Join(args, "\x00"),
		answers:  []int{0},
		isSilent: true,
		stamp: func(s *stamper, _ []string, _ *execx.Result) {
			s.file(database)
			s.prefix(database, 100)
			s.file(database + "-wal")
			// The index's header, both copies and the checkpoint's record.
			s.prefix(database+"-shm", 136)
		},
	}, true
}
