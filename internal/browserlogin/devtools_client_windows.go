package browserlogin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type devToolsClient struct {
	ctx      context.Context
	cancel   context.CancelFunc
	root     string
	messages chan devToolsMessage
}

// refusalCode is the DevTools error code a refused command is answered with,
// the protocol's own for a command the browser could not run.
const refusalCode = -32000

// attachClient attaches a client of the pipe's to target, or to the browser
// itself when target is empty, and routes that session's messages to it.
func (p *devToolsPipe) attachClient(ctx context.Context, cancel context.CancelFunc, target string) (*devToolsClient, error) {
	client := &devToolsClient{ctx: ctx, cancel: cancel, messages: make(chan devToolsMessage, 256)}
	var attached struct {
		Session string `json:"sessionId"`
	}
	ready, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	var err error
	if target == "" {
		err = p.call(ready, "Target.attachToBrowserTarget", nil, &attached)
	} else {
		err = p.call(ready, "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true}, &attached)
	}
	if err == nil && attached.Session == "" {
		err = errors.New("the browser named no session for the client")
	}
	if err != nil {
		return nil, err
	}
	client.root = attached.Session
	p.mu.Lock()
	p.sessions[client.root] = client
	p.mu.Unlock()
	return client, nil
}

// detachClient forgets client's sessions and commands and detaches its root
// session from the browser.
func (p *devToolsPipe) detachClient(client *devToolsClient) {
	client.cancel()
	p.mu.Lock()
	for session, owner := range p.sessions {
		if owner == client {
			delete(p.sessions, session)
			delete(p.targets, session)
		}
	}
	for id, pending := range p.pending {
		if pending.client == client {
			delete(p.pending, id)
		}
	}
	p.mu.Unlock()
	closed, finish := context.WithTimeout(context.Background(), 2*time.Second)
	defer finish()
	_ = p.call(closed, "Target.detachFromTarget", map[string]string{"sessionId": client.root}, nil)
}

// serveClient relays one WebSocket client to target. Each command the
// client sends is first heard, with the page it acts on: one hear gives a
// reason for is answered with that reason as the browser's refusal and never
// reaches the browser; a nil hear passes every command on.
func (p *devToolsPipe) serveClient(w http.ResponseWriter, r *http.Request, target string, hear func(Command) string) {
	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(devToolsMessageLimit)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	client, err := p.attachClient(ctx, cancel, target)
	if err != nil {
		return
	}
	defer p.detachClient(client)
	written := make(chan struct{})
	go func() {
		defer close(written)
		defer connection.CloseNow()
		for {
			select {
			case message := <-client.messages:
				data, err := json.Marshal(message)
				if err != nil || connection.Write(ctx, websocket.MessageText, data) != nil {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			case <-p.done:
				cancel()
				return
			}
		}
	}()
	defer func() { cancel(); <-written }()
	for {
		kind, data, err := connection.Read(ctx)
		if err != nil || kind != websocket.MessageText {
			return
		}
		var message devToolsMessage
		if json.Unmarshal(data, &message) != nil || message.ID <= 0 || message.Method == "" {
			return
		}
		if hear != nil {
			p.mu.Lock()
			page := p.targets[message.Session]
			p.mu.Unlock()
			if reason := hear(Command{Method: message.Method, Target: page, Params: message.Params}); reason != "" {
				refusal, _ := json.Marshal(map[string]any{"code": refusalCode, "message": reason})
				select {
				case client.messages <- devToolsMessage{ID: message.ID, Session: message.Session, Error: refusal}:
				case <-ctx.Done():
					return
				}
				continue
			}
		}
		pending := devToolsPending{id: message.ID, session: message.Session, method: message.Method, client: client}
		if message.Method == "Target.attachToTarget" {
			var attach struct {
				Target string `json:"targetId"`
			}
			if json.Unmarshal(message.Params, &attach) == nil {
				pending.target = attach.Target
			}
		}
		if message.Session == "" {
			message.Session = client.root
		} else {
			p.mu.Lock()
			isOwned := p.sessions[message.Session] == client
			p.mu.Unlock()
			if !isOwned {
				return
			}
		}
		if _, err := p.send(ctx, message, pending); err != nil {
			return
		}
	}
}

// Client is a DevTools connection of this process's own to a browser it
// started, attached to the browser itself, which reaches the browser's pages
// by attaching to them with Target.attachToTarget and flatten. Its events
// arrive on Events, which the caller reads promptly: a client whose events
// back up is closed, as a WebSocket client that falls behind is.
type Client struct {
	pipe   *devToolsPipe
	client *devToolsClient
	events chan Event
	mu     sync.Mutex
	next   int64
	waits  map[int64]chan devToolsMessage
	done   chan struct{}
}

// Open attaches a Client to process's browser.
func (process Process) Open(ctx context.Context) (*Client, error) {
	return openClient(ctx, process.job.transport.pipe)
}

func openClient(ctx context.Context, pipe *devToolsPipe) (*Client, error) {
	clientContext, cancel := context.WithCancel(context.Background())
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	attached, err := pipe.attachClient(clientContext, cancel, "")
	if err != nil {
		cancel()
		return nil, err
	}
	c := &Client{pipe: pipe, client: attached, events: make(chan Event, 256), waits: map[int64]chan devToolsMessage{}, done: make(chan struct{})}
	go c.read()
	return c, nil
}

func (c *Client) read() {
	defer close(c.done)
	defer close(c.events)
	for {
		select {
		case message := <-c.client.messages:
			if message.ID != 0 {
				c.mu.Lock()
				wait := c.waits[message.ID]
				delete(c.waits, message.ID)
				c.mu.Unlock()
				if wait != nil {
					wait <- message
				}
				continue
			}
			select {
			case c.events <- Event{Session: message.Session, Method: message.Method, Params: message.Params}:
			default:
				c.client.cancel()
				return
			}
		case <-c.client.ctx.Done():
			return
		case <-c.pipe.done:
			c.client.cancel()
			return
		}
	}
}

// Events are the browser's events to this client, closed once it closes.
func (c *Client) Events() <-chan Event {
	return c.events
}

// Call runs method in session, the browser's own when it is empty, and
// decodes its result into result, which may be nil.
func (c *Client) Call(ctx context.Context, session, method string, params, result any) error {
	var data json.RawMessage
	if params != nil {
		var err error
		if data, err = json.Marshal(params); err != nil {
			return err
		}
	}
	answer := make(chan devToolsMessage, 1)
	c.mu.Lock()
	c.next++
	id := c.next
	c.waits[id] = answer
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.waits, id); c.mu.Unlock() }()
	message := devToolsMessage{Method: method, Params: data, Session: session}
	if session == "" {
		message.Session = c.client.root
	} else {
		c.pipe.mu.Lock()
		isOwned := c.pipe.sessions[session] == c.client
		c.pipe.mu.Unlock()
		if !isOwned {
			return errors.New(method + ": the session is not this client's")
		}
	}
	if _, err := c.pipe.send(ctx, message, devToolsPending{id: id, session: session, method: method, client: c.client}); err != nil {
		return err
	}
	select {
	case reply := <-answer:
		if len(reply.Error) != 0 {
			var refusal struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(reply.Error, &refusal)
			return errors.New(method + ": the browser refused it: " + refusal.Message)
		}
		if result != nil && len(reply.Result) != 0 {
			return json.Unmarshal(reply.Result, result)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errors.New(method + ": the browser connection closed")
	}
}

// Close detaches the client from the browser.
func (c *Client) Close() {
	c.pipe.detachClient(c.client)
	<-c.done
}
