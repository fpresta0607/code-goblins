package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestQuickstartStartsCFOInHomeBeforeOfferingBoard(t *testing.T) {
	for _, isBoard := range []bool{false, true} {
		f := newSessionFixture(t)
		f.runtime.setupAgent = func(context.Context, string, string, bool, io.Writer, io.Writer) (string, error) {
			return "claude", nil
		}
		f.runtime.choose = func(_ io.Writer, title string, choices []string, selected int) (int, error) {
			if len(f.nativeStarts) != 1 || f.nativeStarts[0] != f.home.Root {
				t.Fatalf("native starts=%q", f.nativeStarts)
			}
			if len(f.opened) != 0 {
				t.Fatal("browser opened before choosing")
			}
			if selected != 0 || !strings.Contains(choices[0], "Default") || !strings.Contains(title, "Ctrl+click") {
				t.Fatalf("title=%s choices=%q default=%d", title, choices, selected)
			}
			if isBoard {
				return 1, nil
			}
			return 0, nil
		}
		exit, _, stderr := f.launch()
		if exit != 0 {
			t.Fatalf("exit=%d stderr=%s", exit, stderr)
		}
		if (len(f.opened) == 1) != isBoard || (len(f.nativeAttached) == 1) == isBoard {
			t.Fatalf("opened=%q attached=%q", f.opened, f.nativeAttached)
		}
	}
}

func TestQuickstartCancelledSetupCannotLaunchCFOOrBoard(t *testing.T) {
	f := newSessionFixture(t)
	f.runtime.setupAgent = func(context.Context, string, string, bool, io.Writer, io.Writer) (string, error) {
		return "", errors.New("setup cancelled")
	}
	exit, _, stderr := f.launch()
	if exit != 1 || len(f.nativeStarts) != 0 || len(f.opened) != 0 || !strings.Contains(stderr, "setup cancelled") {
		t.Fatalf("exit=%d starts=%q opened=%q stderr=%s", exit, f.nativeStarts, f.opened, stderr)
	}
}

func TestExplicitBoardRequiresCFOAndDoesNotAttach(t *testing.T) {
	f := newSessionFixture(t)
	f.runtime.setupAgent = func(context.Context, string, string, bool, io.Writer, io.Writer) (string, error) {
		return "claude", nil
	}
	f.runtime.choose = func(io.Writer, string, []string, int) (int, error) {
		t.Fatal("asked after explicit --board")
		return 0, nil
	}
	exit, _, stderr := f.launch("--board")
	if exit != 0 || len(f.nativeStarts) != 1 || len(f.opened) != 1 || len(f.nativeAttached) != 0 {
		t.Fatalf("exit=%d starts=%q opened=%q attached=%q stderr=%s", exit, f.nativeStarts, f.opened, f.nativeAttached, stderr)
	}
}

func TestQuickstartSetupRerunsChoice(t *testing.T) {
	f := newSessionFixture(t)
	wasRerun := false
	f.runtime.setupAgent = func(_ context.Context, _ string, _ string, isRerun bool, _, _ io.Writer) (string, error) {
		wasRerun = isRerun
		return "pi", nil
	}
	f.runtime.choose = func(io.Writer, string, []string, int) (int, error) { return 0, nil }
	exit, _, stderr := f.launch("setup")
	if exit != 0 || !wasRerun {
		t.Fatalf("exit=%d rerun=%t stderr=%s", exit, wasRerun, stderr)
	}
}

func TestQuickstartHomeFailureStartsNothing(t *testing.T) {
	f := newSessionFixture(t)
	f.runtime.resolveHome = func() (home.Home, error) { return home.Home{}, errors.New("missing home") }
	exit, _, stderr := f.launch()
	if exit != 1 || len(f.nativeStarts) != 0 || !strings.Contains(stderr, "missing home") {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
}

func TestQuickstartPublishesPinnedInstallersWithoutStartingAnything(t *testing.T) {
	fixture := newSessionFixture(t)
	exit, stdout, stderr := fixture.launch("setup", "--installers")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
	var installers map[string]string
	if err := json.Unmarshal([]byte(stdout), &installers); err != nil {
		t.Fatal(err)
	}
	if len(installers) != 3 || len(fixture.nativeStarts) != 0 || len(fixture.opened) != 0 {
		t.Fatalf("installers=%v starts=%q browser=%q", installers, fixture.nativeStarts, fixture.opened)
	}
	for _, name := range []string{"claude", "codex", "pi"} {
		parts := strings.Fields(installers[name])
		if len(parts) != 4 || strings.Join(parts[:3], " ") != "npm.cmd install -g" {
			t.Fatalf("installer=%q", installers[name])
		}
		at := strings.LastIndex(parts[3], "@")
		if at < 1 || len(strings.Split(parts[3][at+1:], ".")) != 3 {
			t.Fatalf("installer is not version-pinned: %s", installers[name])
		}
	}
}
