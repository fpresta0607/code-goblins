package lock

// processStatus indicates whether a process is verifiably alive, dead, or unknown.
type processStatus int

const (
	statusDead    processStatus = iota // Process does not exist
	statusAlive                        // Process exists and is running
	statusUnknown                      // Cannot verify (fail closed: treat as alive)
)
