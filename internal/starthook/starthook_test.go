package starthook

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// environment makes a Python environment at folder under worktree, as uv
// makes one on Windows, and returns its site-packages folder.
func environment(t *testing.T, worktree, folder string) string {
	t.Helper()
	site := filepath.Join(worktree, folder, "Lib", "site-packages")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(worktree, folder, "pyvenv.cfg"), "home = C:\\Python312\nversion_info = 3.12.10\n")
	return site
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// install writes files into site as distribution installs them, with the
// RECORD that names each.
func install(t *testing.T, site, distribution string, files map[string]string) {
	t.Helper()
	record := ""
	for name, content := range files {
		write(t, filepath.Join(site, filepath.FromSlash(name)), content)
		record += name + ",sha256=AAAA,1\n"
	}
	write(t, filepath.Join(site, distribution+".dist-info", "RECORD"), record+distribution+".dist-info/RECORD,,\n")
}

const (
	distutilsLine = "import os; var = 'SETUPTOOLS_USE_DISTUTILS'; enabled = os.environ.get(var, 'local') == 'local'; enabled and __import__('_distutils_hack').add_shim(); \n"
	coverageLine  = `import sys; exec('import os\n\nif os.getenv("COVERAGE_PROCESS_START") or os.getenv("COVERAGE_PROCESS_CONFIG"):\n try:\n  import coverage\n except:\n  pass\n else:\n  coverage.process_startup(slug="pth")')` + "\r\n"
	finderLine    = "import __editable___shop_0_1_0_finder; __editable___shop_0_1_0_finder.install()"
	stealerLine   = "import os, subprocess; subprocess.Popen(['python', os.path.join(os.path.dirname(__file__), 'init.py')])\n"
)

