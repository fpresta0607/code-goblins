package conpty

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestConsoleLatencyChild(t *testing.T) {
	arguments := flag.Args()
	if len(arguments) != 2 || arguments[0] != "latency-child" {
		return
	}
	progress, err := os.Create(os.Getenv("CONPTY_LATENCY_PROGRESS"))
	if err != nil {
		t.Fatal(err)
	}
	defer progress.Close()
	input := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(input, &mode); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetConsoleMode(input, mode & ^uint32(windows.ENABLE_LINE_INPUT|windows.ENABLE_ECHO_INPUT|windows.ENABLE_PROCESSED_INPUT) | windows.ENABLE_VIRTUAL_TERMINAL_INPUT); err != nil {
		t.Fatal(err)
	}
	if arguments[1] == "busy" {
		go func() {
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			for range tick.C {
				fmt.Println(strings.Repeat("load", 200))
			}
		}()
	}
	fmt.Println("latency-ready")
	wrapMarker := func() {
		if arguments[1] != "wrapped" {
			return
		}
		output := windows.Handle(os.Stdout.Fd())
		var info windows.ConsoleScreenBufferInfo
		if err := windows.GetConsoleScreenBufferInfo(output, &info); err != nil {
			t.Fatal(err)
		}
		position := uint32(116) | uint32(uint16(info.CursorPosition.Y))<<16
		if ok, _, err := kernel32.NewProc("SetConsoleCursorPosition").Call(uintptr(output), uintptr(position)); ok == 0 {
			t.Fatal(err)
		}
	}
	readEvents := windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputW")
	var burst []byte
	isBurst := false
	sequence := 0
	for {
		// libuv's Windows raw terminal reader requests one native event at a
		// time. A byte-stream-only fixture misses its console round trips.
		var event struct {
			Kind, Padding                uint16
			Down                         int32
			Repeat, Key, Scan, Character uint16
			Control                      uint32
		}
		var received uint32
		ok, _, err := readEvents.Call(uintptr(input), uintptr(unsafe.Pointer(&event)), 1, uintptr(unsafe.Pointer(&received)))
		if ok == 0 {
			t.Fatal(err)
		}
		if received > 0 && !isBurst {
			if _, err := fmt.Fprintf(progress, "input kind=%d down=%d repeat=%d character=%04x at=%s\n", event.Kind, event.Down, event.Repeat, event.Character, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		}
		if received == 0 || event.Kind != 1 || event.Down == 0 || event.Character == 0 {
			continue
		}
		for range event.Repeat {
			character := byte(event.Character)
			if isBurst {
				burst = append(burst, character)
				if len(burst) == 2000 {
					wrapMarker()
					written, err := fmt.Printf("\rburst-%x\n", sha256.Sum256(burst))
					if _, progressErr := fmt.Fprintf(progress, "burst output bytes=%d error=%v at=%s\n", written, err, time.Now().UTC().Format(time.RFC3339Nano)); progressErr != nil {
						t.Fatal(progressErr)
					}
					if err != nil {
						t.Fatal(err)
					}
					isBurst = false
				}
			} else if character == '!' {
				isBurst = true
			} else {
				sequence++
				wrapMarker()
				written, err := fmt.Printf("\rkey-%04d\n", sequence)
				if _, progressErr := fmt.Fprintf(progress, "key-%04d output bytes=%d error=%v at=%s\n", sequence, written, err, time.Now().UTC().Format(time.RFC3339Nano)); progressErr != nil {
					t.Fatal(progressErr)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestConsoleInputLatencyWhileIdleAndPrinting(t *testing.T) {
	for _, activity := range []string{"idle", "busy", "wrapped"} {
		t.Run(activity, func(t *testing.T) {
			progressPath := filepath.Join(t.TempDir(), "native-input.log")
			console, err := Start(Spec{
				Args: []string{os.Args[0], "-test.run=^TestConsoleLatencyChild$", "--", "latency-child", activity},
				Env:  append(os.Environ(), "CONPTY_LATENCY_PROGRESS="+progressPath),
				Cols: 120, Rows: 40,
			})
			if err != nil {
				t.Fatal(err)
			}
			output := make(chan []byte, 256)
			stopping, ended := make(chan struct{}), make(chan struct{})
			t.Cleanup(func() {
				close(stopping)
				_ = console.Close()
				<-ended
			})
			go func() {
				defer close(ended)
				defer close(output)
				for {
					buffer := make([]byte, 32<<10)
					count, err := console.Read(buffer)
					if count > 0 {
						select {
						case output <- buffer[:count]:
						case <-stopping:
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
			waitForMarker := func(marker string, limit time.Duration) time.Duration {
				t.Helper()
				started := time.Now()
				timer := time.NewTimer(limit)
				defer timer.Stop()
				var pending string
				for {
					select {
					case chunk, isOpen := <-output:
						if !isOpen {
							t.Fatalf("terminal ended before %q", marker)
						}
						pending += string(chunk)
						if strings.Contains(pending, marker) {
							return time.Since(started)
						}
						if len(pending) > 4096 {
							pending = pending[len(pending)-4096:]
						}
					case <-timer.C:
						progress, err := os.ReadFile(progressPath)
						if err != nil {
							t.Logf("native input progress unavailable: %v", err)
						} else {
							t.Logf("native input progress:\n%s", progress)
						}
						t.Fatalf("no %q within %s; last output %q", marker, limit, pending)
					}
				}
			}
			waitForMarker("latency-ready", 15*time.Second)
			var samples []time.Duration
			for sequence := 1; sequence <= 40; sequence++ {
				started := time.Now()
				if _, err := console.Write([]byte("a")); err != nil {
					t.Fatal(err)
				}
				waitForMarker(fmt.Sprintf("key-%04d", sequence), 5*time.Second)
				samples = append(samples, time.Since(started))
			}
			slices.Sort(samples)
			t.Logf("key p95=%s max=%s", samples[37], samples[39])
			if samples[37] >= 50*time.Millisecond || samples[39] > 250*time.Millisecond {
				t.Errorf("key latency exceeds p95 50ms/max 250ms: %v", samples)
			}
			payload := strings.Repeat("0123456789abcdefABCD", 100)
			started := time.Now()
			if _, err := console.Write([]byte("!" + payload)); err != nil {
				t.Fatal(err)
			}
			waitForMarker(fmt.Sprintf("burst-%x", sha256.Sum256([]byte(payload))), 5*time.Second)
			elapsed := time.Since(started)
			t.Logf("ordered 2000-character burst=%s", elapsed)
			if elapsed > 2*time.Second {
				t.Errorf("ordered burst took %s, want at most 2s", elapsed)
			}
		})
	}
}
