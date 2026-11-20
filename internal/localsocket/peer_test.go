//go:build !windows

package localsocket

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortSocketPath is a socket's path in a short folder under /tmp: a test's
// temporary folder can overflow sun_path.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	folder, err := os.MkdirTemp("/tmp", "peer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(folder) })
	return filepath.Join(folder, "peer.sock")
}

// peerChildVariable has the test binary, started by peerChild, run
// TestSocketPeerChild as the process at the other end of a socket: its
// value is the child's role, "dial" or "listen", a colon and the socket's
// path.
const peerChildVariable = "GOBLINS_TEST_SOCKET_PEER"

// TestSocketPeerChild is no test of its own: it returns at once unless
// peerChild started this binary, which then dials or listens on the socket it
// names, says "ready" and holds the socket until its standard input closes,
// or for a minute at most.
func TestSocketPeerChild(t *testing.T) {
	role, path, isChild := strings.Cut(os.Getenv(peerChildVariable), ":")
	if !isChild {
		return
	}
	switch role {
	case "dial":
		conn, err := net.Dial("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
	case "listen":
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		go func() {
			conn, err := listener.Accept()
			if err == nil {
				defer conn.Close()
				_, _ = io.Copy(io.Discard, conn)
			}
		}()
	default:
		t.Fatalf("unknown role %q", role)
	}
	fmt.Println("ready")
	closed := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Minute):
	}
}

// peerChild starts this test binary as a process of its own that takes role
// at the socket path, and returns its process ID once it is ready. The child
// ends with the test.
func peerChild(t *testing.T, role, path string) int {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^TestSocketPeerChild$")
	child.Env = append(os.Environ(), peerChildVariable+"="+role+":"+path)
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	var output []string
	for lines := bufio.NewScanner(stdout); lines.Scan(); {
		if lines.Text() == "ready" {
			return child.Process.Pid
		}
		output = append(output, lines.Text())
	}
	t.Fatalf("the %s child was never ready: %q", role, output)
	return 0
}

// Each end of the socket reads the process at the other, here a process of
// its own, not the end's own process.
func TestEachEndOfASocketReadsTheProcessAtTheOther(t *testing.T) {
	t.Run("the server reads its client", func(t *testing.T) {
		// Arrange
		path := shortSocketPath(t)
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		accepted := make(chan *net.UnixConn, 1)
		go func() {
			conn, _ := listener.AcceptUnix()
			accepted <- conn
		}()
		client := peerChild(t, "dial", path)
		var server *net.UnixConn
		select {
		case server = <-accepted:
		case <-time.After(time.Minute):
		}
		if server == nil {
			t.Fatal("the listener accepted no connection")
		}
		defer server.Close()

		// Act
		pid, err := PeerProcess(server)

		// Assert
		if err != nil || pid != client {
			t.Fatalf("the server reads its client as %d, %v, want the client's %d, not its own %d", pid, err, client, os.Getpid())
		}
	})
	t.Run("the client reads its server", func(t *testing.T) {
		// Arrange
		path := shortSocketPath(t)
		server := peerChild(t, "listen", path)
		client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()

		// Act
		pid, err := PeerProcess(client)

		// Assert
		if err != nil || pid != server {
			t.Fatalf("the client reads its server as %d, %v, want the server's %d, not its own %d", pid, err, server, os.Getpid())
		}
	})
}
