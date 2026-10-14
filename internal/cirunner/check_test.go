package cirunner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// rightApp is an App installed as the runner needs it.
func rightApp() Installation {
	return Installation{Permissions: map[string]string{"administration": "write", "actions": "read", "metadata": "read"}, Selection: "selected", Repositories: []string{"PrecisionDocs-AI"}}
}

// An App that may do more than the service needs, or less, is named with
// each thing it may do wrongly.
func TestCheckFindsAnAppWithMorePermissionThanItNeeds(t *testing.T) {
	for name, row := range map[string]struct {
		change func(*Installation)
		want   string
	}{
		"as the runner needs":    {change: func(*Installation) {}},
		"contents write":         {change: func(i *Installation) { i.Permissions["contents"] = "write" }, want: "it has contents write"},
		"administration read":    {change: func(i *Installation) { i.Permissions["administration"] = "read" }, want: "it has administration read"},
		"no actions":             {change: func(i *Installation) { delete(i.Permissions, "actions") }, want: "it lacks actions read"},
		"every repository":       {change: func(i *Installation) { i.Selection = "all" }, want: "it reaches all repositories"},
		"another repository too": {change: func(i *Installation) { i.Repositories = append(i.Repositories, "other") }, want: "it reaches selected repositories: PrecisionDocs-AI, other"},
		"another repository":     {change: func(i *Installation) { i.Repositories = []string{"other"} }, want: "it reaches selected repositories: other"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			installation := rightApp()
			row.change(&installation)

			// Act
			finding := checkApp(installation, "PrecisionDocs-AI")

			// Assert
			if row.want == "" && finding.Problem != "" || row.want != "" && !strings.Contains(finding.Problem, row.want) {
				t.Errorf("checkApp = %q; want problem %q", finding.Problem, row.want)
			}
		})
	}
}

// rightMachine is a machine as the install leaves it.
func rightMachine() machine {
	return machine{
		ServiceState:                    "Running",
		ServiceAccount:                  "NT SERVICE\\cg-ci-runner",
		IsServiceInHyperVAdministrators: true,
		IsVM:                            true,
		VMGeneration:                    2,
		VMProcessors:                    4,
		VMMemoryMaximum:                 6 << 30,
		IsCheckpoint:                    true,
		FolderAccess:                    []string{"S-1-5-18", "S-1-5-32-544", ServiceSID(ServiceName), "S-1-5-18"},
		ServiceProgram:                  `C:\Program Files\Code Goblins\ci-runner\cg-ci-runner.exe`,
		Owners: map[string]string{
			"the service folder's parent": "S-1-5-32-544",
			"the service folder":          "S-1-5-32-544",
			"the App's settings":          "S-1-5-32-544",
			"the App's key":               "S-1-5-32-544",
			"the VM's folder":             "S-1-5-32-544",
			"the program's folder":        "S-1-5-32-544",
		},
	}
}

