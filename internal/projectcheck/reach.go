package projectcheck

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// heldValue is one production value of an env file, by its variable and why
// it is production's.
type heldValue struct {
	variable, reason string
	// isPinned says the test setup names the variable.
	isPinned bool
}

// heldFile is an env file a goblin's worktree holds: what it holds and what
// loads it.
type heldFile struct {
	name, how string
	values    []heldValue
	// publicStores are the variables that hold the address of a store on
	// another host under a public name.
	publicStores []string
	// loaded says what loads the file, and the two after it whether a test
	// run does: through a test or its setup, or through the application.
	loaded                    string
	byATest, byTheApplication bool
}

// entry names one value for a line, a key beside the public address of the
// store it opens.
func (h heldFile) entry(value heldValue) string {
	reason := value.reason
	if reason == liveKey || reason == realCredential || reason == namedCredential {
		for _, address := range h.publicStores {
			if sharesAStore(value.variable, address) {
				reason += ", which opens the host " + address + " names"
				break
			}
		}
	}
	return value.variable + " (" + reason + ")"
}

// sharesAStore reports whether two variables are named for one service.
func sharesAStore(a, b string) bool {
	others := strings.Split(strings.ToUpper(b), "_")
	for _, word := range storeWords(strings.ToUpper(a)) {
		if slices.Contains(others, word) {
			return true
		}
	}
	return false
}

