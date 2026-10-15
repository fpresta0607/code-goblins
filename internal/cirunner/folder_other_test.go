//go:build !windows

package cirunner

import "testing"

// protectFolder has no access list to set where the service never runs.
func protectFolder(*testing.T, string) {}
