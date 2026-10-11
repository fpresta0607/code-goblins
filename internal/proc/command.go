package proc

import (
	"regexp"
	"strings"
)

// secretArgument matches an argument that names what follows it as a secret.
var secretArgument = regexp.MustCompile(`(?i)(token|secret|passw|apikey|api[-_]key|credential|authorization|cookie|bearer)`)

// ShownCommand is a process's command as the CFO may read it: at most 160
// characters, with the value of an argument named as a secret, and a URL's
// query, left out. A command line can hold a credential, and what a sweep or
// a watch names is kept in its record and shown in a wake.
func ShownCommand(arguments []string) string {
	shown := make([]string, 0, len(arguments))
	hideNext := false
	for _, argument := range arguments {
		switch name, _, hasValue := strings.Cut(argument, "="); {
		case hideNext:
			argument, hideNext = "<hidden>", false
		case hasValue && secretArgument.MatchString(name):
			argument = name + "=<hidden>"
		case strings.HasPrefix(argument, "-") && secretArgument.MatchString(argument):
			hideNext = true
		case strings.Contains(argument, "://"):
			argument, _, _ = strings.Cut(argument, "?")
		}
		shown = append(shown, argument)
	}
	command := strings.Join(shown, " ")
	if len(command) > 160 {
		command = command[:157] + "..."
	}
	return command
}
