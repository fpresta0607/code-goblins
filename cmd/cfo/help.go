package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

var helpWord = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func runHelp(args []string, stdout, stderr io.Writer, references fs.FS) int {
	for _, word := range args {
		if !helpWord.MatchString(word) {
			fmt.Fprintf(stderr, "cfo: unknown help topic %q; use cfo help for available topics\n", strings.Join(args, " "))
			return 2
		}
	}
	if len(args) == 0 {
		return listHelp(references, "docs", stdout, stderr)
	}
	directory, topic := "docs/commands", args
	if args[0] == "contract" {
		directory, topic = "docs/contract", args[1:]
	}
	if len(topic) == 0 {
		return listHelp(references, directory, stdout, stderr)
	}
	name := path.Join(directory, strings.Join(topic, "/"))
	data, err := fs.ReadFile(references, name+".md")
	if err == nil {
		if _, err := stdout.Write(data); err != nil {
			fmt.Fprintf(stderr, "cfo help: write %s: %v\n", name, err)
			return 1
		}
		return 0
	}
	if !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(stderr, "cfo help: read %s: %v\n", name, err)
		return 1
	}
	if entry, err := fs.Stat(references, name); err == nil && entry.IsDir() {
		return listHelp(references, name, stdout, stderr)
	}
	fmt.Fprintf(stderr, "cfo: unknown help topic %q; use cfo help for available topics\n", strings.Join(args, " "))
	return 2
}

func listHelp(references fs.FS, directory string, stdout, stderr io.Writer) int {
	var topics []string
	err := fs.WalkDir(references, directory, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !strings.HasSuffix(name, ".md") {
			return nil
		}
		topic := strings.TrimPrefix(name, "docs/")
		if strings.HasPrefix(topic, "commands/") || strings.HasPrefix(topic, "contract/") {
			topic = strings.TrimPrefix(topic, "commands/")
			topics = append(topics, strings.ReplaceAll(strings.TrimSuffix(topic, ".md"), "/", " "))
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(stderr, "cfo help: list %s: %v\n", directory, err)
		return 1
	}
	_, err = fmt.Fprintln(stdout, "usage: cfo help <topic>\n\nAvailable topics:\n  "+strings.Join(topics, "\n  "))
	if err != nil {
		fmt.Fprintf(stderr, "cfo help: write topics: %v\n", err)
		return 1
	}
	return 0
}
