package conpty

import (
	"encoding/binary"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// burstsSpells lists, in milliseconds and separated by commas, the busy
// spells a "bursts" child reads nothing in, one after another and then from
// the first again.
const burstsSpells = "CONPTY_TEST_BURSTS_SPELLS"

// burstsChild reads its console's input only between busy spells, all of it
// at once, as Claude Code does while it streams: its keys wait out the spell
// they were typed in, and no longer.
func burstsChild() {
	var spells []time.Duration
	for _, spell := range strings.Split(os.Getenv(burstsSpells), ",") {
		milliseconds, err := strconv.Atoi(spell)
		if err != nil {
			os.Exit(2)
		}
		spells = append(spells, time.Duration(milliseconds)*time.Millisecond)
	}
	input := windows.Handle(os.Stdin.Fd())
	readInput := kernel32.NewProc("ReadConsoleInputW")
	fmt.Println("ready")
	for spell := 0; ; spell = (spell + 1) % len(spells) {
		time.Sleep(spells[spell])
		for {
			var unread uint32
			if ok, _, _ := getNumberOfConsoleInputEvents.Call(uintptr(input), uintptr(unsafe.Pointer(&unread))); ok == 0 {
				os.Exit(2)
			}
			if unread == 0 {
				break
			}
			var records [64]inputRecord
			var read uint32
			if ok, _, _ := readInput.Call(uintptr(input), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&read))); ok == 0 {
				os.Exit(2)
			}
		}
	}
}

// saying is when a console said key presses sat unread, and when it said they
// were read, as the console's owner is told.
type saying struct {
	mu   sync.Mutex
	said []said
}

type said struct {
	at       time.Time
	isUnread bool
}

func (s *saying) take(isUnread bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.said = append(s.said, said{time.Now(), isUnread})
}

func (s *saying) all() []said {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]said(nil), s.said...)
}

