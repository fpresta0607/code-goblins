//go:build !windows

package uninstall

import "context"

// running finds nothing off Windows, where Code Goblins runs no program.
func (m Machine) running(context.Context) ([]Item, error) { return nil, nil }
