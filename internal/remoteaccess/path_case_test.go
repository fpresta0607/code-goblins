package remoteaccess

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The Scrawl server that names this PC's Scrawl state folder in another case
// is this PC's exactly where the file system opens the folder by that
// spelling, and another server elsewhere.
func TestReadScrawlNamesTheStateFolderByTheFileSystemsCase(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	state := filepath.Join(parent, "scrawl")
	if err := os.Mkdir(state, 0o755); err != nil {
		t.Fatal(err)
	}
	named := filepath.Join(parent, "SCRAWL")
	_, err := os.Stat(named)
	isOnePath := err == nil
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(scrawlHealth{App: "lavish-axi", StateID: "installation", StateDir: named})
	}))
	t.Cleanup(server.Close)
	c := Config{ScrawlTarget: server.URL, ScrawlState: state}

	// Act
	_, err = readScrawl(context.Background(), c)

	// Assert
	if isOwn := err == nil; isOwn != isOnePath {
		t.Fatalf("readScrawl with state %s named %s = %v, want this PC's: %t", state, named, err, isOnePath)
	}
}
