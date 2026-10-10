package conpty

// PROBE, never merged: where a key's time goes on a hosted runner. It types
// keys as TestConsoleInputLatencyWhileIdleAndPrinting does, into a child that
// does what that test's child does, and times each leg of every key on one
// clock both processes read. Each process also keeps a clock thread of its
// own that notes every time it was not run for 20 ms or more.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// probeNow is the wall clock in nanoseconds, as precisely as Windows keeps
// it, the same in every process.
func probeNow() int64 {
	var now windows.Filetime
	windows.GetSystemTimePreciseAsFileTime(&now)
	return now.Nanoseconds()
}

type probeSpan struct{ From, To int64 }

func (s probeSpan) length() time.Duration { return time.Duration(s.To - s.From) }

// probeClock notes each time its own thread, which asks to sleep a
// millisecond at a time, was away for 20 ms or more.
type probeClock struct {
	mu     sync.Mutex
	stalls []probeSpan
	beats  int64
}

func startProbeClock() *probeClock {
	clock := &probeClock{}
	go func() {
		runtime.LockOSThread()
		last := probeNow()
		for {
			time.Sleep(time.Millisecond)
			now := probeNow()
			clock.mu.Lock()
			clock.beats++
			if now-last >= int64(20*time.Millisecond) {
				clock.stalls = append(clock.stalls, probeSpan{last, now})
			}
			clock.mu.Unlock()
			last = now
		}
	}()
	return clock
}

func (c *probeClock) read() ([]probeSpan, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]probeSpan(nil), c.stalls...), c.beats
}

// probeKey is one key as the child saw it: when it asked for the input event
// that turned out to be the key, when the read returned, when its receipt
// was in the progress file, when its print returned and when its second
// receipt was written.
type probeKey struct{ Asked, Read, Filed, Printed, Filed2 int64 }

type probeChildNotes struct {
	Keys       []probeKey
	Stalls     []probeSpan
	Beats      int64
	SlowWrites []probeSpan
}

func TestLatencyStagesChild(t *testing.T) {
	arguments := flag.Args()
	if len(arguments) != 2 || arguments[0] != "stages-child" {
		return
	}
	clock := startProbeClock()
	progress, err := os.Create(os.Getenv("PROBE_STAGES_PROGRESS"))
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
	var notes probeChildNotes
	file := func(format string, args ...any) {
		began := probeNow()
		if _, err := fmt.Fprintf(progress, format, args...); err != nil {
			t.Fatal(err)
		}
		if ended := probeNow(); ended-began >= int64(5*time.Millisecond) {
			notes.SlowWrites = append(notes.SlowWrites, probeSpan{began, ended})
		}
	}
	sequence := 0
	for {
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
		if received > 0 {
			file("input kind=%d down=%d repeat=%d character=%04x at=%s\n", event.Kind, event.Down, event.Repeat, event.Character, time.Now().UTC().Format(time.RFC3339Nano))
		}
		filed := probeNow()
		if received == 0 || event.Kind != 1 || event.Down == 0 || event.Character == 0 {
			continue
		}
		for range event.Repeat {
			if byte(event.Character) == '?' {
				notes.Stalls, notes.Beats = clock.read()
				encoded, err := json.Marshal(notes)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(os.Getenv("PROBE_STAGES_NOTES"), encoded, 0o600); err != nil {
					t.Fatal(err)
				}
				fmt.Printf("\rnotes-done\n")
				continue
			}
			sequence++
			wrapMarker()
			written, err := fmt.Printf("\rkey-%04d\n", sequence)
			printed := probeNow()
			file("key-%04d output bytes=%d error=%v at=%s\n", sequence, written, err, time.Now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				t.Fatal(err)
			}
			notes.Keys = append(notes.Keys, probeKey{asked, read, filed, printed, probeNow()})
		}
	}
}

// TestLatencyStagesSpin keeps every processor busy until it is ended, or for
// as long as it was told, whichever comes first.
func TestLatencyStagesSpin(t *testing.T) {
	arguments := flag.Args()
	if len(arguments) != 2 || arguments[0] != "stages-spin" {
		return
	}
	limit, err := time.ParseDuration(arguments[1])
	if err != nil {
		t.Fatal(err)
	}
	for range runtime.NumCPU() {
		go func() {
			for {
			}
		}()
	}
	time.Sleep(limit)
	os.Exit(0)
}

