package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/taskcontext"
)

func runContext(args []string, out, errs io.Writer, runtime commandRuntime) int {
	if len(args) != 1 {
		fmt.Fprintln(errs, "cfo context <id>")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	m, err := taskcontext.Refresh(context.Background(), h, args[0], execx.OSRunner{})
	if err == nil {
		err = json.NewEncoder(out).Encode(m)
	}
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	return 0
}

func runRecap(args []string, out, errs io.Writer, runtime commandRuntime) int {
	if len(args) != 3 || args[1] != "--file" {
		fmt.Fprintln(errs, "cfo recap <id> --file <saved.html>")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	m, err := taskcontext.Refresh(context.Background(), h, args[0], execx.OSRunner{})
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	if !strings.EqualFold(filepath.Ext(args[2]), ".html") {
		fmt.Fprintln(errs, "recap: HTML file required")
		return 2
	}
	data, err := os.ReadFile(args[2])
	if err == nil && len(data) == 0 {
		err = fmt.Errorf("recap: empty artifact")
	}
	if err == nil {
		err = exportRecap(context.Background(), execx.OSRunner{}, args[2], m.Recap)
	}
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	fmt.Fprintf(out, "Saved recap: %s\nOpen with: lavish-axi %q\n", m.Recap, m.Recap)
	return 0
}

// Lavish owns portable asset inlining. CFO only selects the durable task path.
func exportRecap(ctx context.Context, commands execx.Runner, source, dest string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	cli := filepath.Join(os.Getenv("APPDATA"), "npm", "node_modules", "lavish-axi", "dist", "cli.mjs")
	if _, err := os.Stat(cli); err != nil {
		return fmt.Errorf("recap: installed Lavish export entry point unavailable: %w", err)
	}
	staging, err := os.CreateTemp(filepath.Dir(dest), "recap-export-*.html")
	if err != nil {
		return err
	}
	stagedPath := staging.Name()
	if err = staging.Close(); err != nil {
		return err
	}
	defer os.Remove(stagedPath)
	result, err := commands.Run(ctx, execx.Request{Name: "node", Args: []string{cli, "export", source, "--out", stagedPath}})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("recap: Lavish portable export failed: %s", strings.TrimSpace(string(result.Stderr)))
	}
	data, err := os.ReadFile(stagedPath)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("recap: Lavish produced an empty export")
	}
	return fsx.AtomicWriteFile(dest, data)
}
