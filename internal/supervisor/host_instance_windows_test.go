package supervisor

import "testing"

// thisHostInstance is empty: host.Running on Windows tells a host by its
// process and start time, and a record names no instance there.
func thisHostInstance(*testing.T) string {
	return ""
}
