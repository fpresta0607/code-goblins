package gatetest

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"gopkg.in/yaml.v3"
)

// planner is the directory of the program that asks the rule in CI.
const planner = "./tools/ciplan"

// repository is this repository as the rule reads it in CI: its packages as
// go list reports them, its policy, the files it tracks and its workflow's
// table of jobs.
type repository struct {
	root, module, policy string
	packages             []Package
	tracked              []string
	table                JobTable
}

var readRepository = sync.OnceValues(func() (repository, error) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return repository{}, err
	}
	repo := repository{root: fsx.LongPath(root)}
	policy, err := os.ReadFile(filepath.Join(repo.root, filepath.FromSlash(PolicyPath)))
	if err != nil {
		return repository{}, err
	}
	repo.policy = string(policy)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	listed, err := output(ctx, execx.OSRunner{}, repo.root, "go", "list", "-e", "-json="+listFields, "./...")
	if err != nil {
		return repository{}, err
	}
	if repo.packages, err = decodePackages(strings.NewReader(listed)); err != nil {
		return repository{}, err
	}
	module, err := output(ctx, execx.OSRunner{}, repo.root, "go", "list", "-m", "-f", "{{.Path}}")
	if err != nil {
		return repository{}, err
	}
	repo.module = strings.TrimSpace(module)
	tracked, err := output(ctx, execx.OSRunner{}, repo.root, "git", "ls-files", "-z")
	if err != nil {
		return repository{}, err
	}
	repo.tracked = names(tracked)
	source, err := os.ReadFile(filepath.Join(repo.root, ".github", "workflows", "go.yml"))
	if err != nil {
		return repository{}, err
	}
	var workflow struct {
		Env struct {
			Jobs string `yaml:"JOBS"`
		} `yaml:"env"`
	}
	if err := yaml.Unmarshal(source, &workflow); err != nil {
		return repository{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(workflow.Env.Jobs))
	decoder.DisallowUnknownFields()
	return repo, decoder.Decode(&repo.table)
})

// jobsFor are the jobs a pull request that changed files starts, by name,
// with the plan they follow from.
func (r repository) jobsFor(changed ...string) (Jobs, []string) {
	plan := build(findings{
		base:      "1111111111111111111111111111111111111111",
		commit:    "2222222222222222222222222222222222222222",
		root:      r.root,
		module:    r.module,
		changed:   changed,
		packages:  r.packages,
		policy:    r.policy,
		hasPolicy: true,
	}, Affected)
	jobs := ChooseJobs(plan, r.table, planner)
	var started []string
	if jobs.Frontend {
		started = append(started, "frontend")
	}
	if jobs.Browser {
		started = append(started, "browser")
	}
	for _, shard := range jobs.Go {
		started = append(started, "go ("+shard.Shard+")")
	}
	return jobs, started
}

// every are all the jobs of the table, by name.
func (r repository) every() []string {
	started := []string{"frontend", "browser"}
	for _, shard := range r.table.Go {
		started = append(started, "go ("+shard.Shard+")")
	}
	return started
}

// The rule a pull request's own run follows, on this repository as it is:
// for a change to the named files, the jobs that must start and the jobs
// that must not. Each file is one the repository tracks, so no case passes
// on a path that names nothing.
func TestAPullRequestsRunStartsTheJobsItsChangeCanAlter(t *testing.T) {
	repo, err := readRepository()
	if err != nil {
		t.Fatal(err)
	}
	goJobs := repo.every()[2:]
	for name, test := range map[string]struct {
		changed  []string
		starts   []string
		leaveOut []string
	}{
		"a board change": {[]string{"frontend/src/App.tsx"}, []string{"frontend", "browser"}, goJobs},
		"a browser spec": {[]string{"frontend/tests/afk.spec.ts"}, []string{"frontend", "browser"}, goJobs},
		"a Go change in a package of the rest job":      {[]string{"internal/train/engine.go"}, []string{"go (rest)", "go (cmd-cfo-1)", "go (cmd-cfo-2)"}, []string{"frontend", "browser"}},
		"a Go change in a package with jobs of its own": {[]string{"internal/conpty/conpty_windows.go"}, []string{"go (conpty)", "go (rest)"}, []string{"browser"}},
		"a document nothing embeds":                     {[]string{"README.md"}, nil, repo.every()},
		"a document the binary embeds":                  {[]string{"AGENTS.md"}, []string{"go (rest)", "go (cmd-cfo-1)", "go (cmd-cfo-2)"}, []string{"frontend", "browser"}},
		"a file a browser spec reads":                   {[]string{"cmd/goblins-window/notes.go"}, []string{"browser", "go (rest)"}, []string{"go (conpty)"}},
		"the page served where no board is built":       {[]string{"internal/boardweb/dist/index.html"}, []string{"browser", "frontend", "go (rest)"}, nil},
		"the licence notices":                           {[]string{"THIRD_PARTY_NOTICES"}, []string{"frontend"}, append([]string{"browser"}, goJobs...)},
		"the licence check's own code":                  {[]string{"tools/notices/main.go"}, []string{"frontend", "go (rest)"}, []string{"browser"}},
		"a board change and a Go change together":       {[]string{"frontend/src/App.tsx", "internal/train/engine.go"}, []string{"frontend", "browser", "go (rest)"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			for _, file := range test.changed {
				if !slices.Contains(repo.tracked, file) {
					t.Fatalf("the repository tracks no %s, so the case proves nothing", file)
				}
			}

			// Act
			jobs, started := repo.jobsFor(test.changed...)

			// Assert
			if jobs.Everything {
				t.Fatalf("a change to %v starts every job (%s), want the rule to narrow it", test.changed, jobs.Why)
			}
			for _, job := range test.starts {
				if !slices.Contains(started, job) {
					t.Errorf("a change to %v does not start %s; it starts %v", test.changed, job, started)
				}
			}
			for _, job := range test.leaveOut {
				if slices.Contains(started, job) {
					t.Errorf("a change to %v starts %s, which nothing in it reaches; it starts %v", test.changed, job, started)
				}
			}
		})
	}
}

