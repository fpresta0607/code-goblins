//go:build !windows

package supervisor

// hideConsoleOnDesktop hides nothing: a console window is a Windows thing.
func hideConsoleOnDesktop(pid int) (bool, error) {
	return false, nil
}
