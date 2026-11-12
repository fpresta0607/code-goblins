package browserlogin

import "encoding/json"

// Event is a DevTools event a Client's browser sent: its method, its
// parameters and the page session it came from, empty for the browser's own.
type Event struct {
	Session string
	Method  string
	Params  json.RawMessage
}
