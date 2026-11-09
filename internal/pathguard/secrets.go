package pathguard

import "path/filepath"

// The names a refusal gives the folders that hold secrets.
const (
	CredentialStoreName = "the credential store"
	SSHName             = "~/.ssh"
)

// Secrets are the folders of the user whose profile is userHome that hold
// the credential store or an ssh key: the credential store's file store,
// fileStore (auth.FileStoreRoot), the folders Windows keeps Credential
// Manager's entries and the keys that open them in, under appData and
// localAppData, and ~/.ssh. A folder an empty variable would name is left
// out rather than made relative.
func Secrets(userHome, appData, localAppData, fileStore string) []Secret {
	var secrets []Secret
	add := func(base, name string, parts ...string) {
		if base != "" {
			secrets = append(secrets, Secret{Path: filepath.Join(append([]string{base}, parts...)...), Name: name})
		}
	}
	add(fileStore, CredentialStoreName)
	add(appData, CredentialStoreName, "Microsoft", "Credentials")
	add(appData, CredentialStoreName, "Microsoft", "Protect")
	add(appData, CredentialStoreName, "Microsoft", "Vault")
	add(localAppData, CredentialStoreName, "Microsoft", "Credentials")
	add(localAppData, CredentialStoreName, "Microsoft", "Vault")
	add(userHome, SSHName, ".ssh")
	return secrets
}
