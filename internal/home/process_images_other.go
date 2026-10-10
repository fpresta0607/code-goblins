//go:build !windows

package home

// processImages lists no program: the msys runtime, whose mounts LiveTemps
// reads, runs on Windows only.
func processImages() ([]string, error) { return nil, nil }
