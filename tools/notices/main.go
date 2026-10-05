// Command notices writes THIRD_PARTY_NOTICES, the licence of everything a
// Code Goblins release ships: the Go standard library and every module
// compiled into the commands under cmd/, the npm packages bundled into the
// board, and the board's own licensed assets. With -check it writes nothing
// and fails when the file is stale or when anything shipped is under a
// copyleft or unrecognised licence. Build-only tools, such as Vite and its
// MPL-2.0 lightningcss, ship nothing and are not read.
//
// Run it from anywhere in the repository after `npm ci` in frontend.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// noticesFile is the generated file, at the repository root.
const noticesFile = "THIRD_PARTY_NOTICES"

// component is one thing a release ships under a licence of its own.
type component struct {
	name    string
	licence string
	text    string
}

// section is a group of components the notices list under one heading.
type section struct {
	heading    string
	components []component
}

func main() {
	check := flag.Bool("check", false, "write nothing; fail when "+noticesFile+" is stale or anything shipped is not permissively licensed")
	flag.Parse()
	root, err := goOutput(".", "list", "-m", "-f", "{{.Dir}}")
	if err == nil {
		err = run(root, *check)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "notices:", err)
		os.Exit(1)
	}
}

func run(root string, check bool) error {
	goroot, err := goOutput(root, "env", "GOROOT")
	if err != nil {
		return err
	}
	listed, err := goCommand(root, "list", "-deps", "-json", "./cmd/...")
	if err != nil {
		return err
	}
	modules, err := goModules(bytes.NewReader(listed))
	if err != nil {
		return err
	}
	goSection, goProblems, err := goComponents(goroot, modules)
	if err != nil {
		return err
	}
	npmSection, npmProblems, err := npmComponents(filepath.Join(root, "frontend"))
	if err != nil {
		return err
	}
	known := append(slices.Clone(goSection.components), npmSection.components...)
	assetSection, assetProblems, err := assetComponents(filepath.Join(root, "frontend", "public", "assets"), known)
	if err != nil {
		return err
	}
	problems := slices.Concat(goProblems, npmProblems, assetProblems)
	return apply(filepath.Join(root, noticesFile), render([]section{goSection, npmSection, assetSection}), problems, check)
}

// apply writes content to path, or with check compares it. Problems refuse
// both, so the file never lists a shipped component whose licence failed.
func apply(path, content string, problems []string, check bool) error {
	if len(problems) > 0 {
		return fmt.Errorf("these shipped components are not permissively licensed:\n  %s", strings.Join(problems, "\n  "))
	}
	if !check {
		return os.WriteFile(path, []byte(content), 0o644)
	}
	current, err := fsx.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if string(current) != content {
		return fmt.Errorf("%s is stale: run go run ./tools/notices and commit the result", filepath.Base(path))
	}
	return nil
}

// module is one Go module a shipped command compiles in.
type module struct {
	Path    string
	Version string
	Dir     string
}

// goModules reads `go list -deps -json` output: every module that provides
// a listed package, other than the main module, once each, by path. A
// package of the standard library is reported as the empty module.
func goModules(r io.Reader) ([]module, error) {
	type listedModule struct {
		Path    string
		Version string
		Dir     string
		Main    bool
		Replace *listedModule
	}
	var modules []module
	seen := map[string]bool{}
	decoder := json.NewDecoder(r)
	for {
		var pkg struct {
			Standard bool
			Module   *listedModule
		}
		if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("read go list output: %w", err)
		}
		var m module
		switch {
		case pkg.Standard:
		case pkg.Module == nil || pkg.Module.Main:
			continue
		case pkg.Module.Replace != nil:
			m = module{Path: pkg.Module.Path, Version: pkg.Module.Replace.Version, Dir: pkg.Module.Replace.Dir}
		default:
			m = module{Path: pkg.Module.Path, Version: pkg.Module.Version, Dir: pkg.Module.Dir}
		}
		if !seen[m.Path] {
			seen[m.Path] = true
			modules = append(modules, m)
		}
	}
	slices.SortFunc(modules, func(a, b module) int { return strings.Compare(a.Path, b.Path) })
	return modules, nil
}

// goComponents reads each module's licence from its directory, and the
// standard library's from goroot, which every command compiles in.
func goComponents(goroot string, modules []module) (section, []string, error) {
	s := section{heading: "Go standard library and modules compiled into the commands under cmd/"}
	var problems []string
	for _, m := range modules {
		name, dir := m.Path+" "+m.Version, m.Dir
		if m.Path == "" {
			name, dir = "Go standard library", goroot
		}
		text, err := licenceText(dir)
		if err != nil {
			return s, nil, fmt.Errorf("%s: %w", name, err)
		}
		licence, err := classify(text)
		if err != nil {
			problems = append(problems, name+": "+err.Error())
		}
		s.components = append(s.components, component{name: name, licence: licence, text: text})
	}
	return s, problems, nil
}

