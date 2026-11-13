package lock

import "errors"

// waitersSuffix names the file beside a lock that its waiters line up at.
const waitersSuffix = ".waiters"

// errWaitedOut reports that a waiter's place in the line did not come up
// within its wait.
var errWaitedOut = errors.New("lock: the wait ended in the line of waiters")