// typeWhileBursts types a key every gap for span into a console whose
// program reads its input between spells, and returns what the console said
// of unread key presses meanwhile and for a moment after.
func typeWhileBursts(t *testing.T, spells string, gap, span time.Duration) []said {
	t.Helper()
	var saying saying
	console, _ := startChild(t, Spec{
		Cols: 80, Rows: 25,
		Env:    append(os.Environ(), childMode+"=bursts", burstsSpells+"="+spells),
		Unread: saying.take,
	})
	for end := time.Now().Add(span); time.Now().Before(end); time.Sleep(gap) {
		if _, err := console.Write([]byte("a")); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(2 * (unreadAfter + unreadShownFor))
	return saying.all()
}

// awaitUnread returns what the console next says of unread key presses,
// failing the test when it says nothing within a minute.
func awaitUnread(t *testing.T, said <-chan bool, want bool) {
	t.Helper()
	select {
	case isUnread := <-said:
		if isUnread != want {
			t.Fatalf("the console said unread=%t, want %t", isUnread, want)
		}
	case <-time.After(time.Minute):
		t.Fatalf("the console did not say unread=%t within a minute", want)
	}
}

// A key typed while the program is busy sits unread in the console's input,
// where nothing on the screen shows it was typed: the Overlord pressed Enter
// on a row of Claude Code's permissions screen on 2026-10-09 while Claude
// Code was starting a tool, and nothing was approved. The console says such
// key presses sit unread, and says they were read once the program reads
// them.
func TestKeysABusyProgramLeavesUnreadAreSaidUnreadAndThenRead(t *testing.T) {
	readNow := filepath.Join(t.TempDir(), "read-now")
	said := make(chan bool, 8)
	console, output := startChild(t, Spec{
		Cols: 80, Rows: 25,
		Env:    append(os.Environ(), childMode+"=late", lateChildFile+"="+readNow),
		Unread: func(isUnread bool) { said <- isUnread },
	})

	typeLine(t, console, "typed while busy")

	awaitUnread(t, said, true)
	if err := os.WriteFile(readNow, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	awaitUnread(t, said, false)
	output.waitFor(t, "got typed while busy")
}

// A key the program reads as usual is never said to sit unread, so a viewer
// shows nothing for ordinary typing.
func TestKeysAProgramReadsAtOnceAreNeverSaidUnread(t *testing.T) {
	said := make(chan bool, 8)
	console, output := startChild(t, Spec{Cols: 80, Rows: 25, Unread: func(isUnread bool) { said <- isUnread }})

	for _, line := range []string{"one", "two", "three"} {
		typeLine(t, console, line)
		output.waitFor(t, "got "+line)
	}

	select {
	case isUnread := <-said:
		t.Fatalf("the console said unread=%t of keys its program read at once", isUnread)
	case <-time.After(2 * unreadAfter): // stall-accepted: nothing is meant to be said, so a stall only makes the wait longer.
	}
}

// A window's focus and a resize are input a program may leave unread for as
// long as it likes, and neither was typed.
func TestInputThatIsNoKeyPressIsNeverSaidUnread(t *testing.T) {
	said := make(chan bool, 8)
	console, _ := startChild(t, Spec{
		Cols: 80, Rows: 25,
		Env:    append(os.Environ(), childMode+"=late", lateChildFile+"="+filepath.Join(t.TempDir(), "never")),
		Unread: func(isUnread bool) { said <- isUnread },
	})

	if _, err := console.Write([]byte("\x1b[I")); err != nil {
		t.Fatal(err)
	}
	if err := console.Resize(90, 30); err != nil {
		t.Fatal(err)
	}

	select {
	case isUnread := <-said:
		t.Fatalf("the console said unread=%t of a focus report and a resize", isUnread)
	case <-time.After(3 * unreadAfter): // stall-accepted: nothing is meant to be said, so a stall only makes the wait longer.
	}
}

// The Overlord, 2026-10-14, of the note: "program busy typed keys waiting
// glitches adn flashes freuqnetly". Claude Code reads typed keys in bursts
// while it streams. A key typed while keys keep coming is read within the
// spell it was typed in, so none waits long, but at most looks some key just
// typed is still unread, and the console once timed the wait from the first
// key of the run: it said keys sat unread of keys none of which had waited
// 300 ms. Its wait is the oldest unread key's own.
func TestKeysABurstyProgramReadsWithinItsSpellsAreNeverSaidUnread(t *testing.T) {
	// stall-accepted: the span is how long the test types and is meant to run out; a stall while it types can still hold a key past unreadAfter, which no bound in the test can prevent.
	said := typeWhileBursts(t, "250", 50*time.Millisecond, 3*time.Second)

	if len(said) > 0 {
		t.Fatalf("the console said %d times of key presses none of which waited 300 ms, the first unread=%t", len(said), said[0].isUnread)
	}
}

// A real wait just past the console's threshold once showed the note for
// 50 ms, and a run of typing hid it for 350 ms between two showings. The note
// changes no sooner than a second after it last changed: said, it stays at
// least unreadShownFor, and once its keys are read no key typed after has
// waited unreadAfter, so it stays away at least that long.
func TestKeysThatWaitAreSaidUnreadSteadily(t *testing.T) {
	// Some spells keep keys past unreadAfter and most just short of it.
	// stall-accepted: the span is how long the test types and is meant to run out; a stall while it types can still hold a key past unreadAfter, which no bound in the test can prevent.
	said := typeWhileBursts(t, "1300,450,450,450", 180*time.Millisecond, 8*time.Second)

	const steady = time.Second
	// The late slack the scheduler can add to a reading.
	const slack = 150 * time.Millisecond
	if len(said) == 0 || len(said)%2 != 0 {
		t.Fatalf("the console said %d times, want key presses said unread and then read, at least once", len(said))
	}
	for index, saying := range said {
		if saying.isUnread != (index%2 == 0) {
			t.Fatalf("the console said unread=%t at its saying %d, want unread and read by turns", saying.isUnread, index+1)
		}
		if lasted := saying.at.Sub(said[max(index-1, 0)].at); index > 0 && lasted < steady-slack {
			t.Errorf("the note was %s for %s, want at least %s", map[bool]string{true: "away", false: "shown"}[saying.isUnread], lasted.Round(time.Millisecond), steady)
		}
	}
}

// keyDown is the record of key char going down, and focus that of a window's
// focus changing.
func keyDown(char rune) inputRecord {
	record := inputRecord{Kind: keyEvent}
	binary.LittleEndian.PutUint32(record.Event[:4], 1)
	binary.LittleEndian.PutUint16(record.Event[10:12], uint16(char))
	return record
}

var focus = inputRecord{Kind: 0x10}

// What the waker says of the records unread at each look, read without a
// console: each look is when it was taken and what was unread then.
func TestTheWaitTheWakerReadsIsTheOldestUnreadKeysOwn(t *testing.T) {
	type look struct {
		at     time.Duration
		unread []inputRecord
		// isPart is set when unread is only the first of the records.
		isPart bool
	}
	// every looks every 50 ms from start to before end, with what unread
	// returns for each look's place in the run.
	every := func(start, end time.Duration, unread func(index int) []inputRecord) []look {
		var looks []look
		for index, at := 0, start; at < end; index, at = index+1, at+unreadLook {
			looks = append(looks, look{at: at, unread: unread(index)})
		}
		return looks
	}
	waiting := []inputRecord{keyDown('y')}
	cases := []struct {
		name  string
		looks []look
		want  map[time.Duration]string
	}{
		{
			name:  "a key read and another typed between two looks is a new key",
			looks: every(0, 3*time.Second, func(index int) []inputRecord { return []inputRecord{keyDown(rune('a' + index%26))} }),
		},
		{
			name: "a key left unread is said at unreadAfter and read no sooner than unreadShownFor after",
			looks: append(every(0, unreadAfter+100*time.Millisecond, func(int) []inputRecord { return waiting }),
				every(unreadAfter+100*time.Millisecond, 3*time.Second, func(int) []inputRecord { return nil })...),
			want: map[time.Duration]string{unreadAfter: keysUnreadLine, unreadAfter + unreadShownFor: keysReadLine},
		},
		{
			name: "a key typed after the note went has waited no time",
			looks: append(append(every(0, unreadAfter+100*time.Millisecond, func(int) []inputRecord { return waiting }),
				every(unreadAfter+100*time.Millisecond, 2*unreadAfter+50*time.Millisecond, func(int) []inputRecord { return nil })...),
				every(2*unreadAfter+50*time.Millisecond, 4*unreadAfter, func(int) []inputRecord { return []inputRecord{keyDown('n')} })...),
			want: map[time.Duration]string{unreadAfter: keysUnreadLine, 2 * unreadAfter: keysReadLine, 3*unreadAfter + 50*time.Millisecond: keysUnreadLine},
		},
		{
			name: "keys read one at a time keep the wait of the one left",
			looks: []look{
				{at: 0, unread: []inputRecord{keyDown('a'), keyDown('b'), keyDown('c')}},
				{at: unreadAfter / 2, unread: []inputRecord{keyDown('b'), keyDown('c')}},
				{at: unreadAfter, unread: []inputRecord{keyDown('c'), keyDown('d')}},
			},
			want: map[time.Duration]string{unreadAfter: keysUnreadLine},
		},
		{
			name:  "a window's focus and the waker's own wakes are never a key",
			looks: every(0, 3*time.Second, func(int) []inputRecord { return []inputRecord{focus, {Kind: menuEvent}} }),
		},
		{
			name: "of a long paste only its start is seen, and it keeps its wait",
			looks: []look{
				{at: 0, unread: []inputRecord{keyDown('p'), keyDown('a'), keyDown('s')}},
				{at: unreadAfter, unread: []inputRecord{keyDown('p'), keyDown('a')}, isPart: true},
			},
			want: map[time.Duration]string{unreadAfter: keysUnreadLine},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			var keys unreadKeys
			start := time.Now()
			got := map[time.Duration]string{}

			// Act
			for _, look := range testCase.looks {
				// stall-accepted: a made-up time each look is said to be taken at, which no wait compares with the clock.
				if line := keys.look(start.Add(look.at), look.unread, look.isPart); line != "" {
					got[look.at] = line
				}
			}

			// Assert
			if !maps.Equal(got, testCase.want) {
				t.Errorf("the waker said %v, want %v", got, testCase.want)
			}
		})
	}
}
