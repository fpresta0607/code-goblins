package conpty

import (
	"fmt"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake/awaketest"
)

// A machine that pauses stops the host with its console's input waker, and
// the wall clock runs on through the pause. A screen read held to the wall
// clock found its time gone once the machine ran again, though the waker had
// had no time to answer (#1247). The read waits across the pause, and the
// waker answers only once the machine runs again.
func TestAScreenReadWaitsForTheWakerAcrossAPauseOfTheWholeMachine(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: the waker answers the ask after the pause.
	w, asks, answers := standInWaker(t)
	go func() {
		if _, err := io.ReadFull(asks, make([]byte, 1)); err != nil {
			return
		}
		awaketest.ReadyThenHold()
		fmt.Fprintln(answers, `screen 1 {"rows":["ready"]}`)
	}()

	// Act
	rows, err := w.screen(time.Second)

	// Assert
	if err != nil || !slices.Equal(rows, []string{"ready"}) {
		t.Fatalf("the screen read = %q, %v across a pause of %s on a waker that answered after it; want the screen", rows, err, awaketest.Length)
	}
}
