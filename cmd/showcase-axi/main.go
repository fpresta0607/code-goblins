// Command showcase-axi is the repo-owned review surface: it serves an agent
// artifact (plan, report, markdown doc, wireframe, frontend mock, diff, CSV,
// or Mermaid diagram) on localhost so the user can read it, annotate it, and
// queue feedback the agent collects with poll.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/showcase"
)

// version is stamped by the release build:
//
//	go build -ldflags "-X main.version=v1.2.3" ./cmd/showcase-axi
var version = "dev"

const usage = `usage: showcase-axi <command> [args]

commands:
  showcase-axi <file> [--reopen]   open or resume a review session in the browser (.md, .html, .diff/.patch, .csv, .mmd)
  showcase-axi poll <file> [--agent-reply <message>]   wait for queued user feedback; prints JSON and exits on delivery
  showcase-axi end <file>          end the session (agent side)
  showcase-axi export <file> [--out <path>]   write one portable self-contained HTML file
  showcase-axi stop                stop the background server
  showcase-axi version             print the version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "showcase-axi %s\n", version)
		return 0
	case "serve":
		// Internal: the detached background server OpenSession starts.
		dir, err := showcase.RuntimeDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := showcase.NewServer(dir).Serve(); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "poll":
		return runPoll(args[1:], stdout, stderr)
	case "end":
		return runEnd(args[1:], stdout, stderr)
	case "export":
		return runExport(args[1:], stdout, stderr)
	case "stop":
		if err := showcase.StopServer(stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		return runOpen(args, stdout, stderr)
	}
}

// parseInterspersed accepts flags before or after positional arguments,
// matching the documented `export <file> [--out <path>]` calling shape.
func parseInterspersed(flags *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for len(args) > 0 {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}
		args = flags.Args()
		if len(args) > 0 {
			positional = append(positional, args[0])
			args = args[1:]
		}
	}
	return positional, nil
}

func runOpen(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("open", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reopen := flags.Bool("reopen", false, "resume a session the reviewer ended from the browser")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	_, err = showcase.OpenSession(context.Background(), positional[0], *reopen, stdout)
	if errors.Is(err, showcase.ErrEndedByUser) {
		fmt.Fprintf(stderr, "the reviewer ended this session from the browser; rerun with --reopen only if they asked for another look\n")
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runPoll(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("poll", flag.ContinueOnError)
	flags.SetOutput(stderr)
	agentReply := flags.String("agent-reply", "", "post a message to the conversation panel before waiting")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	artifact := positional[0]
	if abs, err := filepath.Abs(artifact); err == nil {
		artifact = abs
	}
	if *agentReply != "" {
		if err := showcase.AppendMessage(artifact, "agent", *agentReply); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if err := showcase.Poll(context.Background(), artifact, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runEnd(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	artifact, err := filepath.Abs(args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := showcase.End(artifact, "agent"); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "session ended for %s\n", args[0])
	return 0
}

func runExport(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	out := flags.String("out", "", "output path (default: <artifact>-export.html beside the artifact)")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	path, err := showcase.Export(positional[0], *out)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "exported %s\n", path)
	return 0
}
