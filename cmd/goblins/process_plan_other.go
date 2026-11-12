//go:build !windows

package main

import (
	"context"
	"errors"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func defaultProcessPlan(context.Context, home.Home) (processPlan, error) {
	return processPlan{}, errors.New("reading the machine's processes is supported on Windows")
}