// What the rule cannot be sure of widens the run: a change to the rule
// itself, to a module file, or to a file the policy does not account for
// starts every job.
func TestAChangeTheRuleCannotJudgeStartsEveryJob(t *testing.T) {
	repo, err := readRepository()
	if err != nil {
		t.Fatal(err)
	}
	for name, file := range map[string]string{
		"the workflow":                           ".github/workflows/go.yml",
		"another workflow":                       ".github/workflows/install.yml",
		"the policy the rule reads":              PolicyPath,
		"the program that asks the rule":         "tools/ciplan/main.go",
		"the rule":                               "internal/gatetest/jobs.go",
		"a package the rule's program imports":   "internal/execx/execx.go",
		"a module file":                          "go.mod",
		"a file the policy does not account for": "a-new-folder/a-new-file.xyz",
		"a workflow that does not exist yet":     ".github/workflows/new.yml",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			if isNew := strings.Contains(file, "new"); !isNew && !slices.Contains(repo.tracked, file) {
				t.Fatalf("the repository tracks no %s, so the case proves nothing", file)
			}

			// Act
			jobs, started := repo.jobsFor(file)

			// Assert
			if want := repo.every(); !jobs.Everything || !slices.Equal(started, want) {
				t.Errorf("a change to %s starts %v (everything: %t, %s), want every job: %v", file, started, jobs.Everything, jobs.Why, want)
			}
		})
	}
}

// Whatever a change reaches, a job that tests it starts: for one Go file of
// every package in turn, each package the change reaches is tested by a job
// that starts, and all of a split package's jobs start together. A table
// that named a package wrong, or a rule that dropped one, would leave a
// reached package untested on the pull request's own run.
func TestEveryPackageAChangeReachesIsTestedByAJobThatStarts(t *testing.T) {
	// Arrange
	repo, err := readRepository()
	if err != nil {
		t.Fatal(err)
	}
	own := map[string][]string{}
	rest := ""
	for _, shard := range repo.table.Go {
		for _, dir := range strings.Fields(shard.Packages) {
			own[dir] = append(own[dir], "go ("+shard.Shard+")")
		}
		if shard.Except != "" {
			rest = "go (" + shard.Shard + ")"
		}
	}
	checked := 0
	for _, pkg := range repo.packages {
		dir, err := filepath.Rel(repo.root, pkg.Dir)
		if err != nil {
			t.Fatal(err)
		}
		dir = filepath.ToSlash(dir)
		index := slices.IndexFunc(repo.tracked, func(file string) bool { return path.Dir(file) == dir && path.Ext(file) == ".go" })
		if index < 0 {
			continue
		}
		checked++

		// Act
		jobs, started := repo.jobsFor(repo.tracked[index])
		plan := build(findings{root: repo.root, module: repo.module, changed: []string{repo.tracked[index]}, packages: repo.packages, policy: repo.policy, hasPolicy: true}, Affected)

		// Assert
		if jobs.Everything {
			continue
		}
		if len(plan.Choices) == 0 {
			t.Errorf("a change to %s reaches no package", repo.tracked[index])
		}
		for _, choice := range plan.Choices {
			tests := own["."+strings.TrimPrefix(choice.ImportPath, repo.module)]
			if tests == nil {
				tests = []string{rest}
			}
			for _, job := range tests {
				if !slices.Contains(started, job) {
					t.Errorf("a change to %s reaches %s, which %s tests, and starts only %v", repo.tracked[index], choice.ImportPath, job, started)
				}
			}
		}
	}
	if rest == "" || checked < 10 {
		t.Fatalf("the check read %d packages and the rest job %q; it proves nothing unless the repository and its table were read", checked, rest)
	}
}

