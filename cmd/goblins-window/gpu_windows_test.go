package main

import (
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// The window ends a GPU process only among the processes its own WebView2
// browser started: never another program's WebView2, never a process of
// another name, and never one whose pid its browser's could have been reused
// for, which started before that browser.
func TestTheWindowLooksForItsGPUProcessOnlyUnderItsOwnWebView2(t *testing.T) {
	at := time.Date(2026, 10, 14, 1, 59, 44, 0, time.UTC)
	processes := []proc.Entry{
		{PID: 100, ParentPID: 1, ExeBase: "goblins-window.exe", Start: at},
		{PID: 200, ParentPID: 100, ExeBase: "msedgewebview2.exe", Start: at},
		{PID: 201, ParentPID: 200, ExeBase: "msedgewebview2.exe", Start: at.Add(13 * time.Second)},
		{PID: 202, ParentPID: 200, ExeBase: "MSEDGEWEBVIEW2.EXE", Start: at.Add(13 * time.Second)},
		{PID: 203, ParentPID: 200, ExeBase: "conhost.exe", Start: at},
		{PID: 204, ParentPID: 200, ExeBase: "msedgewebview2.exe", Start: at.Add(-time.Hour)},
		{PID: 300, ParentPID: 1, ExeBase: "other.exe", Start: at},
		{PID: 400, ParentPID: 300, ExeBase: "msedgewebview2.exe", Start: at},
		{PID: 401, ParentPID: 400, ExeBase: "msedgewebview2.exe", Start: at},
		{PID: 500, ParentPID: 100, ExeBase: "goblins.exe", Start: at},
		{PID: 501, ParentPID: 500, ExeBase: "msedgewebview2.exe", Start: at},
	}

	var got []int
	for _, process := range browsersGrandchildren(processes, 100) {
		got = append(got, process.PID)
	}

	slices.Sort(got)
	if want := []int{201, 202}; !slices.Equal(got, want) {
		t.Errorf("candidates = %v, want %v", got, want)
	}
}
