package browserlogin

import "encoding/json"

// Event is a DevTools event a Client's browser sent: its method, its
// parameters and the page session it came from, empty for the browser's own.
type Event struct {
	Session string
	Method  string
	Params  json.RawMessage
}

// Command is a DevTools command a relayed client sent: its method, its
// parameters and the page target it acts on, empty for the browser itself.
type Command struct {
	Method string
	Target string
	Params json.RawMessage
}
