package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	mitText    = "Copyright (c) 2026 Someone\n\nPermission is hereby granted, free of charge, to any person obtaining a copy\nof this software and associated documentation files (the \"Software\"), to deal"
	bsd3Text   = "Redistribution and use in source and binary forms, with or without\nmodification, are permitted provided that ...\n   * Neither the name of Google LLC nor the names of its\ncontributors may be used"
	bsd2Text   = "Redistribution and use in source and binary forms, with or without\nmodification, are permitted provided that the following conditions are met"
	iscText    = "Permission to use, copy, modify, and distribute this software for any\npurpose with or without fee is hereby granted, provided that"
	iscOrText  = "Permission to use, copy, modify, and/or distribute this software for any\npurpose with or without fee is hereby granted, provided that"
	apacheText = "                                 Apache License\n                           Version 2.0, January 2004"
	oflText    = "Permission is hereby granted, free of charge, to any person obtaining\na copy of the Font Software ... SIL OPEN FONT LICENSE Version 1.1 - 26 February 2007"
	gplText    = "                    GNU GENERAL PUBLIC LICENSE\n                       Version 3, 29 June 2007"
	lgplText   = "GNU LESSER GENERAL PUBLIC LICENSE Version 2.1"
	mplText    = "Mozilla Public License Version 2.0\n=================================="
	agplText   = "GNU AFFERO GENERAL PUBLIC LICENSE"
	eplText    = "Eclipse Public License - v 2.0"
)

func TestClassifyNamesPermissiveLicencesAndRefusesTheRest(t *testing.T) {
	cases := []struct {
		name, text, licence, refusal string
	}{
		{"MIT", mitText, "MIT", ""},
		{"BSD-3-Clause", bsd3Text, "BSD-3-Clause", ""},
		{"BSD-2-Clause", bsd2Text, "BSD-2-Clause", ""},
		{"ISC", iscText, "ISC", ""},
		{"ISC and/or wording", iscOrText, "ISC", ""},
		{"Apache-2.0", apacheText, "Apache-2.0", ""},
		{"OFL is not also read as MIT", oflText, "OFL-1.1", ""},
		{"a file under two licences names both", mitText + "\n\n" + apacheText, "MIT AND Apache-2.0", ""},
		{"GPL", gplText, "", "copyleft"},
		{"LGPL", lgplText, "", "copyleft"},
		{"MPL", mplText, "", "copyleft"},
		{"AGPL", agplText, "", "copyleft"},
		{"EPL", eplText, "", "copyleft"},
		{"copyleft wins over a permissive text beside it", mitText + "\n\n" + gplText, "", "copyleft"},
		{"unknown text", "All rights reserved.", "", "unrecognised"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			licence, err := classify(tc.text)

			if tc.refusal == "" {
				if err != nil || licence != tc.licence {
					t.Fatalf("classify = %q, %v, want %q", licence, err, tc.licence)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.refusal) {
				t.Fatalf("classify = %q, %v, want a refusal naming %q", licence, err, tc.refusal)
			}
		})
	}
}

func TestPermissiveExpressionReadsSPDX(t *testing.T) {
	cases := []struct {
		expression string
		want       bool
	}{
		{"MIT", true},
		{"(MIT OR GPL-3.0-only)", true},
		{"MIT OR Apache-2.0", true},
		{"MIT AND ISC", true},
		{"MIT AND GPL-2.0-only", false},
		{"GPL-2.0-only OR LGPL-3.0-only", false},
		{"MPL-2.0", false},
		{"LicenseRef-Proprietary", false},
		{"", false},
		{"MIT OR (Apache-2.0 AND GPL-3.0-only)", false},
		{"MIT AND ISC OR GPL-3.0-only", false},
	}
	for _, tc := range cases {
		if got := permissiveExpression(tc.expression); got != tc.want {
			t.Errorf("permissiveExpression(%q) = %t, want %t", tc.expression, got, tc.want)
		}
	}
}

