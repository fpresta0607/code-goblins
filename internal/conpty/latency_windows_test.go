package conpty

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
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
	// A goroutine of its own writes the progress file, so no key's echo waits
	// on the disk. The ninth line, the fifth key's, is the first that no
	// longer fits inside the file's own record, so NTFS has to find the file
	// room on the disk. Written before the echo, that line took a millisecond
	// where the others took none, and on a hosted runner it once took the
	// whole second that failed the run.
	notesPath := os.Getenv("PROBE_NATURAL_NOTES")
	var notes probeChildNotes
	var slowWrites struct {
		sync.Mutex
		spans []probeSpan
	}
	write := func(receipt string) {
		began := probeNow()
		if _, err := progress.WriteString(receipt); err != nil {
			panic(err)
		}
		if ended := probeNow(); ended-began >= int64(5*time.Millisecond) {
			slowWrites.Lock()
			slowWrites.spans = append(slowWrites.spans, probeSpan{began, ended})
			slowWrites.Unlock()
		}
	}
	receipts := make(chan string, 1024)
	go func() {
		for receipt := range receipts {
			write(receipt)
		}
	}()
	record := func(format string, values ...any) {
		receipts <- fmt.Sprintf(format, values...)
	}
	if os.Getenv("PROBE_AS_MAIN") != "" {
		record = func(format string, values ...any) {
			write(fmt.Sprintf(format, values...))
		}
	}
	var clock *probeClock
	if notesPath != "" {
		clock = startProbeClock()
	}
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
		asked := probeNow()
		ok, _, err := readEvents.Call(uintptr(input), uintptr(unsafe.Pointer(&event)), 1, uintptr(unsafe.Pointer(&received)))
		if ok == 0 {
			t.Fatal(err)
		}
		read := probeNow()
		if received > 0 && !isBurst {
			record("input kind=%d down=%d repeat=%d character=%04x at=%s\n", event.Kind, event.Down, event.Repeat, event.Character, time.Now().UTC().Format(time.RFC3339Nano))
		}
		filed := probeNow()
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
					record("burst output bytes=%d error=%v at=%s\n", written, err, time.Now().UTC().Format(time.RFC3339Nano))
					if err != nil {
						t.Fatal(err)
					}
					isBurst = false
				}
			} else if character == '!' {
				isBurst = true
			} else if character == '?' && notesPath != "" {
				notes.Stalls, notes.Beats = clock.read()
				slowWrites.Lock()
				notes.SlowWrites = append([]probeSpan(nil), slowWrites.spans...)
				slowWrites.Unlock()
				encoded, err := json.Marshal(notes)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(notesPath, encoded, 0o600); err != nil {
					t.Fatal(err)
				}
				fmt.Printf("\rnotes-done\n")
			} else {
				sequence++
				wrapMarker()
				written, err := fmt.Printf("\rkey-%04d\n", sequence)
				printed := probeNow()
				record("key-%04d output bytes=%d error=%v at=%s\n", sequence, written, err, time.Now().UTC().Format(time.RFC3339Nano))
				if err != nil {
					t.Fatal(err)
				}
				notes.Keys = append(notes.Keys, probeKey{asked, read, filed, printed, probeNow()})
			}
		}
	}
}

// keyLatency judges the time each of a run's 40 keys took to come back, in
// the order they were typed. The 95th percentile must stay under 50 ms and
// the second slowest key within 250 ms. On a hosted runner one key in 40 can
// wait a few hundred milliseconds for the machine, 504 ms with the job to
// itself, which is noise; two slow keys are a pattern, and a key past a
// second is a stall, never noise. A failure names the slow keys by their
// number in the run, so a stall that always hits the same key shows.
func keyLatency(samples []time.Duration) (summary string, err error) {
	if len(samples) != 40 {
		return "", fmt.Errorf("measured %d keys, want 40", len(samples))
	}
	sorted := slices.Sorted(slices.Values(samples))
	p95, second, slowest := sorted[37], sorted[38], sorted[39]
	summary = fmt.Sprintf("key p95=%s second slowest=%s slowest=%s", p95, second, slowest)
	if p95 < 50*time.Millisecond && second <= 250*time.Millisecond && slowest <= time.Second {
		return summary, nil
	}
	var slow []string
	for index, sample := range samples {
		if sample > 250*time.Millisecond {
			slow = append(slow, fmt.Sprintf("key %d took %s", index+1, sample))
		}
	}
	return summary, fmt.Errorf("%s; want p95 under 50ms, the second slowest within 250ms and none past 1s; slow keys %v; all %v", summary, slow, sorted)
}

