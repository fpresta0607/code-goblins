package browserlogin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

const devToolsMessageLimit = 32 << 20

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
	reader   io.ReadCloser
	writer   io.WriteCloser
	writeMu  sync.Mutex
	mu       sync.Mutex
	next     int64
	pending  map[int64]devToolsPending
	sessions map[string]*devToolsClient
	done     chan struct{}
	once     sync.Once
}

func newDevToolsPipe(reader io.ReadCloser, writer io.WriteCloser) *devToolsPipe {
	pipe := &devToolsPipe{reader: reader, writer: writer, pending: map[int64]devToolsPending{}, sessions: map[string]*devToolsClient{}, done: make(chan struct{})}
	go pipe.read()
	return pipe
}

func (p *devToolsPipe) close() {
	p.once.Do(func() {
		close(p.done)
		p.reader.Close()
		p.writer.Close()
	})
}

func (p *devToolsPipe) send(ctx context.Context, message devToolsMessage, pending devToolsPending) (int64, error) {
	stop := context.AfterFunc(ctx, p.close)
	defer stop()
	p.mu.Lock()
	p.next++
	message.ID = p.next
	p.pending[message.ID] = pending
	p.mu.Unlock()
	data, err := json.Marshal(message)
	if err == nil {
		p.writeMu.Lock()
		err = ctx.Err()
		if err == nil {
			_, err = p.writer.Write(append(data, 0))
		}
		p.writeMu.Unlock()
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
		return errors.New("the private browser pipe closed")
	}
}

func (p *devToolsPipe) read() {
	defer p.close()
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
			return
		}
		p.deliver(message)
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
