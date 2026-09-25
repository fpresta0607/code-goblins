package main

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

const bannerBoard = "http://127.0.0.1:4310"

func artLines(text string) []string {
	return strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
}

// Without colour the banner is board-ui's banner.txt exactly, one empty line,
// and the two info lines as plain text with the URL written out.
func TestPlainBannerIsTheArtExactlyThenTheInfoLines(t *testing.T) {
	got := renderBanner(false, bannerBoard, "CFO supervising · 0 goblins working · 0 waiting on you")

	want := strings.Join(artLines(bannerArt), "\n") + "\n\n  board   " + bannerBoard + "\n  status  CFO supervising · 0 goblins working · 0 waiting on you\n"
	if got != want {
		t.Fatalf("plain banner =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "\x1b") {
		t.Fatal("the plain banner carries an escape sequence")
	}
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("line %q is wider than 80 columns", line)
		}
	}
}

var escapes = regexp.MustCompile("\x1b\\[[0-9;]*m|\x1b\\]8;;[^\x1b]*\x1b\\\\")

// With colour every art character takes the green its shade names, the
// board URL is a link to itself, and stripping the escapes leaves exactly the
// plain banner.
func TestColourBannerPaintsEachShadeAndLinksTheBoard(t *testing.T) {
	status := "CFO supervising · 2 goblins working · 1 waiting on you"
	got := renderBanner(true, bannerBoard, status)

	if plain := escapes.ReplaceAllString(got, ""); plain != renderBanner(false, bannerBoard, status) {
		t.Fatalf("the colour banner without its escapes =\n%s\nwant the plain banner", plain)
	}
	if !strings.Contains(got, "\x1b]8;;"+bannerBoard+"\x1b\\"+bannerGreens['3']+bannerBoard+colorReset+"\x1b]8;;\x1b\\") {
		t.Errorf("the board URL is not a mint OSC 8 link to itself: %q", got)
	}
	shadeOf := map[string]byte{}
	for digit, green := range bannerGreens {
		shadeOf[green] = digit
	}
	lines := strings.Split(got, "\n")
	shades := artLines(bannerShades)
	for index, want := range shades {
		painted := paintedShades(t, lines[index], shadeOf)
		if strings.TrimRight(painted, " ") != strings.TrimRight(want, " ") {
			t.Errorf("art line %d is painted\n%q\nwant\n%q", index, painted, want)
		}
	}
}

// paintedShades reads a coloured line back into the shade digit each printed
// character was painted with, a space for none.
func paintedShades(t *testing.T, line string, shadeOf map[string]byte) string {
	t.Helper()
	var painted strings.Builder
	current := byte(' ')
	for len(line) > 0 {
		if location := escapes.FindStringIndex(line); location != nil && location[0] == 0 {
			sequence := line[:location[1]]
			if sequence == colorReset {
				current = ' '
			} else if digit, ok := shadeOf[sequence]; ok {
				current = digit
			} else {
				t.Fatalf("unexpected escape %q in an art line", sequence)
			}
			line = line[location[1]:]
			continue
		}
		painted.WriteByte(current)
		line = line[1:]
	}
	if current != ' ' {
		t.Errorf("an art line leaves its colour on at its end")
	}
	return painted.String()
}

// Only a console that takes ANSI sequences gets colour: never into a buffer
// or a file, and never with NO_COLOR set, whatever its value.
func TestBannerColourIsOnlyForAConsoleWithoutNoColor(t *testing.T) {
	if bannerColor(&bytes.Buffer{}) {
		t.Error("a buffer got colour")
	}
	file, err := os.CreateTemp(t.TempDir(), "banner")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if bannerColor(file) {
		t.Error("a file got colour")
	}

	console, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no console to test NO_COLOR against: %v", err)
	}
	defer console.Close()
	t.Setenv("NO_COLOR", "")
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		t.Fatal(err)
	}
	if !bannerColor(console) {
		t.Skip("this console takes no ANSI sequences, so NO_COLOR cannot be told apart here")
	}
	t.Setenv("NO_COLOR", "")
	if bannerColor(console) {
		t.Error("NO_COLOR set to nothing still got colour")
	}
}
