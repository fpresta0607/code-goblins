package browserlogin

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cdp"
)

// TestChromeStartCookieAndExitTimes records how long a fresh-profile headless
// Chrome takes to start, to answer its first Storage.setCookies and to exit
// once asked to close, so the CI job that runs the real-Chrome tests alone
// shows Chrome's own tail where nothing competes with it. It runs only when
// CFO_CHROME_TIMINGS names how many Chromes to time, adds each one's times to
// the file CFO_CHROME_TIMINGS_FILE names, and fails only on an error, never
// on a time.
func TestChromeStartCookieAndExitTimes(t *testing.T) {
	samples, _ := strconv.Atoi(os.Getenv("CFO_CHROME_TIMINGS"))
	if samples <= 0 {
		t.Skip("CFO_CHROME_TIMINGS names no number of Chromes to time")
	}
	record, err := os.OpenFile(os.Getenv("CFO_CHROME_TIMINGS_FILE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer record.Close()
	executable, err := Executable()
	if err != nil {
		t.Fatal(err)
	}
	for index := range samples {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		began := time.Now()
		process, err := Start(ctx, Launch{Executable: executable, Profile: t.TempDir(), Hosts: []string{"localhost"}, Headless: true})
		started := time.Since(began)
		if err != nil {
			cancel()
			t.Fatalf("chrome %d did not start after %s: %v", index, started, err)
		}
		socket, err := browserSocket(ctx, process.URL)
		if err != nil {
			t.Fatal(err)
		}
		connection, err := cdp.Dial(ctx, socket)
		if err != nil {
			t.Fatal(err)
		}
		asked := time.Now()
		err = connection.Call(ctx, "Storage.setCookies", map[string]any{"cookies": []map[string]string{{"name": "timing", "value": "1", "domain": "localhost", "path": "/"}}}, nil)
		cookie := time.Since(asked)
		connection.Close()
		if err != nil {
			t.Fatal(err)
		}
		closing := time.Now()
		if err := process.job.transport.pipe.call(ctx, "Browser.close", nil, nil); err != nil {
			t.Logf("chrome %d did not take the close: %v", index, err)
		}
		for Running(process) && ctx.Err() == nil {
			time.Sleep(100 * time.Millisecond)
		}
		exited := time.Since(closing)
		if err := End(context.Background(), process); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf("chrome %d: start %s, first setCookies %s, exit after close %s, gone %s\n", index, started.Round(time.Millisecond), cookie.Round(time.Millisecond), exited.Round(time.Millisecond), time.Since(closing).Round(time.Millisecond))
		if _, err := record.WriteString(line); err != nil {
			t.Fatal(err)
		}
		t.Log(line)
		cancel()
	}
}
