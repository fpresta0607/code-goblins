package fleettree

// Placed is one process an owner started, as one reading of the machine
// places it: in a job the owner's harness started after launching, or in a
// detached tree that is the owner's.
type Placed struct {
	Process
	// Owner is the task whose goblin started the process, or the terminal,
	// such as the CFO's, whose harness did.
	Owner string
	// Command is its command line, empty when it could not be read.
	Command string
	// Detached says it reaches its owner's harness through no parent.
	Detached bool
	// Service says its job is or runs a machine service, which works for
	// every goblin.
	Service bool
	// ByHarness says the harness started it itself: a tool call's shell, or
	// a hook.
	ByHarness bool
}

// Placed reads the machine once and places every process each of owners
// started, the way Read places a goblin's: each job its harness started
// after launching, with everything under it, and each detached tree that is
// the owner's. It reads no conversation and no gate run, so it is cheap to
// read every few seconds, and it shares Read's answers of whose a process is
// and what it runs. An owner whose harness does not run, or whose process
// id now names another program, has none.
func (r *Reader) Placed(owners []Goblin) ([]Placed, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	read := r.Processes
	if read == nil {
		read = Processes
	}
	processes, err := read()
	if err != nil {
		return nil, err
	}
	var placed []Placed
	for _, owner := range owners {
		if owner.HarnessPID == 0 {
			continue
		}
		harness, isRunning := readHarness(owner.HarnessPID, processes, HarnessLaunch)
		if !isRunning || !owner.HarnessStarted.IsZero() && harness.top.Started.Sub(owner.HarnessStarted).Abs() > startSlack {
			continue
		}
		for _, detached := range []bool{false, true} {
			jobs := harness.jobs
			if detached {
				jobs = r.detachedJobs(owner.Meta.ID, processes, harness)
			}
			for _, job := range jobs {
				facts := jobFacts{commands: map[int]string{}}
				for _, member := range job.members {
					facts.commands[member.PID] = r.command(member)
				}
				isService := job.isService(facts)
				for _, member := range job.members {
					placed = append(placed, Placed{Process: member, Owner: owner.Meta.ID, Command: facts.commands[member.PID], Detached: detached, Service: isService, ByHarness: !detached && member.ParentPID == harness.harness.PID})
				}
			}
		}
	}
	r.forgetEnded(processes)
	return placed, nil
}