// On 2026-10-21 a hijacked release of litellm carried a .pth file whose
// import line started a stealer at every Python start. Each environment a
// fresh install makes holds start hooks of its own, which uv, setuptools,
// pywin32, coverage and an editable install write, and those are found only
// when another package wrote them.
func TestFindTellsAPlantedStartHookFromTheOnesAnEnvironmentCarries(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(t *testing.T, worktree string)
		want    []Hook
	}{
		{name: "an environment uv makes", arrange: func(t *testing.T, worktree string) {
			site := environment(t, worktree, ".venv")
			write(t, filepath.Join(site, "_virtualenv.pth"), "import _virtualenv")
			write(t, filepath.Join(site, "_virtualenv.py"), "")
		}},
		{name: "the start hooks setuptools, pywin32 and coverage ship", arrange: func(t *testing.T, worktree string) {
			site := environment(t, worktree, ".venv")
			install(t, site, "setuptools-80.9.0", map[string]string{"distutils-precedence.pth": distutilsLine})
			install(t, site, "pywin32-311", map[string]string{"pywin32.pth": "# .pth file for the PyWin32 extensions\nwin32\nwin32\\lib\nPythonwin\nimport pywin32_bootstrap\n"})
			install(t, site, "coverage-7.13.4", map[string]string{"a1_coverage.pth": coverageLine})
		}},
		{name: "a project installed editable from its folder", arrange: func(t *testing.T, worktree string) {
			site := environment(t, worktree, ".venv")
			install(t, site, "shop-0.1.0", map[string]string{"__editable__.shop-0.1.0.pth": finderLine, "__editable___shop_0_1_0_finder.py": ""})
			write(t, filepath.Join(site, "shop-0.1.0.dist-info", "direct_url.json"), `{"url": "file:///C:/work/shop", "dir_info": {"editable": true}}`)
		}},
		{name: "path lines, comments and a line an import only follows", arrange: func(t *testing.T, worktree string) {
			site := environment(t, worktree, ".venv")
			install(t, site, "paths-1.0", map[string]string{"paths.pth": "# import os\n\n src\n" + filepath.Join(worktree, "src") + "\n  import os; os.system('x')\n"})
		}},
		{name: "a .pth file outside any environment", arrange: func(t *testing.T, worktree string) {
			write(t, filepath.Join(worktree, "tools", "Lib", "site-packages", "init.pth"), stealerLine)
		}},
		{name: "the hijacked release's import line", arrange: func(t *testing.T, worktree string) {
			install(t, environment(t, worktree, ".venv"), "litellm-1.107.0", map[string]string{"litellm_init.pth": stealerLine, "litellm/__init__.py": ""})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/litellm_init.pth", Package: "litellm-1.107.0"}}},
		{name: "an import line after a byte order mark and a path line, with Windows line ends", arrange: func(t *testing.T, worktree string) {
			install(t, environment(t, worktree, ".venv"), "helper-2.0", map[string]string{"helper.pth": string(rune(0xFEFF)) + "helper\r\n" + stealerLine})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/helper.pth", Package: "helper-2.0"}}},
		{name: "a line that runs on its own after a bare carriage return", arrange: func(t *testing.T, worktree string) {
			install(t, environment(t, worktree, ".venv"), "helper-2.0", map[string]string{"helper.pth": "helper\r" + stealerLine})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/helper.pth", Package: "helper-2.0"}}},
		{name: "a line that runs on its own after a form feed, which Python's splitlines breaks at", arrange: func(t *testing.T, worktree string) {
			install(t, environment(t, worktree, ".venv"), "helper-2.0", map[string]string{"helper.pth": "helper\f" + stealerLine})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/helper.pth", Package: "helper-2.0"}}},
		{name: "a known line in a file another package installs", arrange: func(t *testing.T, worktree string) {
			install(t, environment(t, worktree, ".venv"), "tools-1.0", map[string]string{"distutils-precedence.pth": distutilsLine, "_distutils_hack/__init__.py": ""})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/distutils-precedence.pth", Package: "tools-1.0"}}},
		{name: "an editable finder from a package an index served", arrange: func(t *testing.T, worktree string) {
			install(t, environment(t, worktree, ".venv"), "shop-0.1.0", map[string]string{"__editable__.shop-0.1.0.pth": finderLine, "__editable___shop_0_1_0_finder.py": ""})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/__editable__.shop-0.1.0.pth", Package: "shop-0.1.0"}}},
		{name: "customize modules in every form", arrange: func(t *testing.T, worktree string) {
			site := environment(t, worktree, ".venv")
			install(t, site, "tune-1.0", map[string]string{"sitecustomize.py": "import os"})
			install(t, site, "tune2-1.0", map[string]string{"usercustomize/__init__.py": "import os"})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/sitecustomize.py", Package: "tune-1.0"}, {Path: ".venv/Lib/site-packages/usercustomize", Package: "tune2-1.0"}}},
		{name: "a customize module in a folder a path line adds", arrange: func(t *testing.T, worktree string) {
			install(t, environment(t, worktree, ".venv"), "extras-1.0", map[string]string{"extras.pth": "extras_lib\n", "extras_lib/SiteCustomize.pyc": ""})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/extras_lib/SiteCustomize.pyc", Package: "extras-1.0"}}},
		{name: "a hook no installed package names", arrange: func(t *testing.T, worktree string) {
			write(t, filepath.Join(environment(t, worktree, ".venv"), "zz.pth"), stealerLine)
		}, want: []Hook{{Path: ".venv/Lib/site-packages/zz.pth"}}},
		{name: "environments under other names and in a project's subfolder", arrange: func(t *testing.T, worktree string) {
			install(t, environment(t, worktree, "env"), "a-1.0", map[string]string{"a.pth": stealerLine})
			install(t, environment(t, worktree, filepath.Join("backend", ".venv")), "b-1.0", map[string]string{"b.pth": stealerLine})
			install(t, environment(t, worktree, filepath.Join("services", "api", ".venv")), "c-1.0", map[string]string{"c.pth": stealerLine})
		}, want: []Hook{{Path: "backend/.venv/Lib/site-packages/b.pth", Package: "b-1.0"}, {Path: "env/Lib/site-packages/a.pth", Package: "a-1.0"}, {Path: "services/api/.venv/Lib/site-packages/c.pth", Package: "c-1.0"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			worktree := t.TempDir()
			test.arrange(t, worktree)

			// Act
			hooks, err := Find(worktree)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			for index := range hooks {
				relative, err := filepath.Rel(worktree, hooks[index].Path)
				if err != nil {
					t.Fatal(err)
				}
				hooks[index].Path = filepath.ToSlash(relative)
			}
			slices.SortFunc(hooks, func(a, b Hook) int { return strings.Compare(a.Path, b.Path) })
			if !slices.Equal(hooks, test.want) {
				t.Fatalf("hooks = %+v, want %+v", hooks, test.want)
			}
		})
	}
}

func TestFindInAWorktreeThatIsGoneFindsNothing(t *testing.T) {
	// Act
	hooks, err := Find(filepath.Join(t.TempDir(), "gone"))

	// Assert
	if err != nil || len(hooks) != 0 {
		t.Fatalf("hooks = %+v, %v, want none", hooks, err)
	}
}