// probeRaise puts every process in the console's job, and the console server
// that was not there before, in the high priority class.
func probeRaise(console *Console, before, after []int) error {
	raise := func(pid int) error {
		process, err := windows.OpenProcess(windows.PROCESS_SET_INFORMATION, false, uint32(pid))
		if err != nil {
			return err
		}
		defer windows.CloseHandle(process)
		return windows.SetPriorityClass(process, windows.HIGH_PRIORITY_CLASS)
	}
	buffer := make([]uintptr, 66)
	if err := windows.QueryInformationJobObject(console.job, windows.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(&buffer[0])), uint32(len(buffer))*uint32(unsafe.Sizeof(uintptr(0))), nil); err != nil {
		return err
	}
	count := *(*uint32)(unsafe.Add(unsafe.Pointer(&buffer[0]), 4))
	for _, pid := range unsafe.Slice((*uintptr)(unsafe.Add(unsafe.Pointer(&buffer[0]), 8)), int(count)) {
		if err := raise(int(pid)); err != nil {
			return err
		}
	}
	for _, pid := range after {
		if !slices.Contains(before, pid) {
			if err := raise(pid); err != nil {
				return err
			}
		}
	}
	return nil
}

type probeChunk struct {
	at   int64
	data []byte
}

// probeEdges are the upper bounds, in milliseconds, of the counts a run keeps
// of how long things took.
var probeEdges = []int64{1, 5, 20, 50, 250, 1000}

type probeCounts [7]int

func (c *probeCounts) add(length time.Duration) {
	for index, edge := range probeEdges {
		if length < time.Duration(edge)*time.Millisecond {
			c[index]++
			return
		}
	}
	c[len(probeEdges)]++
}

func (c probeCounts) String() string {
	var parts []string
	for index, edge := range probeEdges {
		parts = append(parts, fmt.Sprintf("under %dms: %d", edge, c[index]))
	}
	return strings.Join(append(parts, fmt.Sprintf("1s or more: %d", c[len(probeEdges)])), ", ")
}

// probeStats is what a run keeps for one of its phases.
type probeStats struct {
	consoles, keys, failed int
	total                  probeCounts
	worst                  map[string]int64
	childStalls            probeCounts
	slowWrites             probeCounts
}

func probeMs(nanoseconds int64) string {
	return fmt.Sprintf("%.1fms", float64(nanoseconds)/1e6)
}