func TestGoModulesListsEachShippedModuleOnceWithTheStandardLibrary(t *testing.T) {
	listed := `{"ImportPath":"fmt","Standard":true}
{"ImportPath":"os","Standard":true}
{"ImportPath":"golang.org/x/sys/windows","Module":{"Path":"golang.org/x/sys","Version":"v0.48.0","Dir":"C:/cache/sys"}}
{"ImportPath":"golang.org/x/sys/windows/registry","Module":{"Path":"golang.org/x/sys","Version":"v0.48.0","Dir":"C:/cache/sys"}}
{"ImportPath":"example.com/fork","Module":{"Path":"example.com/upstream","Version":"v1.0.0","Dir":"C:/cache/upstream","Replace":{"Path":"example.com/fork","Version":"v1.0.1","Dir":"C:/cache/fork"}}}
{"ImportPath":"github.com/fpresta0607/code-goblins/cmd/cfo","Module":{"Path":"github.com/fpresta0607/code-goblins","Main":true,"Dir":"C:/dev/repo"}}
`

	modules, err := goModules(strings.NewReader(listed))

	if err != nil {
		t.Fatal(err)
	}
	want := []module{
		{},
		{Path: "example.com/upstream", Version: "v1.0.1", Dir: "C:/cache/fork"},
		{Path: "golang.org/x/sys", Version: "v0.48.0", Dir: "C:/cache/sys"},
	}
	if len(modules) != len(want) {
		t.Fatalf("modules = %+v, want %+v", modules, want)
	}
	for i := range want {
		if modules[i] != want[i] {
			t.Errorf("module %d = %+v, want %+v", i, modules[i], want[i])
		}
	}
}

func TestGoComponentsRefusesACopyleftModuleAndReadsTheStandardLibraryFromGOROOT(t *testing.T) {
	goroot := writeFiles(t, map[string]string{"LICENSE": bsd3Text})
	permissive := writeFiles(t, map[string]string{"LICENSE.txt": iscText, "NOTICE": "Portions by Someone."})
	copyleft := writeFiles(t, map[string]string{"COPYING": gplText})

	s, problems, err := goComponents(goroot, []module{{}, {Path: "example.com/ok", Version: "v1.0.0", Dir: permissive}, {Path: "example.com/gpl", Version: "v2.0.0", Dir: copyleft}})

	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || !strings.HasPrefix(problems[0], "example.com/gpl v2.0.0: copyleft") {
		t.Fatalf("problems = %q, want one naming example.com/gpl as copyleft", problems)
	}
	if s.components[0].name != "Go standard library" || s.components[0].licence != "BSD-3-Clause" {
		t.Errorf("first component = %+v, want the standard library under BSD-3-Clause", s.components[0])
	}
	if !strings.Contains(s.components[1].text, "Portions by Someone.") {
		t.Errorf("the ISC module's text %q leaves out its NOTICE", s.components[1].text)
	}
}

func TestGoComponentsFailsForAModuleWithNoLicenceFile(t *testing.T) {
	_, _, err := goComponents(t.TempDir(), []module{{Path: "example.com/bare", Version: "v1.0.0", Dir: writeFiles(t, map[string]string{"go.mod": "module example.com/bare"})}})

	if err == nil || !strings.Contains(err.Error(), "example.com/bare v1.0.0: no licence file") {
		t.Fatalf("err = %v, want one naming the module without a licence file", err)
	}
}

func TestNPMComponentsListsOnlyBundledPackagesAndRefusesCopyleftOnes(t *testing.T) {
	frontend := writeFiles(t, map[string]string{
		"package-lock.json": `{"lockfileVersion":3,"packages":{
			"":{"name":"board"},
			"node_modules/react":{"version":"19.3.0","license":"MIT"},
			"node_modules/lightningcss":{"version":"1.33.0","license":"MPL-2.0","dev":true},
			"node_modules/fsevents":{"version":"2.3.3","license":"MIT","devOptional":true},
			"node_modules/react/node_modules/strict":{"version":"1.0.0","license":"GPL-3.0-only"},
			"node_modules/liar":{"version":"1.0.0","license":"MIT"},
			"node_modules/eula":{"version":"2.0.0","license":"MIT"}
		}}`,
		"node_modules/react/LICENSE":                     mitText,
		"node_modules/react/node_modules/strict/LICENSE": gplText,
		"node_modules/liar/LICENSE.md":                   mplText,
		"node_modules/eula/LICENSE":                      "Business Source License 1.1\n\nYou may not use the Licensed Work in production.",
	})

	s, problems, err := npmComponents(frontend)

	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range s.components {
		names = append(names, c.name)
	}
	if got := strings.Join(names, ", "); got != "npm eula 2.0.0, npm liar 1.0.0, npm react 19.3.0, npm strict 1.0.0" {
		t.Errorf("components = %s, want the four bundled packages and no development tool", got)
	}
	joined := strings.Join(problems, "\n")
	if len(problems) != 3 || !strings.Contains(joined, `npm strict 1.0.0: declares "GPL-3.0-only"`) || !strings.Contains(joined, "npm liar 1.0.0: copyleft") || !strings.Contains(joined, "npm eula 2.0.0: "+errUnrecognised.Error()) {
		t.Errorf("problems = %q, want strict refused by its declaration, liar by its copyleft licence file and eula by its unrecognised one", problems)
	}
}

