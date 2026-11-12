//go:build !windows

package lifecycle

import "github.com/fpresta0607/code-goblins/internal/home"

// OwnerOf reads every process as no terminal's or task's of home h, as it
// reads one that cannot be read: reading a process's mark and place is not
// ported to this system yet.
func OwnerOf(h home.Home) func(pid int) string {
	return func(int) string { return "" }
}