// probeOverlaps names the spans that overlap from..to, each by when it began
// and ended after from.
func probeOverlaps(spans []probeSpan, from, to int64) string {
	var parts []string
	for _, span := range spans {
		if span.From < to && span.To > from {
			parts = append(parts, fmt.Sprintf("%s to %s", probeMs(span.From-from), probeMs(span.To-from)))
		}
	}
	if parts == nil {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func TestLatencyStages(t *testing.T) {
	budget, err := time.ParseDuration(os.Getenv("PROBE_STAGES_FOR"))
	if err != nil {
		t.Skip("PROBE_STAGES_FOR does not say how long to run")
	}
	churnAfter, err := time.ParseDuration(os.Getenv("PROBE_STAGES_CHURN_AFTER"))
	if err != nil {
		churnAfter = budget
	}
	spinAfter, err := time.ParseDuration(os.Getenv("PROBE_STAGES_SPIN_AFTER"))
	if err != nil {
		spinAfter = budget
	}
	isHigh := os.Getenv("PROBE_STAGES_HIGH") != ""
	if isHigh {
		if err := windows.SetPriorityClass(windows.CurrentProcess(), windows.HIGH_PRIORITY_CLASS); err != nil {
			t.Fatal(err)
		}
	}
	clock := startProbeClock()
	folder := t.TempDir()
	began := time.Now()
	beganAt := probeNow()

	var churned atomic.Int64
	stopChurn := make(chan struct{})
	churnEnded := make(chan struct{})
	isChurning := false
	startChurn := func() {
		isChurning = true
		go func() {
			defer close(churnEnded)
			block := make([]byte, 4<<20)
			for index := range block {
				block[index] = byte(index * 31)
			}
			for number := 0; ; number++ {
				_ = os.Remove(filepath.Join(folder, fmt.Sprintf("churn-%d.bin", number-8)))
				file, err := os.Create(filepath.Join(folder, fmt.Sprintf("churn-%d.bin", number)))
				if err != nil {
					return
				}
				for range 16 {
					select {
					case <-stopChurn:
						file.Close()
						return
					default:
					}
					written, err := file.Write(block)
					churned.Add(int64(written))
					if err != nil {
						break
					}
				}
				_ = file.Sync()
				file.Close()
			}
		}()
	}
	var spinning *exec.Cmd
	startSpin := func() {
		spinning = exec.Command(os.Args[0], "-test.run=^TestLatencyStagesSpin$", "--", "stages-spin", (budget - time.Since(began) + 5*time.Second).Round(time.Second).String())
		if err := spinning.Start(); err != nil {
			t.Logf("the spinning program did not start: %v", err)
			spinning = nil
		}
	}

	phases := map[string]*probeStats{}
	phase := func(name string) *probeStats {
		if phases[name] == nil {
			phases[name] = &probeStats{worst: map[string]int64{}}
		}
		return phases[name]
	}
	since := func(at int64) string { return fmt.Sprintf("+%.1fs", float64(at-beganAt)/1e9) }

	lastSaid := time.Now()
	for number := 1; time.Since(began) < budget; number++ {
		name := "quiet"
		switch elapsed := time.Since(began); {
		case elapsed >= spinAfter:
			name = "spin"
			if isChurning {
				isChurning = false
				close(stopChurn)
				<-churnEnded
			}
			if spinning == nil {
				startSpin()
			}
		case elapsed >= churnAfter:
			name = "churn"
			if !isChurning {
				startChurn()
			}
		}
		stats := phase(name)
		activity := []string{"idle", "busy", "wrapped"}[(number-1)%3]
		func() {
			progressPath := filepath.Join(folder, "progress.log")
			notesPath := filepath.Join(folder, "notes.json")
			_ = os.Remove(notesPath)
			var saidUnread atomic.Int64
			var servers []int
			if isHigh {
				servers = consoleServers(t)
			}
			console, err := Start(Spec{
				Args: []string{os.Args[0], "-test.run=^TestLatencyStagesChild$", "--", "stages-child", activity},
				Env:  append(os.Environ(), "PROBE_STAGES_PROGRESS="+progressPath, "PROBE_STAGES_NOTES="+notesPath),
				Cols: 120,
				Rows: 40,
				Unread: func(isUnread bool) {
					if isUnread {
						saidUnread.Add(1)
					}
				},
			})
			if err != nil {
				stats.failed++
				t.Logf("console %d (%s, %s): Start: %v", number, activity, name, err)
				return
			}
			stats.consoles++
			if isHigh {
				if err := probeRaise(console, servers, consoleServers(t)); err != nil {
					t.Logf("console %d: raise: %v", number, err)
				}
			}
			output := make(chan probeChunk, 256)
			stopping, ended := make(chan struct{}), make(chan struct{})
			defer func() {
				close(stopping)
				_ = console.Close()
				<-ended
			}()
			go func() {
				defer close(ended)
				defer close(output)
				for {
					buffer := make([]byte, 32<<10)
					count, err := console.Read(buffer)
					at := probeNow()
					if count > 0 {
						select {
						case output <- probeChunk{at, buffer[:count]}:
						case <-stopping:
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
			var pending string
			var lastRead int64
			waitFor := func(marker string, limit time.Duration) (read, seen int64, isSeen bool) {
				timer := time.NewTimer(limit)
				defer timer.Stop()
				for {
					if at := strings.Index(pending, marker); at >= 0 {
						pending = pending[at+len(marker):]
						return lastRead, probeNow(), true
					}
					if len(pending) > 4096 {
						pending = pending[len(pending)-4096:]
					}
					select {
					case chunk, isOpen := <-output:
						if !isOpen {
							return 0, 0, false
						}
						pending += string(chunk.data)
						lastRead = chunk.at
					case <-timer.C:
						return 0, 0, false
					}
				}
			}
			if _, _, isSeen := waitFor("latency-ready", 15*time.Second); !isSeen {
				stats.failed++
				t.Logf("console %d (%s, %s) at %s: the child never said it was ready", number, activity, name, since(probeNow()))
				return
			}
			type typed struct{ w0, w1, read, seen int64 }
			var keys []typed
			for sequence := 1; sequence <= 40; sequence++ {
				w0 := probeNow()
				if _, err := console.Write([]byte("a")); err != nil {
					stats.failed++
					t.Logf("console %d (%s, %s): Write: %v", number, activity, name, err)
					return
				}
				w1 := probeNow()
				read, seen, isSeen := waitFor(fmt.Sprintf("key-%04d", sequence), 10*time.Second)
				if !isSeen {
					stats.failed++
					testStalls, _ := clock.read()
					t.Logf("console %d (%s, %s) at %s: key %d did not come back within 10s; keys said unread %d times; test clock stalls since it was typed: %s", number, activity, name, since(w0), sequence, saidUnread.Load(), probeOverlaps(testStalls, w0, probeNow()))
					return
				}
				keys = append(keys, typed{w0, w1, read, seen})
			}
			if _, err := console.Write([]byte("?")); err != nil {
				stats.failed++
				return
			}
			if _, _, isSeen := waitFor("notes-done", 10*time.Second); !isSeen {
				stats.failed++
				t.Logf("console %d (%s, %s) at %s: the child never wrote its notes", number, activity, name, since(probeNow()))
				return
			}
			encoded, err := os.ReadFile(notesPath)
			var notes probeChildNotes
			if err == nil {
				err = json.Unmarshal(encoded, &notes)
			}
			if err != nil || len(notes.Keys) != len(keys) {
				stats.failed++
				t.Logf("console %d (%s, %s): the child's notes: %v, %d keys for %d typed", number, activity, name, err, len(notes.Keys), len(keys))
				return
			}
			testStalls, _ := clock.read()
			for _, stall := range notes.Stalls {
				stats.childStalls.add(stall.length())
				if stall.length() >= 100*time.Millisecond {
					t.Logf("child clock stall: console %d (%s, %s) at %s: %s", number, activity, name, since(stall.From), stall.length().Round(100*time.Microsecond))
				}
			}
			for _, write := range notes.SlowWrites {
				stats.slowWrites.add(write.length())
				if write.length() >= 50*time.Millisecond {
					t.Logf("slow progress file write: console %d (%s, %s) at %s: %s", number, activity, name, since(write.From), write.length().Round(100*time.Microsecond))
				}
			}
			for index, key := range keys {
				child := notes.Keys[index]
				legs := map[string]int64{
					"typing":      key.w1 - key.w0,
					"way in":      child.Read - key.w1,
					"file write":  child.Filed - child.Read,
					"print":       child.Printed - child.Filed,
					"way out":     key.read - child.Filed,
					"handing on":  key.seen - key.read,
					"second file": child.Filed2 - child.Printed,
					"whole key":   key.seen - key.w0,
				}
				for leg, length := range legs {
					stats.worst[leg] = max(stats.worst[leg], length)
				}
				stats.keys++
				stats.total.add(time.Duration(legs["whole key"]))
				if legs["whole key"] < int64(50*time.Millisecond) {
					continue
				}
				t.Logf("slow key: at %s console %d (%s, %s) key %d took %s: typing %s, way in %s (the child had asked %s before it was typed), its file write %s, its print %s, way out from before the print %s, handing on %s; test clock stalls %s; child clock stalls %s; slow file writes %s; keys said unread %d times",
					since(key.w0), number, activity, name, index+1, probeMs(legs["whole key"]), probeMs(legs["typing"]), probeMs(legs["way in"]), probeMs(key.w0-child.Asked), probeMs(legs["file write"]), probeMs(legs["print"]), probeMs(legs["way out"]), probeMs(legs["handing on"]),
					probeOverlaps(testStalls, key.w0, key.seen), probeOverlaps(notes.Stalls, key.w0, key.seen), probeOverlaps(notes.SlowWrites, key.w0, key.seen), saidUnread.Load())
			}
		}()
		if time.Since(lastSaid) >= time.Minute {
			lastSaid = time.Now()
			t.Logf("after %s: %d consoles, %d keys, %d failed in %s", time.Since(began).Round(time.Second), stats.consoles, stats.keys, stats.failed, name)
		}
	}
	if isChurning {
		close(stopChurn)
		<-churnEnded
	}
	if spinning != nil {
		_ = spinning.Process.Kill()
		_ = spinning.Wait()
	}

	testStalls, beats := clock.read()
	var testCounts probeCounts
	for _, stall := range testStalls {
		testCounts.add(stall.length())
		if stall.length() >= 50*time.Millisecond {
			t.Logf("test clock stall: at %s: %s", since(stall.From), stall.length().Round(100*time.Microsecond))
		}
	}
	t.Logf("high priority: %t", isHigh)
	t.Logf("ran %s on %d processors; the test's clock thread beat %d times and stalled 20ms or more %d times: %s", time.Since(began).Round(time.Second), runtime.NumCPU(), beats, len(testStalls), testCounts)
	t.Logf("disk churn wrote %d MB", churned.Load()>>20)
	for _, name := range []string{"quiet", "churn", "spin"} {
		stats := phases[name]
		if stats == nil {
			continue
		}
		t.Logf("%s: %d consoles, %d keys, %d consoles failed", name, stats.consoles, stats.keys, stats.failed)
		t.Logf("%s: whole keys: %s", name, stats.total)
		t.Logf("%s: child clock stalls of 20ms or more: %s", name, stats.childStalls)
		t.Logf("%s: progress file writes of 5ms or more: %s", name, stats.slowWrites)
		for _, leg := range []string{"whole key", "typing", "way in", "file write", "print", "way out", "handing on", "second file"} {
			t.Logf("%s: worst %s: %s", name, leg, probeMs(stats.worst[leg]))
		}
	}
}
