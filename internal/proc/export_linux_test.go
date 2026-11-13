package proc

import "testing"

// ReadMachineStartFrom makes every reading take the machine's start from
// path, not from /proc/stat, until the test ends.
func ReadMachineStartFrom(t testing.TB, path string) {
	was := machineStartFile
	t.Cleanup(func() { machineStartFile = was })
	machineStartFile = path
}

// ReadBootIDFrom makes every reading take the machine's boot ID from path,
// not from /proc, until the test ends.
func ReadBootIDFrom(t testing.TB, path string) {
	was := bootIDFile
	t.Cleanup(func() { bootIDFile = was })
	bootIDFile = path
}
