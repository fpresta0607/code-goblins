package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func runSupervisor(args []string, out, errs io.Writer, runtime commandRuntime) int {
	if len(args) < 1 || len(args) > 2 {
		fmt.Fprintln(errs, "cfo supervisor: run, start, stop, status, register <session:pane>, confirm-delivery <sequence>")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if (args[0] == "register" || args[0] == "confirm-delivery") != (len(args) == 2) {
		fmt.Fprintln(errs, "cfo supervisor: wrong argument count")
		return 2
	}
	switch args[0] {
	case "register":
		var target herdr.Target
		target, err = herdr.ParseTarget(args[1])
		if err == nil {
			err = supervisor.Register(ctx, h.State, target, &herdr.Client{Commands: execx.OSRunner{}, Session: target.Session})
		}
		if err == nil {
			err = supervisor.Start(ctx, h)
		}
	case "confirm-delivery":
		var seq int
		seq, err = strconv.Atoi(args[1])
		if err == nil {
			err = supervisor.ConfirmDelivery(h.State, seq)
		}
	case "run":
		err = (supervisor.Service{Home: h, OnEvent: func(reason string) { fmt.Fprintln(out, reason) }, Deliver: func(ctx context.Context) error {
			primary, e := supervisor.ReadPrimary(h.State)
			client := &herdr.Client{Commands: execx.OSRunner{}, Session: primary.Target.Session}
			if e != nil {
				client.Session = "unregistered"
			}
			return supervisor.DeliverPending(ctx, h.State, client, func(ctx context.Context, target herdr.Target, message string) error {
				return (fleet.Sender{Resolve: fleet.Resolver{StateDir: h.State}, Herdr: client, RequireAgent: true}).Text(ctx, target.String(), message)
			})
		}}).Run(ctx)
	case "start":
		err = supervisor.Start(ctx, h)
	case "stop":
		err = supervisor.Stop(h.State)
	case "status":
		status := supervisor.Status(h.State)
		if err := json.NewEncoder(out).Encode(status); err != nil {
			fmt.Fprintln(errs, err)
			return 1
		}
		if !status.Healthy {
			return 1
		}
		return 0
	default:
		fmt.Fprintln(errs, "cfo supervisor: unknown command")
		return 2
	}
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	return 0
}

func rearmRegistered(ctx context.Context, h home.Home, out io.Writer) {
	if _, err := supervisor.ReadPrimary(h.State); err != nil {
		fmt.Fprintln(out, "SUPERVISION UNREGISTERED: run cfo session-start --primary <session:pane> in the primary harness; restored tabs are not registration")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	if err := supervisor.Start(ctx, h); err != nil {
		fmt.Fprintf(out, "SUPERVISION DEGRADED: %v\n", err)
		return
	}
	status := supervisor.Status(h.State)
	fmt.Fprintf(out, "SUPERVISION: observing=%t delivery=%s; %s\n", status.Observing, status.Reason, status.Action)
}
