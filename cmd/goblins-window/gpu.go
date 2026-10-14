package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// webviewName is the program WebView2 runs each of its processes as.
const webviewName = "msedgewebview2.exe"

// gpuArgument marks the WebView2 process that draws for the window.
const gpuArgument = "--type=gpu-process"

// browsersGrandchildren lists the processes WebView2's browser processes
// started for window: the browser is the window's own child, and its GPU
// process is among the browser's children. A child is taken only when it
// started after its parent, as a reused pid would not.
func browsersGrandchildren(processes []proc.Entry, window int) []proc.Entry {
	var browsers []proc.Entry
	for _, process := range processes {
		if process.ParentPID == window && strings.EqualFold(process.ExeBase, webviewName) {
			browsers = append(browsers, process)
		}
	}
	var children []proc.Entry
	for _, process := range processes {
		if !strings.EqualFold(process.ExeBase, webviewName) {
			continue
		}
		if slices.ContainsFunc(browsers, func(browser proc.Entry) bool {
			return process.ParentPID == browser.PID && !process.Start.Before(browser.Start)
		}) {
			children = append(children, process)
		}
	}
	return children
}

// endGPU ends the GPU process of the WebView2 this window runs, which
// WebView2 then starts again, as it does after the GPU process crashes. It is
// how the window mends a page that a load did not: one whose GPU process
// hangs draws nothing, whatever page it holds.
func endGPU(window int) error {
	processes, err := proc.Processes()
	if err != nil {
		return err
	}
	// A sandboxed child, such as a renderer, may refuse to be read, so what
	// the others said matters only when no GPU process was ended.
	var errs []error
	for _, child := range browsersGrandchildren(processes, window) {
		err := proc.TerminateVerifiedIn(child.PID, child.Start, func(identity proc.Identity) error {
			if !slices.Contains(identity.Arguments, gpuArgument) {
				return errNotGPU
			}
			return nil
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, errNotGPU) {
			errs = append(errs, err)
		}
	}
	return errors.Join(append([]error{fmt.Errorf("no WebView2 GPU process of the window, pid %d, was ended", window)}, errs...)...)
}

var errNotGPU = errors.New("not the WebView2 GPU process")
