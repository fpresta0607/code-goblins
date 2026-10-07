//go:build !windows

package supervisor

import (
	"errors"
	"time"
)

// SignInBegan is read only on Windows, the fleet's platform.
func SignInBegan() (time.Time, error) {
	return time.Time{}, errors.New("when this sign-in began is read only on Windows")
}