// productionReach checks the env files a goblin's worktree holds: the ones
// the worktree manifest gives it from the checkout and the ones the
// repository tracks. A production value in one is within a test run's reach
// when a test, its setup or the application's own code loads the file,
// unless the test setup names the variable, which is how a project pins it.
// With no test command of the repository's own an agent chooses what the
// test step runs, and nothing stands between it and the file. A file no
// test loads is said to be that: what it holds is in the worktree all the
// same, within reach of the scripts and the development server that do load
// it, and of whoever reads it.
func (c *checker) productionReach(ctx context.Context, test string, setup testSetup) error {
	type envFile struct{ name, how string }
	var files []envFile
	if manifest, err := worktree.Resolve(c.DataDir, c.project); err == nil {
		how := "which every goblin's worktree shares by " + worktree.ManifestFileName
		for _, name := range manifest.Link {
			if !c.repo.tracked[name] {
				files = append(files, envFile{name, how})
			}
		}
	}
	for _, name := range sortedKeys(c.repo.tracked) {
		if isEnvFile(path.Base(name)) && !isExample(path.Base(name)) {
			files = append(files, envFile{name, "which the repository tracks"})
		}
	}
	loaders, err := c.envLoaders(ctx)
	if err != nil {
		return err
	}

	variables, production, pinned := 0, 0, 0
	pinners := map[string]bool{}
	passedOver := map[string]map[string]bool{}
	var held []heldFile
	var read []string
	for _, file := range files {
		var data []byte
		if c.repo.tracked[file.name] {
			data, _ = c.repo.read(ctx, file.name)
		} else {
			var err error
			if data, err = fsx.ReadFile(filepath.Join(c.Checkout, filepath.FromSlash(file.name))); err != nil {
				continue
			}
		}
		values, err := auth.ParseEnv(bytes.NewReader(data))
		if err != nil {
			continue
		}
		holds := heldFile{name: file.name, how: file.how}
		holds.loaded, holds.byATest, holds.byTheApplication = whatLoads(loaders, file.name)
		read = append(read, file.name+", "+file.how+", "+holds.loaded)
		variables += len(values)
		for _, variable := range sortedKeys(values) {
			verdict := judge(variable, values[variable], c.repo.tracked[file.name])
			switch {
			case verdict.isPublicStore:
				holds.publicStores = append(holds.publicStores, variable)
			case verdict.passedOver != "":
				if passedOver[verdict.passedOver] == nil {
					passedOver[verdict.passedOver] = map[string]bool{}
				}
				passedOver[verdict.passedOver][variable] = true
			}
			if verdict.production == "" {
				continue
			}
			production++
			value := heldValue{variable: variable, reason: verdict.production}
			if by := setup.pinnedBy(variable); by != "" {
				pinned++
				pinners[by] = true
				value.isPinned = true
			}
			holds.values = append(holds.values, value)
		}
		held = append(held, holds)
	}

	// A file a test run loads is judged by what the test setup leaves open.
	// A file no test loads is judged by all it holds, since a setup that
	// names a variable clears nothing for a script.
	var reached, kept []string
	reachedCount, keptCount := 0, 0
	reachedWorst, keptWorst, byATest := false, false, false
	for _, holds := range held {
		isLoadedByTests := holds.byATest || holds.byTheApplication
		var entries []string
		worst := false
		for _, value := range holds.values {
			if isLoadedByTests && value.isPinned {
				continue
			}
			entries = append(entries, holds.entry(value))
			worst = worst || value.reason == liveKey || value.reason == namesProduction
		}
		if len(entries) == 0 {
			continue
		}
		line := holds.name + ", " + holds.how + ", " + holds.loaded + ": " + strings.Join(entries, ", ")
		if isLoadedByTests {
			reached, reachedCount, reachedWorst, byATest = append(reached, line), reachedCount+len(entries), reachedWorst || worst, byATest || holds.byATest
			continue
		}
		kept, keptCount, keptWorst = append(kept, line), keptCount+len(entries), keptWorst || worst
	}

	passed := 0
	for _, names := range passedOver {
		passed += len(names)
	}
	const floorOnly = "The count is a floor, since the rules read a variable's name and the shape of its value"
	floor := floorOnly
	switch {
	case passed == 1:
		floor += ": 1 more variable holds a value the rules pass over, named on the line test-env-examined"
	case passed > 1:
		floor += fmt.Sprintf(": %d more variables hold a value the rules pass over, named on the line test-env-examined", passed)
	}
	pins := fmt.Sprintf("Named by the test setup and left out: %d", pinned)
	if len(pinners) > 0 {
		pins += " (" + strings.Join(sortedKeys(pinners), ", ") + ")"
	}
	if reachedCount > 0 {
		step := fmt.Sprintf("The gate's test step is the repository's own command, %q", test)
		if test == "" {
			step = "The gate's test step is an agent's choice, since the gate names no test command"
		}
		severity := High
		if reachedWorst || test == "" {
			severity = Critical
		}
		says := "a test run in a goblin's worktree loads production: a test or its setup loads an env file that holds " + count(reachedCount, "production value") + " the test setup does not name"
		if !byATest {
			says = "a test run in a goblin's worktree can load production: the application's own code loads an env file that holds " + count(reachedCount, "production value") + " the test setup does not name, and a test that starts the application runs that code"
		}
		c.add(AreaGate, "test-reaches-production", severity, says,
			strings.Join(reached, ". ")+". "+pins+". A setup that clears variables by a rule and not by name is not seen here. "+step+". "+floor+". Read at "+c.repo.asRead(),
			"keep production out of what a worktree shares by naming a development env file, or none, as link in "+worktree.ManifestFileName+", or name each variable in the test setup")
	}
	if keptCount > 0 {
		severity := High
		if keptWorst {
			severity = Critical
		}
		c.add(AreaGate, "worktree-holds-production", severity,
			"a goblin's worktree holds "+count(keptCount, "production value")+" in its env files, and no test loads them as far as this check can tell, so their reach is what does load the file and whoever reads it",
			strings.Join(kept, ". ")+". A test run counts as loading a file when a test, its setup or the application's own code loads it. "+floor+". Read at "+c.repo.asRead(),
			"keep production out of what a worktree shares by naming a development env file, or none, as link in "+worktree.ManifestFileName)
	}

	evidence := "no env file is shared into a goblin's worktree or tracked"
	if len(read) > 0 {
		evidence = strings.Join(read, ". ")
	}
	for _, reason := range passedOverReasons {
		if names := sortedKeys(passedOver[reason]); len(names) > 0 {
			evidence += ". Passed over as " + reason + ": " + strings.Join(names, ", ")
		}
	}
	c.add(AreaGate, "test-env-examined", OK,
		fmt.Sprintf("examined %s in %s a goblin's worktree holds: %s, %d of them named by the test setup", count(variables, "variable"), count(len(read), "env file"), count(production, "production value"), pinned),
		evidence+". "+pins+". "+floorOnly+". Test setup read at "+c.repo.asRead()+": "+setup.read(), "")
	return nil
}
