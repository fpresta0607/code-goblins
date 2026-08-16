package showcase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// OpenResult describes a session that is ready in the browser.
type OpenResult struct {
	ID  string
	URL string
}

// OpenSession ensures the background server is up, registers the artifact,
// and opens the review page in the default browser.
func OpenSession(ctx context.Context, artifact string, reopen bool, out io.Writer) (OpenResult, error) {
	abs, err := filepath.Abs(artifact)
	if err != nil {
		return OpenResult{}, err
	}
	if _, err := os.Stat(abs); err != nil {
		return OpenResult{}, fmt.Errorf("showcase: %w", err)
	}
	port, err := EnsureServer(ctx)
	if err != nil {
		return OpenResult{}, err
	}
	body, err := json.Marshal(registerRequest{Path: abs, Reopen: reopen})
	if err != nil {
		return OpenResult{}, err
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	response, err := http.Post(endpoint+"/api/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return OpenResult{}, fmt.Errorf("showcase: register: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		return OpenResult{}, ErrEndedByUser
	}
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(response.Body)
		return OpenResult{}, fmt.Errorf("showcase: register: %s", bytes.TrimSpace(detail))
	}
	var registered registerResponse
	if err := json.NewDecoder(response.Body).Decode(&registered); err != nil {
		return OpenResult{}, err
	}
	url := endpoint + registered.URL
	if err := openBrowser(url); err != nil {
		fmt.Fprintf(out, "open this URL in your browser: %s\n", url)
	}
	fmt.Fprintf(out, "showcasing %s (%s) at %s\n", filepath.Base(abs), registered.Kind, url)
	fmt.Fprintf(out, "collect feedback with: showcase-axi poll %s\n", artifact)
	return OpenResult{ID: registered.ID, URL: url}, nil
}

// EnsureServer returns the port of the running server, starting a detached
// one when server.json is missing or stale.
func EnsureServer(ctx context.Context) (int, error) {
	dir, err := RuntimeDir()
	if err != nil {
		return 0, err
	}
	if port, ok := healthyServer(dir); ok {
		return port, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(exe, "serve")
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("showcase: start server: %w", err)
	}
	cmd.Process.Release()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if port, ok := healthyServer(dir); ok {
			return port, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 0, fmt.Errorf("showcase: server did not come up within 10s")
}

func healthyServer(dir string) (int, bool) {
	info, err := ReadRuntime(dir)
	if err != nil {
		return 0, false
	}
	client := &http.Client{Timeout: 800 * time.Millisecond}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", info.Port))
	if err != nil {
		return 0, false
	}
	response.Body.Close()
	return info.Port, response.StatusCode == http.StatusOK
}

// StopServer asks the background server to shut down.
func StopServer(out io.Writer) error {
	dir, err := RuntimeDir()
	if err != nil {
		return err
	}
	info, err := ReadRuntime(dir)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(out, "no showcase-axi server is running")
		return nil
	}
	if err != nil {
		return err
	}
	response, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/api/stop", info.Port), "application/json", nil)
	if err != nil {
		os.Remove(runtimePath(dir))
		fmt.Fprintln(out, "server was not answering; cleared its stale record")
		return nil
	}
	response.Body.Close()
	fmt.Fprintln(out, "showcase-axi server stopped")
	return nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
