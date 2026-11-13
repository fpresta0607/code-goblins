package doctor

import "golang.org/x/sys/windows"

// pseudoConsole is the Windows call every native terminal runs in.
var pseudoConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("CreatePseudoConsole")

// checkConPTY reports whether this Windows has the pseudo console a native
// terminal runs its program in.
func checkConPTY() Check {
	check := Check{Name: "conpty", Version: "pseudo console available", Hint: "Windows 10 version 1809 or later, which native terminals run on"}
	if err := pseudoConsole.Find(); err != nil {
		check.Version, check.Err = "", "this Windows has no pseudo console"
	}
	return check
}
