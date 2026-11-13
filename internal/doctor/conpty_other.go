//go:build !windows

package doctor

// checkConPTY reports that this system has no native terminal yet: Code
// Goblins runs its programs in a Windows pseudo console, and the terminal for
// other systems is still to be written.
func checkConPTY() Check {
	return Check{Name: "conpty", Hint: "native terminals run only on Windows so far", Err: "this system has no native terminal for Code Goblins yet"}
}
