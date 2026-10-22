package awake_test

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/awake/awaketest"
)

// A paused process sees its clock run on by the pause as a paused machine
// does, its timers end late by it, and a timeout spends none of it: it ends
// once its own length has run outside the pause.
func TestARealPauseOfTheProcessSpendsNoneOfATimeout(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange
	const timeout = time.Second
	began := time.Now()
	ctx, cancel := awake.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Act
	awaketest.Ready()
	select {
	case <-ctx.Done():
	case <-time.After(time.Minute):
		t.Fatal("the timeout did not end within a minute")
	}
	waited := time.Since(began)

	// Assert
	if waited < awaketest.Length+timeout/2 {
		t.Errorf("a timeout of %s ended %s after it began across a pause of %s, so the pause spent it", timeout, waited, awaketest.Length)
	}
}
