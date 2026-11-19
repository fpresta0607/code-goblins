package proc

import (
	"os"
	"path/filepath"
	"testing"
)

// node runs the Scrawl server when its script is lavish-axi's, named in
// another case exactly where the file system opens the script's folder by
// that spelling.
func TestServiceOfNamesTheScrawlServersScriptByTheFileSystemsCase(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, "lavish-axi", "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(parent, "LAVISH-AXI"))
	isOnePath := err == nil
	script := filepath.Join(parent, "LAVISH-AXI", "dist", "server.mjs")

	// Act
	service := ServiceOf("node", []string{"node", script, "server"})

	// Assert
	if isServer := service == PageServer; isServer != isOnePath {
		t.Fatalf("ServiceOf(node %s server) = %d, want the Scrawl server: %t", script, service, isOnePath)
	}
}

// A backslash in the script's name separates its folders only where it is
// the system's separator: elsewhere it is a character of one name, and that
// file is no lavish-axi script.
func TestServiceOfReadsTheScriptsFoldersBySeparator(t *testing.T) {
	// Arrange
	script := filepath.Join(t.TempDir(), `lavish-axi\dist\server.mjs`)

	// Act
	service := ServiceOf("node", []string{"node", script, "server"})

	// Assert
	if isServer, want := service == PageServer, os.IsPathSeparator('\\'); isServer != want {
		t.Fatalf("ServiceOf(node %s server) = %d, want the Scrawl server: %t", script, service, want)
	}
}
