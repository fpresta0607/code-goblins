package projectcheck

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

// terminalReach checks the second road a test run has to production: the
// credentials a spawn writes into a task's terminal. A terminal carries the
// stored credentials of the services its task is granted, and a test run
// started there inherits them with no env file at all. Which services a
// task is granted is the manifest's own answer: the ones marked default for
// a brief with no credentials line, and the ones a brief names.
//
// A credential counts as within a test's reach when a tracked file a test
// run could start reads its variable, and no test setup names it. It reads
// auth.json and the repository and never the credential store, so it names
// variables and cannot say what one holds.
func (c *checker) terminalReach(ctx context.Context, test string, setup testSetup) error {
	file := auth.ManifestPath(c.DataDir, c.project)
	manifest, err := auth.LoadManifest(c.DataDir, c.project)
	if err != nil {
		c.add(AreaGate, "terminal-credentials-examined", OK,
			"a task's terminal carries no credential from the credential store, since the project has no auth manifest the loader takes",
			"no manifest read at "+file+", which the connectors area reports", "")
		return nil
	}
	var names, publishable, forTests []string
	for _, service := range manifest.Services {
		for _, name := range service.Env {
			switch {
			case publishableName(name):
				publishable = append(publishable, name)
			case namedForTests(name):
				forTests = append(forTests, name)
			default:
				names = append(names, name)
			}
		}
	}
	readers, err := c.repo.readers(ctx, names, func(name string) bool {
		return !sourceExtensions[strings.ToLower(path.Ext(name))] || isDocument(name) || suppliedByGitHub(name)
	})
	if err != nil {
		return err
	}
	byDefault := manifest.Grant(auth.Need{IsUnstated: true}).Services

	var everyTask, whenNamed, carried, unread, optional []string
	variables, read, pinned, openByDefault, open := 0, 0, 0, 0, 0
	pinners := map[string]bool{}
	for _, service := range manifest.Services {
		var own, within, idle []string
		for _, name := range service.Env {
			if publishableName(name) || namedForTests(name) {
				continue
			}
			variables++
			own = append(own, name)
			reader := readers[strings.ToUpper(name)]
			if reader == "" {
				idle = append(idle, name)
				continue
			}
			read++
			if by := setup.pinnedBy(name); by != "" {
				pinned++
				pinners[by] = true
				continue
			}
			within = append(within, name+" ("+reader+")")
		}
		isDefault := slices.Contains(byDefault, service.Name)
		if len(own) > 0 {
			line := service.Name + ": " + strings.Join(own, ", ")
			if isDefault {
				line += ", by default"
			}
			carried = append(carried, line)
		}
		if len(idle) > 0 {
			unread = append(unread, service.Name+": "+strings.Join(idle, ", "))
		}
		if service.Optional {
			optional = append(optional, service.Name)
		}
		if len(within) == 0 {
			continue
		}
		open += len(within)
		line := service.Name + ": " + strings.Join(within, ", ")
		if isDefault {
			openByDefault += len(within)
			everyTask = append(everyTask, line)
			continue
		}
		whenNamed = append(whenNamed, line)
	}

	pins := fmt.Sprintf("Named by the test setup and left out: %d", pinned)
	if len(pinners) > 0 {
		pins += " (" + strings.Join(sortedKeys(pinners), ", ") + ")"
	}
	const unopened = "The credential store is not opened, so whether each is stored and what it holds is not read: a terminal carries a variable only when the store holds it"
	if open > 0 {
		severity := Medium
		says := "a test run in the terminal of a task whose brief names a service inherits that service's credentials, " + fmt.Sprint(open) + " of which the repository reads and no test setup names"
		fix := "name each variable in the test setup with a value for tests, or keep the service off the credentials line of a brief whose task runs the tests"
		if openByDefault > 0 {
			severity = High
			if test == "" {
				severity = Critical
			}
			says = "a test run in a task's terminal inherits " + count(open, "credential") + " the repository reads and no test setup names, " + fmt.Sprint(openByDefault) + " of them in every task whose brief has no credentials line"
			fix = "name each variable in the test setup with a value for tests, and take default off a service in " + file + " that a task should carry only when its brief names it"
		}
		var evidence []string
		if len(everyTask) > 0 {
			evidence = append(evidence, "Carried by every task whose brief has no credentials line: "+strings.Join(everyTask, ". "))
		}
		if len(whenNamed) > 0 {
			evidence = append(evidence, "Carried when a brief names the service: "+strings.Join(whenNamed, ". "))
		}
		step := fmt.Sprintf("The gate's test step is the repository's own command, %q", test)
		if test == "" {
			step = "The gate's test step is an agent's choice, since the gate names no test command"
		}
		c.add(AreaGate, "terminal-reaches-production", severity, says,
			strings.Join(evidence, ". ")+". Each is named with the first tracked file that reads it. "+pins+". "+unopened+". "+step+". Read at "+c.repo.asRead(), fix)
	}

	evidence := "Every task whose brief has no credentials line carries none, since no service is marked default"
	if len(byDefault) > 0 {
		evidence = "Every task whose brief has no credentials line carries the default services: " + strings.Join(byDefault, ", ")
	}
	for _, part := range []struct {
		says  string
		names []string
	}{
		{"A task carries a service's variables when its brief names it", carried},
		{"Read by no tracked file", unread},
		{"Left out as publishable", publishable},
		{"Left out as named for tests", forTests},
		{"Optional, so carried only when stored", optional},
	} {
		if len(part.names) > 0 {
			evidence += ". " + part.says + ": " + strings.Join(part.names, ". ")
		}
	}
	c.add(AreaGate, "terminal-credentials-examined", OK,
		fmt.Sprintf("examined the credentials a task's terminal carries by %s: %s and %s, %d of them read by the repository, %d of those named by the test setup", auth.ManifestFileName, count(len(manifest.Services), "service"), count(variables, "variable"), read, pinned),
		evidence+". "+pins+". "+unopened+". A file counts as reading a variable when it is source code tracked at "+c.repo.at()+" that names it as a word of its own, outside documents and workflows. Test setup read at "+c.repo.asRead()+": "+setup.read(), "")
	return nil
}

// sourceExtensions end the name of a file of source code, which a run of
// the project or of its tests can start. A settings file, a manifest and a
// workflow name variables too, and read none.
var sourceExtensions = map[string]bool{
	".py": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".mts": true, ".cts": true, ".tsx": true, ".jsx": true,
	".go": true, ".rb": true, ".php": true, ".java": true, ".kt": true, ".cs": true, ".rs": true, ".swift": true, ".ex": true, ".exs": true,
	".sh": true, ".ps1": true, ".vue": true, ".svelte": true, ".astro": true,
}

// namedForTests reports whether a variable is named as a value for tests,
// as TEST_DATABASE_URL is: one of its words is TEST or TESTING.
func namedForTests(name string) bool {
	for _, word := range strings.Split(strings.ToUpper(name), "_") {
		if word == "TEST" || word == "TESTING" {
			return true
		}
	}
	return false
}