// What of the service, its account, its folder or its VM is not as the
// install left it is named, and nothing else is.
func TestCheckMachineSaysWhatIsNotAsTheInstallLeftIt(t *testing.T) {
	for name, row := range map[string]struct {
		change func(*machine)
		want   string
	}{
		"as the install left it":         {change: func(*machine) {}},
		"a stopped service":              {change: func(m *machine) { m.ServiceState = "Stopped" }, want: "it is Stopped"},
		"a service as LocalSystem":       {change: func(m *machine) { m.ServiceAccount = "LocalSystem" }, want: "it runs as LocalSystem"},
		"an account out of Hyper-V":      {change: func(m *machine) { m.IsServiceInHyperVAdministrators = false }, want: "it is not"},
		"a folder open to Users":         {change: func(m *machine) { m.FolderAccess = append(m.FolderAccess, "S-1-5-32-545") }, want: "S-1-5-32-545"},
		"a folder inheriting":            {change: func(m *machine) { m.IsFolderInheriting = true }, want: "inheriting true"},
		"no VM":                          {change: func(m *machine) { m.IsVM = false }, want: "is here false"},
		"a VM with 8 processors":         {change: func(m *machine) { m.VMProcessors = 8 }, want: "8 processors"},
		"a VM with more memory":          {change: func(m *machine) { m.VMMemoryMaximum = 8 << 30 }, want: "8589934592 bytes"},
		"no clean checkpoint":            {change: func(m *machine) { m.IsCheckpoint = false }, want: "it is not"},
		"a key the user owns":            {change: func(m *machine) { m.Owners["the App's key"] = "S-1-5-21-1-2-3-1001" }, want: "the App's key is owned by S-1-5-21-1-2-3-1001"},
		"a parent the user owns":         {change: func(m *machine) { m.Owners["the service folder's parent"] = "S-1-5-21-1-2-3-1001" }, want: "the service folder's parent is owned by S-1-5-21-1-2-3-1001"},
		"an owner unread":                {change: func(m *machine) { delete(m.Owners, "the VM's folder") }, want: "5 of its 6 owners were read"},
		"a program in Downloads":         {change: func(m *machine) { m.ServiceProgram = `C:\Users\u\Downloads\cg-ci-runner.exe` }, want: `it runs C:\Users\u\Downloads\cg-ci-runner.exe`},
		"a program beside Program Files": {change: func(m *machine) { m.ServiceProgram = `C:\Program Files Evil\cg-ci-runner.exe` }, want: "Program Files Evil"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			found := rightMachine()
			row.change(&found)

			// Act
			findings := checkMachine(found, `C:\Program Files`)

			// Assert
			var problems []string
			for _, finding := range findings {
				if finding.Problem != "" {
					problems = append(problems, finding.Checked+": "+finding.Problem)
				}
			}
			if row.want == "" && len(problems) != 0 || row.want != "" && (len(problems) != 1 || !strings.Contains(problems[0], row.want)) {
				t.Errorf("problems %q; want one with %q, or none", problems, row.want)
			}
		})
	}
}

// The check reads the machine, the App and the pipe, and a reading that
// fails is a finding of its own, never a pass.
func TestCheckReadsTheMachineTheAppAndThePipe(t *testing.T) {
	good, err := json.Marshal(rightMachine())
	if err != nil {
		t.Fatal(err)
	}
	for name, row := range map[string]struct {
		machine     execx.Result
		appErr      error
		askErr      error
		wantProblem string
	}{
		"all as installed":      {machine: execx.Result{Stdout: good}},
		"no Hyper-V to read":    {machine: execx.Result{ExitCode: 1, Stderr: []byte("Get-VM : not found")}, wantProblem: "this machine: cirunner: read what the install made: exit 1: Get-VM : not found"},
		"an App GitHub refuses": {machine: execx.Result{Stdout: good}, appErr: errors.New("GitHub answered HTTP 401"), wantProblem: "the App: GitHub answered HTTP 401"},
		"a pipe nobody serves":  {machine: execx.Result{Stdout: good}, askErr: ErrNotServing, wantProblem: "the service's pipe: " + ErrNotServing.Error()},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			commands := &recorder{result: row.machine}
			o := CheckOptions{
				Dir:          `C:\ProgramData\Code Goblins\ci-runner`,
				Repo:         "PrecisionDocs-AI",
				Commands:     commands,
				Installation: func(context.Context) (Installation, error) { return rightApp(), row.appErr },
				Ask: func(_ context.Context, _ Request, reply func(Reply)) error {
					if row.askErr != nil {
						return row.askErr
					}
					reply(Reply{Kind: ReplyStatus, Line: "no job runs"})
					return nil
				},
			}

			// Act
			findings := Check(context.Background(), o)

			// Assert
			var problems []string
			for _, finding := range findings {
				if finding.Problem != "" {
					problems = append(problems, finding.Checked+": "+finding.Problem)
				}
			}
			if row.wantProblem == "" && len(problems) != 0 || row.wantProblem != "" && (len(problems) != 1 || problems[0] != row.wantProblem) {
				t.Errorf("problems %q; want %q alone, or none", problems, row.wantProblem)
			}
			if len(commands.ran) != 1 || strings.Contains(script(commands.ran[0]), o.Dir) {
				t.Errorf("ran %d script(s), the folder in its text %v; want one, with its folder in the environment", len(commands.ran), len(commands.ran) == 1 && strings.Contains(script(commands.ran[0]), o.Dir))
			}
		})
	}
}
