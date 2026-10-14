package cirunner

import (
	_ "embed"
	"strings"
	"text/template"
)

//go:embed image/user-data.yaml
var userData string

// UserData is the VM's first boot settings, as cloud-init reads them from the
// settings disk, with the runner pinned in pins.go.
func UserData() (string, error) {
	page, err := template.New("user-data").Option("missingkey=error").Parse(userData)
	if err != nil {
		return "", err
	}
	var rendered strings.Builder
	err = page.Execute(&rendered, struct{ RunnerURL, RunnerSHA256 string }{RunnerURL, RunnerSHA256})
	return rendered.String(), err
}

// MetaData is the VM's NoCloud instance: the id cloud-init makes it once
// for, and its name.
const MetaData = "instance-id: cg-ci-precisiondocs\nlocal-hostname: cg-ci-precisiondocs\n"
