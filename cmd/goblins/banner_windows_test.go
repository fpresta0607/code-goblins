package main

import (
	"bytes"
	"os"
	"testing"
)

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
	if !consoleTakesEscapes(console) {
		t.Error("NO_COLOR made a console plain, though it is about colour alone")
	}
}