// npmComponents lists the packages package-lock.json installs for the
// board's build other than its development tools: those are what the bundle
// carries. Each must declare a permissive SPDX licence and ship a licence
// file whose text is recognised as permissive.
func npmComponents(frontend string) (section, []string, error) {
	s := section{heading: "npm packages bundled into the board"}
	raw, err := fsx.ReadFile(filepath.Join(frontend, "package-lock.json"))
	if err != nil {
		return s, nil, err
	}
	var lock struct {
		Packages map[string]struct {
			Version     string `json:"version"`
			License     string `json:"license"`
			Dev         bool   `json:"dev"`
			DevOptional bool   `json:"devOptional"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(raw, &lock); err != nil {
		return s, nil, fmt.Errorf("read package-lock.json: %w", err)
	}
	var problems []string
	for path, pkg := range lock.Packages {
		if path == "" || pkg.Dev || pkg.DevOptional {
			continue
		}
		name := path[strings.LastIndex(path, "node_modules/")+len("node_modules/"):]
		label := "npm " + name + " " + pkg.Version
		text, err := licenceText(filepath.Join(frontend, filepath.FromSlash(path)))
		if errors.Is(err, fs.ErrNotExist) {
			return s, nil, fmt.Errorf("%s is not installed: run npm ci in frontend first", name)
		}
		if err != nil {
			return s, nil, fmt.Errorf("%s: %w", label, err)
		}
		if !permissiveExpression(pkg.License) {
			problems = append(problems, fmt.Sprintf("%s: declares %q, which is not a permissive licence", label, pkg.License))
		} else if _, err := classify(text); err != nil {
			problems = append(problems, label+": "+err.Error())
		}
		s.components = append(s.components, component{name: label, licence: pkg.License, text: text})
	}
	slices.SortFunc(s.components, func(a, b component) int { return strings.Compare(a.name, b.name) })
	return s, problems, nil
}

// assetComponents lists the licence files the board carries beside assets
// of its own, such as its fonts, other than copies of a licence text already
// listed for a package.
func assetComponents(assets string, known []component) (section, []string, error) {
	s := section{heading: "Board assets with their own licence, served under /assets"}
	var problems []string
	err := filepath.WalkDir(assets, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !isLicenceFile(entry.Name()) {
			return err
		}
		raw, err := fsx.ReadFile(path)
		if err != nil {
			return err
		}
		text := normalise(raw)
		if slices.ContainsFunc(known, func(c component) bool { return c.text == text }) {
			return nil
		}
		relative, err := filepath.Rel(assets, path)
		if err != nil {
			return err
		}
		name := "assets/" + filepath.ToSlash(relative)
		licence, err := classify(text)
		if err != nil {
			problems = append(problems, name+": "+err.Error())
		}
		s.components = append(s.components, component{name: name, licence: licence, text: text})
		return nil
	})
	return s, problems, err
}

// licenceText joins a package's licence and notice files, in name order.
func licenceText(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var texts []string
	for _, entry := range entries {
		upper := strings.ToUpper(entry.Name())
		if entry.IsDir() || !(isLicenceFile(entry.Name()) || strings.HasPrefix(upper, "NOTICE")) {
			continue
		}
		raw, err := fsx.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", err
		}
		texts = append(texts, normalise(raw))
	}
	if len(texts) == 0 {
		return "", fmt.Errorf("no licence file in %s", dir)
	}
	return strings.Join(texts, "\n\n"), nil
}

func isLicenceFile(name string) bool {
	upper := strings.ToUpper(name)
	return strings.Contains(upper, "LICENSE") || strings.Contains(upper, "LICENCE") || strings.HasPrefix(upper, "COPYING") || strings.Contains(upper, "-OFL")
}

// normalise gives every text LF line ends and no trailing blank space, so
// the notices read the same whichever way git checked a file out.
func normalise(raw []byte) string {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

var errUnrecognised = errors.New("unrecognised licence: read it and, when it is permissive, teach tools/notices its text")

// copyleftMarkers name the copyleft licences by the title their text
// carries. A text that carries one is copyleft whatever else it says.
var copyleftMarkers = []string{
	"gnu general public license",
	"gnu lesser general public license",
	"gnu library general public license",
	"gnu affero general public license",
	"mozilla public license",
	"eclipse public license",
	"common development and distribution license",
	"european union public licen",
	"creative commons attribution-sharealike",
}

// permissiveMarkers name the permissive licences by a sentence only their
// text carries, in the order a text naming several is reported.
var permissiveMarkers = []struct {
	licence string
	markers []string
}{
	{"MIT", []string{"permission is hereby granted, free of charge, to any person obtaining a copy of this software"}},
	{"BSD-3-Clause", []string{"redistribution and use in source and binary forms", "neither the name"}},
	{"BSD-2-Clause", []string{"redistribution and use in source and binary forms"}},
	{"ISC", []string{"distribute this software for any purpose with or without fee is hereby granted"}},
	{"Apache-2.0", []string{"apache license", "version 2.0"}},
	{"OFL-1.1", []string{"sil open font license", "version 1.1"}},
}

// classify names the licence a text grants, or refuses one that is copyleft
// or that it does not recognise.
func classify(text string) (string, error) {
	folded := strings.Join(strings.Fields(strings.ToLower(text)), " ")
	for _, marker := range copyleftMarkers {
		if strings.Contains(folded, marker) {
			return "", fmt.Errorf("copyleft licence (%s)", marker)
		}
	}
	var found []string
	for _, candidate := range permissiveMarkers {
		matches := true
		for _, marker := range candidate.markers {
			matches = matches && strings.Contains(folded, marker)
		}
		if matches && !(candidate.licence == "BSD-2-Clause" && slices.Contains(found, "BSD-3-Clause")) {
			found = append(found, candidate.licence)
		}
	}
	if len(found) == 0 {
		return "", errUnrecognised
	}
	return strings.Join(found, " AND "), nil
}

// permissiveSPDX are the SPDX identifiers an npm package may declare.
var permissiveSPDX = map[string]bool{
	"0BSD": true, "Apache-2.0": true, "BlueOak-1.0.0": true, "BSD-2-Clause": true, "BSD-3-Clause": true,
	"CC0-1.0": true, "ISC": true, "MIT": true, "Unlicense": true, "Zlib": true,
}

var spdxOperator = regexp.MustCompile(`\s+(AND|OR)\s+`)

// permissiveExpression reports whether an SPDX expression grants a
// permissive licence: every term of an AND, at least one of an OR. An
// expression mixing both, or nesting parentheses, is refused for a person to
// read.
func permissiveExpression(expression string) bool {
	inner := strings.TrimSpace(expression)
	if strings.HasPrefix(inner, "(") && strings.HasSuffix(inner, ")") {
		inner = inner[1 : len(inner)-1]
	}
	if inner == "" || strings.ContainsAny(inner, "()") {
		return false
	}
	operators := spdxOperator.FindAllStringSubmatch(inner, -1)
	terms := spdxOperator.Split(inner, -1)
	if slices.ContainsFunc(operators, func(o []string) bool { return o[1] != operators[0][1] }) {
		return false
	}
	if len(operators) > 0 && operators[0][1] == "OR" {
		return slices.ContainsFunc(terms, func(term string) bool { return permissiveSPDX[term] })
	}
	return !slices.ContainsFunc(terms, func(term string) bool { return !permissiveSPDX[term] })
}

// render lists every component with its licence and a reference to its
// text, then each distinct text once.
func render(sections []section) string {
	var list, texts strings.Builder
	index := map[string]int{}
	for _, s := range sections {
		if len(s.components) == 0 {
			continue
		}
		fmt.Fprintf(&list, "%s:\n", s.heading)
		for _, c := range s.components {
			n, ok := index[c.text]
			if !ok {
				n = len(index) + 1
				index[c.text] = n
				fmt.Fprintf(&texts, "\n[%d] %s\n\n%s\n", n, strings.Repeat("-", 72), c.text)
			}
			fmt.Fprintf(&list, "  %s: %s [%d]\n", c.name, c.licence, n)
		}
		list.WriteString("\n")
	}
	return "THIRD-PARTY NOTICES\n\n" +
		"Code Goblins is MIT licensed (see LICENSE). A release also ships the\n" +
		"third-party software below, each under the licence text it refers to.\n" +
		"The board's own notice, naming the Cline Kanban files it adapts, is\n" +
		"frontend/public/assets/NOTICE.txt, served at /assets/NOTICE.txt.\n\n" +
		"Generated by `go run ./tools/notices`; do not edit by hand.\n\n" +
		list.String() + "Licence texts\n" + texts.String()
}

func goOutput(dir string, args ...string) (string, error) {
	out, err := goCommand(dir, args...)
	return strings.TrimSpace(string(out)), err
}

func goCommand(dir string, args ...string) ([]byte, error) {
	cmd := execx.Command("go", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
