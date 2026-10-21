package services

import "context"

// Compose names one compose stack: the checkout it runs from, its compose
// file and the env file compose reads, both relative to the checkout.
type Compose struct {
	Dir     string
	File    string
	EnvFile string
}

// Failure is a container of a stack that did not come up, as it says of
// itself.
type Failure struct {
	Service   string
	Container string
	// State is the container's state as docker inspect reads it, its health
	// check's last results included.
	State string
	// Log is the end of the container's log.
	Log string
}

// Docker is what the services need of the Docker engine and compose.
type Docker interface {
	// EngineRunning reports whether the engine answers.
	EngineRunning(ctx context.Context) (bool, error)
	StartEngine(ctx context.Context) error
	StopEngine(ctx context.Context) error
	// RunningContainers names every container running on the engine.
	RunningContainers(ctx context.Context) ([]string, error)
	// Running names the services of the compose stack that run.
	Running(ctx context.Context, compose Compose) ([]string, error)
	// Up starts services, and what they depend on, and waits for them to
	// run, or to be healthy where they declare a health check.
	Up(ctx context.Context, compose Compose, services []string) error
	// Failed reads each container of the stack that ran and stopped, keeps
	// restarting, or runs without passing its health check.
	Failed(ctx context.Context, compose Compose) ([]Failure, error)
	// Down stops and removes services, or the whole stack when none are
	// named.
	Down(ctx context.Context, compose Compose, services []string) error
	// Relieve hands the file cache of the engine's virtual machine back to
	// the machine.
	Relieve(ctx context.Context) error
}