// One slow key in a run is a hosted runner's noise. Two slow keys, a slow
// 95th percentile or one key past a second each fail the run.
func TestKeyLatencyToleratesOneSlowKeyButNoPattern(t *testing.T) {
	// typed is a run of 40 keys at 10 ms, the first of them, from the sixth
	// key on, replaced by slow.
	typed := func(slow ...time.Duration) []time.Duration {
		samples := make([]time.Duration, 40)
		for index := range samples {
			samples[index] = 10 * time.Millisecond
		}
		copy(samples[5:], slow)
		return samples
	}
	for name, test := range map[string]struct {
		samples    []time.Duration
		shouldPass bool
		names      []string
	}{
		"every key fast":                         {samples: typed(), shouldPass: true},
		"one key waits 504 ms for the runner":    {samples: typed(504 * time.Millisecond), shouldPass: true},
		"the second slowest is exactly 250 ms":   {samples: typed(time.Second, 250*time.Millisecond), shouldPass: true},
		"the 95th percentile is just under 50ms": {samples: typed(49*time.Millisecond, 49*time.Millisecond, 49*time.Millisecond), shouldPass: true},
		"two keys past 250 ms":                   {samples: typed(300*time.Millisecond, 260*time.Millisecond), names: []string{"key 6 took 300ms", "key 7 took 260ms"}},
		"one key past a second":                  {samples: typed(1100 * time.Millisecond), names: []string{"key 6 took 1.1s"}},
		"the 95th percentile reaches 50 ms":      {samples: typed(50*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond)},
		"fewer keys than a run types":            {samples: make([]time.Duration, 39)},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			_, err := keyLatency(test.samples)

			// Assert
			if (err == nil) != test.shouldPass {
				t.Fatalf("keyLatency = %v, want a pass: %t", err, test.shouldPass)
			}
			for _, named := range test.names {
				if !strings.Contains(err.Error(), named) {
					t.Errorf("the failure %q does not say %q", err, named)
				}
			}
		})
	}
}

func TestConsoleInputLatencyWhileIdleAndPrinting(t *testing.T) {
	for _, activity := range []string{"idle", "busy", "wrapped"} {
		t.Run(activity, func(t *testing.T) {
			progressPath := filepath.Join(t.TempDir(), "native-input.log")
			console, err := Start(Spec{
				Args: []string{os.Args[0], "-test.run=^TestConsoleLatencyChild$", "--", "latency-child", activity},
				Env:  append(os.Environ(), "CONPTY_LATENCY_PROGRESS="+progressPath, "PROBE_NATURAL_NOTES="+progressPath+".notes"),
				Cols: 120, Rows: 40,
			})
			if err != nil {
				t.Fatal(err)
			}
			isAsMain := os.Getenv("PROBE_AS_MAIN") != ""
			testClock := probeTestClock()
			slowFrom, err := time.ParseDuration(os.Getenv("PROBE_NATURAL_FROM"))
			if err != nil {
				slowFrom = 20 * time.Millisecond
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
			type typedKey struct{ w0, w1, seen, idle int64 }
			var typed []typedKey
			processesBefore := probeProcessTimes()
			var processesAfter map[uintptr]probeProcess
			for sequence := 1; sequence <= 40; sequence++ {
				idle := probeIdle()
				started := time.Now()
				w0 := probeNow()
				if _, err := console.Write([]byte("a")); err != nil {
					t.Fatal(err)
				}
				w1 := probeNow()
				waitForMarker(fmt.Sprintf("key-%04d", sequence), 5*time.Second)
				samples = append(samples, time.Since(started))
				typed = append(typed, typedKey{w0, w1, probeNow(), probeIdle() - idle})
				if processesAfter == nil && samples[sequence-1] >= slowFrom {
					processesAfter = probeProcessTimes()
				}
			}
			summary, err := keyLatency(samples)
			t.Logf("natural: asMain=%t %s: %s", isAsMain, activity, summary)
			defer func() {
				if _, err := console.Write([]byte("?")); err != nil {
					t.Logf("natural: notes: %v", err)
					return
				}
				waitForMarker("notes-done", 5*time.Second)
				encoded, err := os.ReadFile(progressPath + ".notes")
				var notes probeChildNotes
				if err == nil {
					err = json.Unmarshal(encoded, &notes)
				}
				if err != nil || len(notes.Keys) != len(typed) {
					t.Logf("natural: notes: %v, %d keys for %d typed", err, len(notes.Keys), len(typed))
					return
				}
				for _, write := range notes.SlowWrites {
					key := 0
					for index, typedKey := range typed {
						if typedKey.w0 <= write.From {
							key = index + 1
						}
					}
					t.Logf("natural slow write: asMain=%t %s: a progress file write took %s, begun after key %d was typed", isAsMain, activity, probeMs(write.To-write.From), key)
				}
				testStalls, _ := testClock.read()
				for index, key := range typed {
					if key.seen-key.w0 < int64(slowFrom) {
						continue
					}
					child := notes.Keys[index]
					t.Logf("natural slow key: asMain=%t %s key %d took %s at %s: typing %s, way in %s (the child had asked %s before it was typed), its receipt %s, its print %s, way out and handing on from before the print %s, second receipt %s; the %d processors stood idle %s in all; test clock stalls %s; child clock stalls %s; busiest since the console was ready: %s",
						isAsMain, activity, index+1, probeMs(key.seen-key.w0), time.Unix(0, key.w0).UTC().Format("15:04:05.000"), probeMs(key.w1-key.w0), probeMs(child.Read-key.w1), probeMs(key.w0-child.Asked), probeMs(child.Filed-child.Read), probeMs(child.Printed-child.Filed), probeMs(key.seen-child.Filed), probeMs(child.Filed2-child.Printed),
						runtime.NumCPU(), probeMs(key.idle), probeOverlaps(testStalls, key.w0, key.seen), probeOverlaps(notes.Stalls, key.w0, key.seen), probeBusiest(processesBefore, processesAfter))
				}
			}()
			if err != nil {
				t.Error(err)
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
