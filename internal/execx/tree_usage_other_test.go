//go:build !windows

package execx

import (
	"context"
	"testing"
)

func TestTreeAccountingRequiresWholeTreeSupport(t *testing.T) {
	tree := &Tree{}
	if _, err := tree.Usage(); err == nil {
		t.Fatal("unsupported accounting reported success")
	}
	if _, err := tree.TerminateAndWait(context.Background()); err == nil {
		t.Fatal("unsupported cleanup reported success")
	}
}
