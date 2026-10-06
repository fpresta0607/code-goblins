package release

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// WindowsSignature reads a file's Authenticode signature with Windows
// PowerShell's Get-AuthenticodeSignature, the reading the one-line install
// makes, so an update accepts exactly the signatures an install does.
func WindowsSignature(ctx context.Context, path string) (string, string, error) {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		return "", "", errors.New("SystemRoot is not set, so Windows PowerShell cannot be found")
	}
	shell := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	quoted := "'" + strings.ReplaceAll(path, "'", "''") + "'"
	script := "$s = Get-AuthenticodeSignature -LiteralPath " + quoted + "; $n = ''; if ($s.SignerCertificate) { $n = $s.SignerCertificate.GetNameInfo('SimpleName', $false) }; [Console]::Out.Write([string]$s.Status + \"`t\" + $n)"
	output, err := execx.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return "", "", err
	}
	status, signer, found := strings.Cut(strings.TrimRight(string(output), "\r\n"), "\t")
	if !found {
		return "", "", errors.New("Get-AuthenticodeSignature said nothing readable")
	}
	return status, signer, nil
}
