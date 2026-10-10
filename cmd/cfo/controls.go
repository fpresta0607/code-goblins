package main

import (
	"os"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/priority"
)

// controls are the commands that are the fleet's own small work: each is a
// conversation with a terminal's host, the supervisor or the home's own
// records, or a reading of the machine, that is over in moments, or serves
// or waits on those conversations for as long as it runs, as serve, attach,
// watch and a hook that holds a turn do, and an update, whose every step
// has a minute. Every other command starts or waits on work, or is not one
// anything waits on under a bound, and stays at normal priority. A
// terminal's host is not here because it raises itself, whoever starts it
// (host.Run).
var controls = map[string]bool{
	"serve": true, "status": true, "stop": true, "attach": true, "update": true, "process-plan": true,
	"send": true, "answer": true, "peek": true, "fleet-view": true,
	"drain": true, "watch": true, "notify": true, "question": true, "register": true,
	"pause": true, "resume": true, "kill": true,
	"hook": true, "native-hook": true, "session-start": true,
}

// aboveTheWork raises this command one priority class above normal when it
// is one of the fleet's controls, and returns what gives the class back. A
// control ran at the priority of the builds and tests it supervises, so on a
// machine with every core busy it waited its turn behind them: on 2026-10-10
// cfo send twice got no answer to its handshake in five seconds, and an
// update's prepare step, which writes five copies of a program, came within
// seconds of its minute. Beside sixteen busy threads one such copy took 3.5
// seconds at normal priority and 0.07 raised, and a send's own work 107
// milliseconds against 2. What a goblin or a gate agent starts is their work
// and is never raised, and neither is anything a raised command starts,
// which Windows gives the normal class.
func aboveTheWork(args []string) (restore func()) {
	if len(args) == 0 || !controls[args[0]] || os.Getenv(harness.RoleVariable) == harness.RoleGoblin || os.Getenv(gateAgentVariable) != "" {
		return func() {}
	}
	return priority.AboveTheWork()
}
