package connections

import "golang.org/x/sys/windows/registry"

// userEnvironmentNames lists the names of the variables Windows gives every
// new process of this user, read as value names only from HKCU\Environment.
func userEnvironmentNames() ([]string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer key.Close()
	return key.ReadValueNames(0)
}
