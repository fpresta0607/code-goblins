package browserlogin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

const devToolsMessageLimit = 32 << 20

// errPipeClosed is a call's answer when the browser closed its end of the
// pipe before it answered, as a browser asked to close may.
var errPipeClosed = errors.New("the private browser pipe closed")

type devToolsMessage struct {
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Session string          `json:"sessionId,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type devToolsPending struct {
	id      int64
	session string
	method  string
	client  *devToolsClient
	answer  chan devToolsMessage
}

// devToolsPipe multiplexes private commands and each attached client's
// sessions over Chrome's inherited, zero-delimited debugging pipe.
type devToolsPipe struct {
	reader io.ReadCloser
	writer io.WriteCloser
	// writing holds the turn to write, one command at a time, which a
	// caller waiting for it can give up when its context ends.
	writing  chan struct{}
	mu       sync.Mutex
	next     int64
	pending  map[int64]devToolsPending
	sessions map[string]*devToolsClient
	done     chan struct{}
	once     sync.Once
	// why is what closed the pipe, set before done is closed.
	why error
}

func newDevToolsPipe(reader io.ReadCloser, writer io.WriteCloser) *devToolsPipe {
	pipe := &devToolsPipe{reader: reader, writer: writer, pending: map[int64]devToolsPending{}, sessions: map[string]*devToolsClient{}, done: make(chan struct{}), writing: make(chan struct{}, 1)}
	go pipe.read()
	return pipe
}

func (p *devToolsPipe) close() {
	p.closeFor(errors.New("the bridge closed it"))
}

// closeFor closes the pipe once, for why, which every caller that then finds
// it closed is told, so a request the bridge refuses for it says what ended
// the browser's connection.
func (p *devToolsPipe) closeFor(why error) {
	p.once.Do(func() {
		p.why = why
		close(p.done)
		p.reader.Close()
		p.writer.Close()
	})
}

// send writes message for a caller whose context is ctx. A caller that gives
// up before its write, or after it, leaves the pipe to the others: it is the
// browser's one connection, which every request of the bridge and every
// client shares, and closing it for one caller answered every later request
// with a 503 (#1020). Only a cancel that cuts into the write closes it, since
// what the browser read of a frame cut short would garble every frame after.
func (p *devToolsPipe) send(ctx context.Context, message devToolsMessage, pending devToolsPending) (int64, error) {
	p.mu.Lock()
	p.next++
	message.ID = p.next
	p.pending[message.ID] = pending
	p.mu.Unlock()
	data, err := json.Marshal(message)
	if err == nil {
		select {
		case p.writing <- struct{}{}:
			err = ctx.Err()
			if err == nil {
				stop := context.AfterFunc(ctx, func() { p.closeFor(errors.New("a write to it was cut short")) })
				_, err = p.writer.Write(append(data, 0))
				stop()
			}
			select {
			case <-p.done:
				err = p.closedError()
			default:
			}
			<-p.writing
		case <-ctx.Done():
			err = ctx.Err()
		case <-p.done:
			err = p.closedError()
		}
	}
	if err != nil {
		p.mu.Lock()
		delete(p.pending, message.ID)
		p.mu.Unlock()
	}
	return message.ID, err
}

func (p *devToolsPipe) call(ctx context.Context, method string, params, result any) error {
	var data json.RawMessage
	if params != nil {
		var err error
		data, err = json.Marshal(params)
		if err != nil {
			return err
		}
	}
	answer := make(chan devToolsMessage, 1)
	id, err := p.send(ctx, devToolsMessage{Method: method, Params: data}, devToolsPending{answer: answer, method: method})
	if err != nil {
		return err
	}
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	select {
	case reply := <-answer:
		if len(reply.Error) != 0 {
			return errors.New("the browser refused " + method)
		}
		body := bytes.TrimSpace(reply.Result)
		if len(body) == 0 || body[0] != '{' {
			return errors.New("the browser reply has no result object")
		}
		if result != nil {
			return json.Unmarshal(reply.Result, result)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return p.closedError()
	}
}

// closedError is what a caller that found the pipe closed is told.
func (p *devToolsPipe) closedError() error {
	return fmt.Errorf("%w: %w", errPipeClosed, p.why)
}

func (p *devToolsPipe) read() {
	why := errors.New("the browser closed its end")
	defer func() { p.closeFor(why) }()
	scanner := bufio.NewScanner(p.reader)
	scanner.Buffer(make([]byte, 4096), devToolsMessageLimit)
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if index := bytes.IndexByte(data, 0); index >= 0 {
			return index + 1, data[:index], nil
		}
		if atEOF && len(data) > 0 {
			return 0, nil, io.ErrUnexpectedEOF
		}
		return 0, nil, nil
	})
	for scanner.Scan() {
		var message devToolsMessage
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			why = errors.New("the browser sent a message that is not JSON")
			return
		}
		p.deliver(message)
	}
	if err := scanner.Err(); err != nil {
		why = fmt.Errorf("reading the browser: %w", err)
	}
}

func (p *devToolsPipe) deliver(message devToolsMessage) {
	p.mu.Lock()
	var client *devToolsClient
	if message.ID != 0 {
		pending, ok := p.pending[message.ID]
		delete(p.pending, message.ID)
		if !ok {
			p.mu.Unlock()
			return
		}
		if pending.answer != nil {
			p.mu.Unlock()
			pending.answer <- message
			return
		}
		client = pending.client
		message.ID, message.Session = pending.id, pending.session
		if pending.method == "Target.attachToTarget" || pending.method == "Target.attachToBrowserTarget" {
			var attached struct {
				Session string `json:"sessionId"`
			}
			if json.Unmarshal(message.Result, &attached) == nil && attached.Session != "" {
				p.sessions[attached.Session] = client
			}
		}
	} else {
		client = p.sessions[message.Session]
		if client != nil {
			var target struct {
				Session string `json:"sessionId"`
			}
			if json.Unmarshal(message.Params, &target) == nil && target.Session != "" {
				if message.Method == "Target.attachedToTarget" {
					p.sessions[target.Session] = client
				}
				if message.Method == "Target.detachedFromTarget" {
					delete(p.sessions, target.Session)
				}
			}
			if message.Session == client.root {
				message.Session = ""
			}
		}
	}
	p.mu.Unlock()
	if client != nil {
		select {
		case client.messages <- message:
		case <-client.ctx.Done():
		default:
			client.cancel()
		}
	}
}
