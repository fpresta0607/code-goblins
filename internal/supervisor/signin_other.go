//go:build !windows

package supervisor

import (
	"errors"
	"time"
)

// SignedIn is read only on Windows, the fleet's platform.
func SignedIn() (time.Time, error) {
	return time.Time{}, errors.New("when this sign-in began is read only on Windows")
}
