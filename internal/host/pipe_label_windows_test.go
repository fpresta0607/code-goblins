package host

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// A host's pipe carries a medium integrity label, so the board, which runs at
// medium integrity, still reaches a host Windows started elevated, as an
// administrator's run item's host is.
func TestAHostPipeIsLabeledMediumIntegrity(t *testing.T) {
	// Arrange
	name, err := pipeName()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	pipe, err := listen(name)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(pipe.waiting)
	descriptor, err := windows.GetSecurityInfo(pipe.waiting, windows.SE_KERNEL_OBJECT, windows.LABEL_SECURITY_INFORMATION)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if label := descriptor.String(); !strings.Contains(label, "(ML;;NW;;;ME)") {
		t.Errorf("the pipe's label is %q, want medium integrity, no write up", label)
	}
}
