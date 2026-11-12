package main

import "github.com/fpresta0607/code-goblins/internal/conpty"

func serveScheduling() error {
	return conpty.KeepCurrentProcessInteractive()
}
