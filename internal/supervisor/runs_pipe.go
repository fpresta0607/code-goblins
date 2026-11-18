package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/memory"
)

const (
	// maxRunRequest bounds one request: the command plus its JSON escaping.
	maxRunRequest = 8 * maxRunCommand
	// runRequestTimeout bounds the work that waits on the loop or the
	// allowance.
	runRequestTimeout = 20 * time.Second
)

// runReadTimeout bounds how long a client takes to send its request, and
// runReplyTimeout how long goblins run-request waits for the answer. Only time
// the machine ran spends either (awake), since a pause of the whole machine
// stops the other side too.
var (
	runReadTimeout  = 10 * time.Second
	runReplyTimeout = 30 * time.Second
)

// firstInstanceWait is how long the supervisor waits for the name of its run
// requests, which another instance still holds, to come free: a supervisor
// that just stopped releases it within moments, and a name held longer than
// this is not ours. Only time the machine ran spends it (awake), since a
// pause of the whole machine stops the supervisor before too.
const firstInstanceWait = 2 * time.Second

// requestTimeout bounds the work of req: a key fetch loads its pages in a
// Chrome it starts, and lending a login's browser starts one too, which a
// loaded machine took over half a minute to do. Every other request takes
// runRequestTimeout.
func requestTimeout(req runPipeRequest) time.Duration {
	if req.Kind == "browser" && req.Browser != nil && (req.Browser.Action == "fetch" || req.Browser.Action == "attach") {
		return browserFetchTimeout
	}
	return runRequestTimeout
}

// refuseRunRequests tells the board that run requests are not served, and why.
func (s *Service) refuseRunRequests(err error) {
	s.Store.mu.Lock()
	s.Store.issue(err.Error())
	saveErr := s.Store.save()
	s.Store.mu.Unlock()
	s.publish(errors.Join(err, saveErr))
}

// answerRunRequest answers one run request, line, which the process pid sent
// over a connection made at connected: with the reason it was refused, or
// nothing when the item is on the board. identified is why the sender's
// process could not be told, and readErr why its request could not be read.
func (s *Service) answerRunRequest(ctx context.Context, pid int, identified error, connected time.Time, line []byte, readErr error) pipeReply {
	var reply pipeReply
	var req runPipeRequest
	switch {
	case identified != nil:
		reply.Error = "the supervisor could not tell which process sent the request: " + identified.Error()
	case readErr != nil:
		reply.Error = "the run request was incomplete or over its size limit"
	case json.Unmarshal(line, &req) != nil:
		reply.Error = "the run request is not valid JSON"
	default:
		var err error
		ctx, cancel := context.WithTimeout(ctx, requestTimeout(req))
		switch req.Kind {
		case "reported":
			// A look takes no item, so it waits behind nobody's request.
			err = s.lookNow(ctx)
		case "recall":
			// A question of the memory index reads only the index, so any
			// process of this home may ask it and nobody's request waits.
			var result memory.RecallResult
			if req.Recall == nil {
				err = errors.New("a recall request names no question")
			} else if result, err = s.memory.recall(*req.Recall); err == nil {
				reply.Recall = &result
			}
		case "browser":
			// The broker takes its own requests one at a time, and a
			// browser's start waits behind nobody's item.
			var lease BrowserLease
			if req.Browser == nil {
				err = errors.New("a browser request names no browser")
			} else if err = goblinPipeCaller(s.Store.Home.State, req.Browser.Task, pid, connected); err != nil {
				err = fmt.Errorf("browser caller: %w", err)
			} else {
				switch req.Browser.Action {
				case "sign-in":
					lease, err = s.requestBrowserSignIn(*req.Browser)
				case "fetch":
					lease, err = s.fetchCredential(ctx, *req.Browser)
				default:
					lease, err = s.browsers.take(ctx, *req.Browser)
				}
				if err == nil && lease.Login != "" {
					reply.Browser = &lease
				}
			}
		case "credential-step":
			if req.Step == nil {
				err = errors.New("a card step names no card")
			} else {
				err = s.stepCredential(pid, connected, *req.Step)
			}
		default:
			s.runRequests.Lock()
			switch req.Kind {
			case "":
				err = s.acceptRunRequest(pid, connected, req)
			case "afk-on", "afk-off":
				err = s.switchAFK(ctx, pid, connected, req.Kind == "afk-on", req.Asked)
			case "afk-train-landed":
				// Any process of this home may ask: the supervisor logs
				// only what the train's record and GitHub say.
				if req.Train == nil {
					err = errors.New("a train landing names no train")
				} else {
					err = s.logTrainLanding(ctx, *req.Train)
				}
			case "helper":
				var started HelperStart
				if req.Helper == nil {
					err = errors.New("a helper request names no helper")
				} else if err = goblinPipeCaller(s.Store.Home.State, req.Helper.Parent, pid, connected); err != nil {
					err = fmt.Errorf("helper caller: %w", err)
				} else if started, err = s.acceptHelper(*req.Helper); err == nil {
					reply.Helper = &started
				}
			default:
				err = s.acceptCEOItem(pid, connected, req)
			}
			s.runRequests.Unlock()
		}
		cancel()
		switch {
		case err != nil:
			reply.Error = err.Error()
		case req.Kind != "reported" && req.Kind != "browser" && req.Kind != "recall":
			s.publish(nil)
		}
	}
	return reply
}

// lookNow has the supervisor's loop run a cycle now, which reads what a
// goblin just reported and tells every board, and returns once it has.
// Anyone who can reach the supervisor may ask: a look changes nothing.
func (s *Service) lookNow(ctx context.Context) error {
	looked := make(chan struct{})
	select {
	case s.looks <- looked:
	case <-ctx.Done():
		return errors.New("the supervisor's loop did not take the report in time")
	}
	select {
	case <-looked:
		return nil
	case <-ctx.Done():
		return errors.New("the supervisor's loop did not finish looking in time")
	}
}

// sendPipeRequest hands one request to the supervisor and returns the reason
// it was refused, if it was.
func sendPipeRequest(stateDir string, req runPipeRequest) error {
	_, err := askPipe(stateDir, req)
	return err
}

// exchangeRunRequest sends req over conn, a connection to this home's
// supervisor, and returns its answer, or the reason it was refused.
func exchangeRunRequest(conn interface {
	io.ReadWriter
	SetDeadline(time.Time) error
}, req runPipeRequest) (pipeReply, error) {
	// The answer may take as long as the request's work, and the margin
	// runReplyTimeout leaves past runRequestTimeout.
	answerWait := requestTimeout(req) + runReplyTimeout - runRequestTimeout
	lift := awake.Deadline(conn.SetDeadline, answerWait)
	defer lift()
	data, err := json.Marshal(req)
	if err != nil {
		return pipeReply{}, err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return pipeReply{}, fmt.Errorf("the supervisor did not take the run request: %w", err)
	}
	line, err := bufio.NewReader(io.LimitReader(conn, 64<<10)).ReadBytes('\n')
	var reply pipeReply
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return pipeReply{}, fmt.Errorf("the supervisor did not answer the run request within %s", answerWait)
	}
	if err != nil || json.Unmarshal(line, &reply) != nil {
		return pipeReply{}, errors.New("the supervisor gave no answer to the run request")
	}
	if reply.Error != "" {
		return reply, errors.New(reply.Error)
	}
	return reply, nil
}
