//go:build !windows

package supervisor

import "os"

// shellUse says nothing off Windows: only Windows PowerShell on a Windows
// runner has been seen to sit past the wait.
func shellUse(*os.Process) string { return "" }
