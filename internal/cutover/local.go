package cutover

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/githubapi"
	"github.com/fpresta0607/code-goblins/internal/installbase"
)

// OldSettings are the home and the projects root a build before the rename
// set in the user's settings, each empty where it set none.
func OldSettings(env installbase.EnvStore) (root, projects string, err error) {
	prefix := strings.ToUpper(oldName) + "_"
	root, _, err = env.Get(prefix + "HOME")
	if err != nil {
		return "", "", err
	}
	projects, _, err = env.Get(prefix + "PROJECTS_ROOT")
	return root, projects, err
}

// TaskScheduler is Windows' Task Scheduler, read and written through
// PowerShell, each job's definition carried in base64 so no quoting can break
// it. Windows' own jobs, under \Microsoft\, are never read.
type TaskScheduler struct {
	Commands execx.Runner
}

func (s TaskScheduler) Jobs() ([]Job, error) {
	const script = `$jobs = @(Get-ScheduledTask | Where-Object { $_.TaskPath -notlike '\Microsoft\*' } | ForEach-Object {
  [pscustomobject]@{ Name = $_.TaskPath + $_.TaskName; XML = [string](Export-ScheduledTask -TaskName $_.TaskName -TaskPath $_.TaskPath) }
})
[Console]::Out.Write([Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes((ConvertTo-Json -InputObject $jobs -Compress -Depth 2))))`
	out, err := s.run(script)
	if err != nil {
		return nil, err
	}
	text, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		return nil, fmt.Errorf("read the scheduled jobs: %w", err)
	}
	var jobs []Job
	if err := json.Unmarshal(text, &jobs); err != nil {
		return nil, fmt.Errorf("read the scheduled jobs: %w", err)
	}
	return jobs, nil
}

func (s TaskScheduler) Register(job Job) error {
	folder, name := splitJobName(job.Name)
	_, err := s.run(fmt.Sprintf(`$xml = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))
$null = Register-ScheduledTask -TaskName '%s' -TaskPath '%s' -Xml $xml -Force -ErrorAction Stop`, base64.StdEncoding.EncodeToString([]byte(job.XML)), quoted(name), quoted(folder)))
	return err
}

func (s TaskScheduler) Delete(fullName string) error {
	folder, name := splitJobName(fullName)
	_, err := s.run(fmt.Sprintf(`Unregister-ScheduledTask -TaskName '%s' -TaskPath '%s' -Confirm:$false -ErrorAction Stop`, quoted(name), quoted(folder)))
	return err
}

func (s TaskScheduler) run(script string) ([]byte, error) {
	result, err := s.Commands.Run(context.Background(), execx.Request{
		Name: "powershell",
		Args: []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script},
	})
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, errors.New(strings.TrimSpace(string(result.Stderr)))
	}
	return result.Stdout, nil
}

// splitJobName parts a job's full name into its folder, ending in \, and its
// own name.
func splitJobName(fullName string) (folder, name string) {
	at := strings.LastIndex(fullName, `\`) + 1
	return fullName[:at], fullName[at:]
}

// quoted escapes a value for the single-quoted PowerShell string it goes in.
func quoted(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

// CredentialManager is Windows Credential Manager.
type CredentialManager struct{}

func (CredentialManager) Entries(prefix string) ([]auth.VaultEntry, error) {
	return auth.VaultEntries(prefix)
}
func (CredentialManager) Write(entry auth.VaultEntry) error { return auth.WriteVaultEntry(entry) }
func (CredentialManager) Delete(target string) error        { return auth.DeleteVaultTarget(target) }

// GitHubLabels are a repository's labels on GitHub.
type GitHubLabels struct {
	Client *githubapi.Client
}

// mostLabelPages bounds how many pages of 100 labels a repository is read
// in.
const mostLabelPages = 10

func (l GitHubLabels) Labels(repository string) ([]string, error) {
	var names []string
	for page := 1; page <= mostLabelPages; page++ {
		var labels []struct {
			Name string `json:"name"`
		}
		if err := l.Client.Get(context.Background(), fmt.Sprintf("repos/%s/labels?per_page=100&page=%d", repository, page), &labels); err != nil {
			return nil, err
		}
		for _, label := range labels {
			names = append(names, label.Name)
		}
		if len(labels) < 100 {
			return names, nil
		}
	}
	return nil, fmt.Errorf("%s has more than %d labels", repository, mostLabelPages*100)
}

func (l GitHubLabels) Rename(repository, from, to string) error {
	_, err := l.Client.Do(context.Background(), "PATCH", "repos/"+repository+"/labels/"+url.PathEscape(from), map[string]string{"new_name": to})
	return err
}
