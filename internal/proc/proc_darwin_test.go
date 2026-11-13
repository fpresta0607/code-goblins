package proc

import (
	"encoding/binary"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// An instance is the boot session's UUID and the kernel's unique ID of the
// process, with no clock in either: one process reads the same each time,
// and two processes read apart.
func TestAnInstanceIsTheBootSessionAndTheUniqueIDOfItsProcess(t *testing.T) {
	// Arrange
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || boot == "" {
		t.Fatalf("the boot session reads %q, %v", boot, err)
	}
	first, _ := startSleeper(t, t.TempDir())
	second, _ := startSleeper(t, t.TempDir())

	// Act
	instance, err := Instance(first.Process.Pid)
	again, againErr := Instance(first.Process.Pid)
	other, otherErr := Instance(second.Process.Pid)

	// Assert
	if err != nil || againErr != nil || otherErr != nil {
		t.Fatalf("Instance = %v, then %v, and of another child %v", err, againErr, otherErr)
	}
	if instance != again {
		t.Errorf("two readings of one child's instance gave %q and %q", instance, again)
	}
	if !strings.HasPrefix(instance, boot+"/") || !strings.HasPrefix(other, boot+"/") {
		t.Errorf("the instances %q and %q do not start with the boot session %q", instance, other, boot)
	}
	if instance == other {
		t.Errorf("two children read as the one instance %q", instance)
	}
	if strings.ContainsAny(instance, " \t\r\n") {
		t.Errorf("the instance %q holds a space, and a waiter's ticket parts its fields at one", instance)
	}
}

// The kernel stamps a process's start as it makes it, to the microsecond,
// so the start lies between the moment before the spawn and the moment
// after it.
func TestAChildsStartLiesBetweenTheMomentsBeforeAndAfterItsSpawn(t *testing.T) {
	// Arrange
	before := time.Now().Truncate(time.Microsecond)
	child, _ := startSleeper(t, t.TempDir())
	after := time.Now()

	// Act
	start, isFound := StartTime(child.Process.Pid)

	// Assert
	if !isFound {
		t.Fatal("the child's start could not be read")
	}
	if start.Before(before) || start.After(after) {
		t.Errorf("the child reads as started at %s, want between %s and %s", start.Format(time.RFC3339Nano), before.Format(time.RFC3339Nano), after.Format(time.RFC3339Nano))
	}
}

// startedWithRecord is what kern.procargs2 gives for a program started from
// path with arguments and environment, its path padded with zero bytes, and
// a string after an empty one, which is not the environment's.
func startedWithRecord(path string, arguments, environment []string) []byte {
	record := binary.NativeEndian.AppendUint32(nil, uint32(len(arguments)))
	record = append(record, path...)
	record = append(record, 0, 0, 0, 0)
	for _, value := range slices.Concat(arguments, environment, []string{"", "executable_path=" + path}) {
		record = append(record, value...)
		record = append(record, 0)
	}
	return record
}

func TestParseStartedWithReadsTheArgumentsAndTheEnvironment(t *testing.T) {
	tests := []struct {
		name            string
		record          []byte
		wantArguments   []string
		wantEnvironment []string
	}{
		{
			name:            "arguments and an environment",
			record:          startedWithRecord("/bin/sleep", []string{"sleep", "600"}, []string{"HOME=/Users/goblin", "GOBLINS_PROC_TEST=shown"}),
			wantArguments:   []string{"sleep", "600"},
			wantEnvironment: []string{"HOME=/Users/goblin", "GOBLINS_PROC_TEST=shown"},
		},
		{
			name:          "no environment",
			record:        startedWithRecord("/bin/sleep", []string{"sleep", "600"}, nil),
			wantArguments: []string{"sleep", "600"},
		},
		{
			name:            "an empty argument after the first",
			record:          startedWithRecord("/bin/echo", []string{"echo", "", "after"}, []string{"A=1"}),
			wantArguments:   []string{"echo", "", "after"},
			wantEnvironment: []string{"A=1"},
		},
		{
			name:            "an environment that runs to the end of the record",
			record:          append(binary.NativeEndian.AppendUint32(nil, 1), "/bin/true\x00\x00true\x00A=1\x00B=2"...),
			wantArguments:   []string{"true"},
			wantEnvironment: []string{"A=1", "B=2"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act
			arguments, environment, err := parseStartedWith(test.record)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(arguments, test.wantArguments) || !slices.Equal(environment, test.wantEnvironment) {
				t.Errorf("parseStartedWith = %q and %q, want %q and %q", arguments, environment, test.wantArguments, test.wantEnvironment)
			}
		})
	}
}

func TestParseStartedWithRefusesARecordCutShort(t *testing.T) {
	tests := map[string][]byte{
		"no count":            {1, 0},
		"no path's end":       append(binary.NativeEndian.AppendUint32(nil, 1), "/bin/sleep"...),
		"fewer arguments":     append(binary.NativeEndian.AppendUint32(nil, 3), "/bin/sleep\x00sleep\x00"...),
		"nothing but a count": binary.NativeEndian.AppendUint32(nil, 0),
	}
	for name, record := range tests {
		t.Run(name, func(t *testing.T) {
			// Act
			arguments, environment, err := parseStartedWith(record)

			// Assert
			if err == nil {
				t.Errorf("parseStartedWith = %q and %q, want a refusal", arguments, environment)
			}
		})
	}
}
