package main

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
)

// The launcher's banner is board-ui's art (data/board-ui/banner): banner.txt
// is printed as it is when colour is off, and banner-shades.txt names each art
// character's green with 1, 2 or 3, a space meaning no colour.
var (
	//go:embed banner/banner.txt
	bannerArt string
	//go:embed banner/banner-shades.txt
	bannerShades string
)

// bannerGreens are the board's own greens, as truecolor foregrounds, by the
// shade digit that names them.
//
// ponytail: truecolor only. Every Windows console that accepts ANSI sequences
// takes truecolor, and anything else prints plain; add board-ui's 256 and
// 16-colour palettes if a terminal ever colours without truecolor.
var bannerGreens = map[byte]string{
	'1': "\x1b[38;2;47;158;110m",  // #2F9E6E, the wordmark's bottom row
	'2': "\x1b[38;2;0;229;155m",   // #00E59B, the board's accent
	'3': "\x1b[38;2;110;231;183m", // #6EE7B7, the board's mint
}

const colorReset = "\x1b[0m"

// renderBanner returns the art, one empty line and the board and status
// lines. With colour each art character takes its shade, the labels the
// accent and the board URL the mint inside an OSC 8 link to itself; without,
// the art is banner.txt exactly and the lines are plain text.
func renderBanner(color bool, url, status string) string {
	var b strings.Builder
	shades := strings.Split(strings.ReplaceAll(bannerShades, "\r\n", "\n"), "\n")
	for index, line := range strings.Split(strings.TrimRight(strings.ReplaceAll(bannerArt, "\r\n", "\n"), "\n"), "\n") {
		if !color {
			b.WriteString(line + "\n")
			continue
		}
		shade := byte(' ')
		for column := 0; column < len(line); column++ {
			next := byte(' ')
			if index < len(shades) && column < len(shades[index]) {
				next = shades[index][column]
			}
			if next != shade {
				if green, ok := bannerGreens[next]; ok {
					b.WriteString(green)
				} else {
					b.WriteString(colorReset)
				}
				shade = next
			}
			b.WriteByte(line[column])
		}
		if shade != ' ' {
			b.WriteString(colorReset)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	board, boardLabel, statusLabel := url, "board", "status"
	if color {
		board = "\x1b]8;;" + url + "\x1b\\" + bannerGreens['3'] + url + colorReset + "\x1b]8;;\x1b\\"
		boardLabel = bannerGreens['2'] + boardLabel + colorReset
		statusLabel = bannerGreens['2'] + statusLabel + colorReset
	}
	fmt.Fprintf(&b, "  %s   %s\n", boardLabel, board)
	fmt.Fprintf(&b, "  %s  %s\n", statusLabel, status)
	return b.String()
}

var setConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")

// bannerColor reports whether out takes the banner's colours: a console, with
// no NO_COLOR set, that accepts ANSI sequences once asked to. Anything else,
// a pipe or a file included, gets the plain banner.
func bannerColor(out io.Writer) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	handle := syscall.Handle(file.Fd())
	var mode uint32
	if err := syscall.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	const enableVirtualTerminalProcessing = 0x0004
	if mode&enableVirtualTerminalProcessing != 0 {
		return true
	}
	enabled, _, _ := setConsoleMode.Call(uintptr(handle), uintptr(mode|enableVirtualTerminalProcessing))
	return enabled != 0
}
