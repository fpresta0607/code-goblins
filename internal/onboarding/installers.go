package onboarding

// Installer is how one agent is installed, the same way install.ps1 installs
// it.
type Installer struct {
	// Kind is "powershell" for the vendor's own install script, saved to a
	// file and run from it, or "npm" for a global npm package.
	Kind string
	// Source is the script's address or the package's name.
	Source string
}

// installers are each agent's installer. An npm package is pinned to the
// version install.ps1 pins, so a release upstream reaches users only when it
// is pinned there and here; a test holds the two to the same version.
var installers = map[string]Installer{
	// The native build, claude.exe, which a native terminal can start; npm's
	// package installs a script it cannot.
	"claude": {Kind: "powershell", Source: "https://claude.ai/install.ps1"},
	"codex":  {Kind: "npm", Source: "@openai/codex@0.160.1"},
	"pi":     {Kind: "npm", Source: "@earendil-works/pi-coding-agent@1.0.4"},
}

// InstallerFor is the installer of the agent named id, and false for a name
// that is no agent.
func InstallerFor(id string) (Installer, bool) {
	installer, ok := installers[id]
	return installer, ok
}

// Describe says what the installer runs, for the screen that asks before
// running it.
func (i Installer) Describe() string {
	if i.Kind == "npm" {
		return "npm install -g " + i.Source
	}
	return "the installer at " + i.Source
}
