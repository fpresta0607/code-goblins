package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/fpresta0607/code-goblins/internal/boardweb"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

func runServe(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("serve", flag.ContinueOnError)
	f.SetOutput(stderr)
	address := f.String("listen", "127.0.0.1:4310", "loopback address for the native board")
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return 2
	}
	host, _, err := net.SplitHostPort(*address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		fmt.Fprintln(stderr, "serve requires a numeric loopback address, for example 127.0.0.1:4310")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	assets, err := boardweb.Assets()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer listener.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	config := watch.ConfigFromEnv(h)
	if config.Cleanup != nil {
		config.Cleanup()
	}
	config.WaitEvent = nil
	config.Cleanup = nil
	root, err := pipeline.DefaultRoot()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	s, err := supervisor.Start(ctx, h, supervisor.Options{
		Gate: pipeline.Reader{Root: root, Commands: execx.OSRunner{}},
		Send: func(ctx context.Context, id, text string) error { return runtime.sendText(ctx, h, id, text) },
		Peek: func(ctx context.Context, id string, lines int) (string, error) {
			return runtime.peek(ctx, h, id, lines)
		},
		Reconcile:      func(ctx context.Context) error { return watch.Reconcile(ctx, config) },
		VerifyDelivery: (supervisor.Git{}).VerifyDelivery,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer s.Close()
	server := &http.Server{Handler: supervisor.NewHTTP(s, listener.Addr().String(), assets), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		select {
		case <-ctx.Done():
		case <-s.Done():
		}
		shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = server.Shutdown(shutdownCtx)
	}()
	fmt.Fprintf(stdout, "CFO native board: http://%s\nSupervisor PID %d; browser-independent; Ctrl-C stops this process.\n", listener.Addr(), os.Getpid())
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
