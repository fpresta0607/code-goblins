//go:build !windows

package uninstall

// registered has nothing to find off Windows, which keeps no registry.
func (m Machine) registered() ([]Item, error) { return nil, nil }
