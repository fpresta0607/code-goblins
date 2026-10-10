//go:build !windows

package home

import "os"

// processImages lists no program: the msys runtime, whose mounts LiveTemps
// reads, runs on Windows only.
func processImages() ([]string, error) { return nil, nil }

// userTemp is the machine's temporary folder.
func userTemp() (string, error) { return os.TempDir(), nil }
