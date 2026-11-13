//go:build !windows

package supervisor

import (
	"context"
	"errors"
	"fmt"
)

// errPipeNotPorted is what a request to the supervisor returns on this
// system: the supervisor takes requests on a Windows named pipe, and the
// transport for other systems is still to be written.
var errPipeNotPorted = fmt.Errorf("supervisor: requests to the supervisor are not ported to this system yet: %w", errors.ErrUnsupported)

// serveRunRequests serves nothing: the supervisor's request transport is not
// ported to this system yet.
func (s *Service) serveRunRequests(ctx context.Context) {}

// Reported tells no supervisor of the report, which shows at the board's next
// refresh, as it does when no supervisor runs.
func Reported(stateDir string) {}

// sendPipeRequest refuses, since requests to the supervisor are not ported
// yet.
func sendPipeRequest(stateDir string, req runPipeRequest) error {
	return errPipeNotPorted
}

// askPipe refuses, as sendPipeRequest does.
func askPipe(stateDir string, req runPipeRequest) (pipeReply, error) {
	return pipeReply{}, errPipeNotPorted
}
