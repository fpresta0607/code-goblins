package release

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// WindowsSignature reads a file's Authenticode signature with Windows
// PowerShell's Get-AuthenticodeSignature, the reading the one-line install
// makes, so an update accepts exactly the signatures an install does,
// embedded or from a catalog. Windows PowerShell gets its own module path, as
// install.cmd gives it: one inherited from PowerShell 7 lists a Security
// module it cannot load, and the cmdlet then never runs. A reading that fails
// is an error, never an empty status taken for no signature.
func WindowsSignature(ctx context.Context, path string) (string, string, error) {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		return "", "", errors.New("SystemRoot is not set, so Windows PowerShell cannot be found")
	}
	shell := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	quoted := "'" + strings.ReplaceAll(path, "'", "''") + "'"
	script := "$ErrorActionPreference = 'Stop'; $s = Get-AuthenticodeSignature -LiteralPath " + quoted + "; $n = ''; if ($s.SignerCertificate) { $n = $s.SignerCertificate.GetNameInfo('SimpleName', $false) }; [Console]::Out.Write([string]$s.Status + \"`t\" + $n)"
	command := execx.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-Command", script)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "PSMODULEPATH=") {
			command.Env = append(command.Env, entry)
		}
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return "", "", fmt.Errorf("Get-AuthenticodeSignature failed: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	status, signer, found := strings.Cut(strings.TrimRight(string(output), "\r\n"), "\t")
	if !found || status == "" {
		return "", "", fmt.Errorf("Get-AuthenticodeSignature said nothing readable: %s", strings.TrimSpace(stderr.String()))
	}
	return status, signer, nil
}
