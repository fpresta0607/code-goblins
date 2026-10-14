package main

import (
	"flag"
	"fmt"
)

// parseFlags parses args into f, the flags of a command that takes no
// further argument, and refuses a word left over with its usage. Such a word
// is usually a value given without its flag, or one with spaces left
// unquoted: cfo answer <task-id> "<option>" was refused with exit 2 and
// nothing said, which read as an answer given (#851).
func parseFlags(f *flag.FlagSet, args []string) bool {
	if err := f.Parse(args); err != nil {
		return false
	}
	if f.NArg() == 0 {
		return true
	}
	hasFlags := false
	f.VisitAll(func(*flag.Flag) { hasFlags = true })
	if !hasFlags {
		fmt.Fprintf(f.Output(), "cfo %s: unexpected argument %q: it takes no argument\n", f.Name(), f.Arg(0))
		return false
	}
	fmt.Fprintf(f.Output(), "cfo %s: unexpected argument %q: each value follows its flag, quoted when it holds spaces\n", f.Name(), f.Arg(0))
	f.Usage()
	return false
}
