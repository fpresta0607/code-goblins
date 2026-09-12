package project

import (
	"context"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"testing"
)

type dr struct {
	codes []int
	i     int
}

func (d *dr) Run(context.Context, execx.Request) (execx.Result, error) {
	c := 0
	if d.i < len(d.codes) {
		c = d.codes[d.i]
	}
	d.i++
	return execx.Result{ExitCode: c}, nil
}
func TestDeployRequiredMissing(t *testing.T) {
	_, e := RunDeployment(context.Background(), &dr{}, Deployment{Required: true, Targets: []DeployTarget{{Name: "web", Command: Command{"x"}}}}, "", "api")
	if e == nil {
		t.Fatal("want error")
	}
}
func TestDeploySuccess(t *testing.T) {
	x, e := RunDeployment(context.Background(), &dr{}, Deployment{Required: true, Targets: []DeployTarget{{Name: "web", Command: Command{"x"}, Verify: Command{"v"}}}}, "", "")
	if e != nil || x[0].State != Healthy {
		t.Fatal(x, e)
	}
}
func TestDeployHealthFailure(t *testing.T) {
	x, e := RunDeployment(context.Background(), &dr{codes: []int{0, 1}}, Deployment{Required: true, Targets: []DeployTarget{{Name: "web", Command: Command{"x"}, Verify: Command{"v"}}}}, "", "")
	if e == nil || x[0].State != Unhealthy {
		t.Fatal(x, e)
	}
}
func TestDeployMulti(t *testing.T) {
	x, e := RunDeployment(context.Background(), &dr{}, Deployment{Required: true, Targets: []DeployTarget{{Name: "web", Command: Command{"x"}}, {Name: "api", Command: Command{"y"}}}}, "", "")
	if e != nil || len(x) != 2 {
		t.Fatal(x, e)
	}
}
