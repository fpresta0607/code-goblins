package cirunner

import (
	"strings"
	"testing"
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