// The rule on a module small enough to read: a leaf package a and a package
// b that imports it, in jobs of their own, and beside them in the rest job a
// package c nothing imports, the planner and a package d it imports.
func TestChooseJobsFollowsWhatTheChangeReaches(t *testing.T) {
	table := JobTable{
		Frontend: JobRule{Paths: []string{"frontend/**", "NOTICES"}, Packages: []string{"./c"}},
		Browser:  JobRule{Paths: []string{"frontend/**", "b/page.html"}},
		Go: []Shard{
			{Shard: "a-1", Packages: "./a", Run: "One"},
			{Shard: "a-2", Packages: "./a", Skip: "One"},
			{Shard: "b", Packages: "./b"},
			{Shard: "rest", Except: "./a ./b"},
		},
	}
	policy := `{"version":1,"contracts":[{"paths":["b/page.html"],"packages":["b"],"why":"a test reads it"}],"outside":[{"paths":["frontend/**","docs/**","NOTICES"],"why":"no Go check reads these"}]}`
	for name, test := range map[string]struct {
		changed    []string
		everything bool
		want       []string
	}{
		"a leaf package starts its jobs, its importer's and the rest": {[]string{"a/a.go"}, false, []string{"go (a-1)", "go (a-2)", "go (b)", "go (rest)"}},
		"a package nothing imports starts the rest":                   {[]string{"c/c.go"}, false, []string{"frontend", "go (rest)"}},
		"the frontend starts the frontend and the browser":            {[]string{"frontend/src/main.ts"}, false, []string{"frontend", "browser"}},
		"a file only the frontend job reads":                          {[]string{"NOTICES"}, false, []string{"frontend"}},
		"a file a spec and a Go test read":                            {[]string{"b/page.html"}, false, []string{"browser", "go (b)", "go (rest)"}},
		"a document":                                                  {[]string{"docs/guide.md"}, false, nil},
		"nothing":                                                     {nil, false, nil},
		"the planner":                                                 {[]string{"tools/ciplan/main.go"}, true, []string{"frontend", "browser", "go (a-1)", "go (a-2)", "go (b)", "go (rest)"}},
		"a package the planner imports":                               {[]string{"d/d.go"}, true, []string{"frontend", "browser", "go (a-1)", "go (a-2)", "go (b)", "go (rest)"}},
		"a workflow":                                                  {[]string{".github/workflows/go.yml"}, true, []string{"frontend", "browser", "go (a-1)", "go (a-2)", "go (b)", "go (rest)"}},
		"the policy":                                                  {[]string{PolicyPath}, true, []string{"frontend", "browser", "go (a-1)", "go (a-2)", "go (b)", "go (rest)"}},
		"a module file":                                               {[]string{"go.sum"}, true, []string{"frontend", "browser", "go (a-1)", "go (a-2)", "go (b)", "go (rest)"}},
		"a file nothing accounts for":                                 {[]string{"mystery.bin"}, true, []string{"frontend", "browser", "go (a-1)", "go (a-2)", "go (b)", "go (rest)"}},
		"a document beside a file nothing accounts for":               {[]string{"docs/guide.md", "mystery.bin"}, true, []string{"frontend", "browser", "go (a-1)", "go (a-2)", "go (b)", "go (rest)"}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			found := branch(test.changed...)
			found.packages = append(found.packages, pkg("d"), pkg("tools/ciplan", "example.com/repo/d"))
			found.policy, found.hasPolicy = policy, true

			// Act
			jobs := ChooseJobs(build(found, Affected), table, planner)

			// Assert
			var started []string
			if jobs.Frontend {
				started = append(started, "frontend")
			}
			if jobs.Browser {
				started = append(started, "browser")
			}
			for _, shard := range jobs.Go {
				started = append(started, "go ("+shard.Shard+")")
			}
			if jobs.Everything != test.everything || !slices.Equal(started, test.want) {
				t.Errorf("a change to %v starts %v (everything: %t, %s), want %v (everything: %t)", test.changed, started, jobs.Everything, jobs.Why, test.want, test.everything)
			}
		})
	}
}

// A policy the rule cannot read narrows nothing.
func TestChooseJobsStartsEveryJobUnderAPolicyItCannotRead(t *testing.T) {
	// Arrange
	found := branch("docs/guide.md")
	found.policy, found.hasPolicy = `{"version":99}`, true
	table := JobTable{Frontend: JobRule{Paths: []string{"frontend/**"}}, Browser: JobRule{Paths: []string{"frontend/**"}}, Go: []Shard{{Shard: "rest", Except: "./a"}}}

	// Act
	jobs := ChooseJobs(build(found, Affected), table, planner)

	// Assert
	if !jobs.Everything || !jobs.Frontend || !jobs.Browser || len(jobs.Go) != 1 {
		t.Errorf("under a policy of another version the rule starts %+v, want every job", jobs)
	}
}
