package showcase

import (
	"context"
	"encoding/json"
	"io"
	"time"
)

// Poll blocks until queued reviewer feedback or the session end is
// deliverable, prints it as one JSON payload, and returns. Delivered items
// are marked in the session file, so a killed or restarted poll never loses
// or repeats them. Poll stays silent while there is nothing to deliver.
func Poll(ctx context.Context, artifact string, out io.Writer) error {
	for {
		payload, delivered, err := Consume(artifact)
		if err != nil {
			return err
		}
		if delivered {
			return json.NewEncoder(out).Encode(payload)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