func TestNPMComponentsAsksForNPMCIWhenAPackageIsNotInstalled(t *testing.T) {
	frontend := writeFiles(t, map[string]string{"package-lock.json": `{"packages":{"node_modules/react":{"version":"19.3.0","license":"MIT"}}}`})

	_, _, err := npmComponents(frontend)

	if err == nil || !strings.Contains(err.Error(), "run npm ci in frontend first") {
		t.Fatalf("err = %v, want one asking for npm ci", err)
	}
}

func TestAssetComponentsSkipCopiesOfListedLicences(t *testing.T) {
	assets := writeFiles(t, map[string]string{
		"react-LICENSE.txt":        strings.ReplaceAll(mitText, "\n", "\r\n") + "\r\n",
		"fonts/nunito-OFL.txt":     oflText,
		"fonts/nunito.woff2":       "font bytes",
		"goblin-app.png":           "image bytes",
		"cline-kanban-LICENSE.txt": apacheText,
	})

	s, problems, err := assetComponents(assets, []component{{name: "npm react 19.3.0", licence: "MIT", text: mitText}})

	if err != nil || len(problems) != 0 {
		t.Fatalf("assetComponents = %v, %q", err, problems)
	}
	var listed []string
	for _, c := range s.components {
		listed = append(listed, c.name+"="+c.licence)
	}
	if got := strings.Join(listed, ", "); got != "assets/cline-kanban-LICENSE.txt=Apache-2.0, assets/fonts/nunito-OFL.txt=OFL-1.1" {
		t.Errorf("assets = %s, want the Apache and OFL files and not the CRLF copy of React's licence", got)
	}
}

func TestRenderListsEveryComponentAndEachTextOnce(t *testing.T) {
	notices := render([]section{
		{heading: "Go", components: []component{{name: "Go standard library", licence: "BSD-3-Clause", text: bsd3Text}, {name: "golang.org/x/sys v0.48.0", licence: "BSD-3-Clause", text: bsd3Text}}},
		{heading: "npm"},
		{heading: "Assets", components: []component{{name: "assets/nunito-OFL.txt", licence: "OFL-1.1", text: oflText}}},
	})

	for _, line := range []string{"  Go standard library: BSD-3-Clause [1]\n", "  golang.org/x/sys v0.48.0: BSD-3-Clause [1]\n", "  assets/nunito-OFL.txt: OFL-1.1 [2]\n"} {
		if !strings.Contains(notices, line) {
			t.Errorf("notices leave out %q:\n%s", line, notices)
		}
	}
	if strings.Count(notices, "Neither the name of Google LLC") != 1 {
		t.Errorf("the shared BSD text appears %d times, want once", strings.Count(notices, "Neither the name of Google LLC"))
	}
	if strings.Contains(notices, "npm:") {
		t.Error("an empty section still has its heading")
	}
}

func TestApplyWritesChecksAndRefuses(t *testing.T) {
	t.Run("writes the notices", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), noticesFile)

		if err := apply(path, "notices\n", nil, false); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); string(got) != "notices\n" {
			t.Errorf("file = %q", got)
		}
	})
	t.Run("a current file passes the check", func(t *testing.T) {
		path := filepath.Join(writeFiles(t, map[string]string{noticesFile: "notices\n"}), noticesFile)

		if err := apply(path, "notices\n", nil, true); err != nil {
			t.Fatal(err)
		}
	})
	for name, existing := range map[string]map[string]string{"stale": {noticesFile: "old\n"}, "missing": {}} {
		t.Run("a "+name+" file fails the check and is left alone", func(t *testing.T) {
			path := filepath.Join(writeFiles(t, existing), noticesFile)

			err := apply(path, "notices\n", nil, true)

			if err == nil || !strings.Contains(err.Error(), "run go run ./tools/notices") {
				t.Fatalf("err = %v, want the stale file named with its fix", err)
			}
			if got, _ := os.ReadFile(path); string(got) != existing[noticesFile] {
				t.Errorf("the check changed the file to %q", got)
			}
		})
	}
	t.Run("a copyleft component refuses both modes and writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, noticesFile)
		for _, check := range []bool{false, true} {
			err := apply(path, "notices\n", []string{"example.com/gpl v2.0.0: copyleft licence (gnu general public license)"}, check)

			if err == nil || !strings.Contains(err.Error(), "example.com/gpl v2.0.0") {
				t.Errorf("check=%t: err = %v, want the copyleft module named", check, err)
			}
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("a refused run wrote %s", noticesFile)
		}
	})
}

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
