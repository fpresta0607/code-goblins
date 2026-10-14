package browserlogin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

type devToolsClient struct {
	ctx      context.Context
	cancel   context.CancelFunc
	root     string
	messages chan devToolsMessage
}

func (p *devToolsPipe) serveClient(w http.ResponseWriter, r *http.Request, target string) {
	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(devToolsMessageLimit)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	client := &devToolsClient{ctx: ctx, cancel: cancel, messages: make(chan devToolsMessage, 256)}
	var attached struct {
		Session string `json:"sessionId"`
	}
	ready, stop := context.WithTimeout(ctx, 5*time.Second)
	if target == "" {
		err = p.call(ready, "Target.attachToBrowserTarget", nil, &attached)
	} else {
		err = p.call(ready, "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true}, &attached)
	}
	stop()
	if err != nil || attached.Session == "" {
		return
	}
	client.root = attached.Session
	p.mu.Lock()
	p.sessions[client.root] = client
	p.mu.Unlock()
	defer func() {
		cancel()
		p.mu.Lock()
		for session, owner := range p.sessions {
			if owner == client {
				delete(p.sessions, session)
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
	}()
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
		pending := devToolsPending{id: message.ID, session: message.Session, method: message.Method, client: client}
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
