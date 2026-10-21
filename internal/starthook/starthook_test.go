package starthook

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// makeEnvironment makes a Python environment at folder under worktree, as uv
// makes one on Windows, and returns its site-packages folder.
func makeEnvironment(t *testing.T, worktree, folder string) string {
	t.Helper()
	site := filepath.Join(worktree, folder, "Lib", "site-packages")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(worktree, folder, "pyvenv.cfg"), "home = C:\\Python312\nimplementation = CPython\nuv = 0.9.30\nversion_info = 3.12.10\n")
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
// RECORD that names each relative to site.
func install(t *testing.T, site, distribution string, files map[string]string) {
	t.Helper()
	record := ""
	for name, content := range files {
		write(t, filepath.Join(site, filepath.FromSlash(name)), content)
		record += name + ",sha256=AAAA,1\n"
	}
	write(t, filepath.Join(site, distribution+".dist-info", "RECORD"), record+distribution+".dist-info/RECORD,,\n")
}

// editable marks distribution in site as installed editable from folder.
func editable(t *testing.T, site, distribution, folder string) {
	t.Helper()
	address := (&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(folder), "/")}).String()
	write(t, filepath.Join(site, distribution+".dist-info", "direct_url.json"), `{"url": "`+address+`", "dir_info": {"editable": true}}`)
}

const (
	distutilsLine = "import os; var = 'SETUPTOOLS_USE_DISTUTILS'; enabled = os.environ.get(var, 'local') == 'local'; enabled and __import__('_distutils_hack').add_shim(); \n"
	coverageLine  = `import sys; exec('import os\n\nif os.getenv("COVERAGE_PROCESS_START") or os.getenv("COVERAGE_PROCESS_CONFIG"):\n try:\n  import coverage\n except:\n  pass\n else:\n  coverage.process_startup(slug="pth")')` + "\r\n"
	pytestCovLine = `import os, sys;exec('if \'COV_CORE_SOURCE\' in os.environ:\n try:\n  from pytest_cov.embed import init\n  init()\n except Exception as exc:\n  sys.stderr.write(\n   "pytest-cov: Failed to setup subprocess coverage. "\n   "Environ: {0!r} "\n   "Exception: {1!r}\\n".format(\n    dict((k, v) for k, v in os.environ.items() if k.startswith(\'COV_CORE\')),\n    exc\n   )\n  )\n')` + "\n"
	finderLine    = "import __editable___shop_0_1_0_finder; __editable___shop_0_1_0_finder.install()"
	stealerLine   = "import os, subprocess; subprocess.Popen(['python', os.path.join(os.path.dirname(__file__), 'init.py')])\n"
)

// On 2026-10-21 a hijacked release of litellm carried a .pth file whose
// import line started a stealer at every Python start. Each environment a
// fresh install makes holds start hooks of its own, which uv, setuptools,
// pywin32, coverage and an editable install of the project write, and those
// are found only when another package wrote them.
func TestFindTellsAPlantedStartHookFromTheOnesAnEnvironmentCarries(t *testing.T) {
	for _, test := range []struct {
		name         string
		arrange      func(t *testing.T, worktree string)
		isInstalling bool
		want         []Hook
	}{
		{name: "an environment uv makes", arrange: func(t *testing.T, worktree string) {
			site := makeEnvironment(t, worktree, ".venv")
			write(t, filepath.Join(site, "_virtualenv.pth"), "import _virtualenv")
			write(t, filepath.Join(site, "_virtualenv.py"), "")
		}},
		{name: "the start hooks setuptools, pywin32 and coverage ship", arrange: func(t *testing.T, worktree string) {
			site := makeEnvironment(t, worktree, ".venv")
			install(t, site, "setuptools-80.9.0", map[string]string{"distutils-precedence.pth": distutilsLine})
			install(t, site, "pywin32-311", map[string]string{"pywin32.pth": "# .pth file for the PyWin32 extensions\nwin32\nwin32\\lib\nPythonwin\nimport pywin32_bootstrap\n"})
			install(t, site, "coverage-7.13.4", map[string]string{"a1_coverage.pth": coverageLine})
			install(t, site, "pytest_cov-6.3.0", map[string]string{"pytest-cov.pth": pytestCovLine})
		}},
		{name: "the project installed editable from its folder, by setuptools and by meson-python", arrange: func(t *testing.T, worktree string) {
			site := makeEnvironment(t, worktree, ".venv")
			install(t, site, "shop-0.1.0", map[string]string{"__editable__.shop-0.1.0.pth": finderLine, "__editable___shop_0_1_0_finder.py": ""})
			editable(t, site, "shop-0.1.0", worktree)
			install(t, site, "engine-2.0", map[string]string{"engine-editable.pth": "import _engine_editable_loader\n", "_engine_editable_loader.py": ""})
			editable(t, site, "engine-2.0", filepath.Join(worktree, "engine"))
		}},
		{name: "path lines, comments and a line an import only follows", arrange: func(t *testing.T, worktree string) {
			site := makeEnvironment(t, worktree, ".venv")
			install(t, site, "paths-1.0", map[string]string{"paths.pth": "# import os\n\n src\n" + filepath.Join(worktree, "src") + "\n  import os; os.system('x')\n"})
		}},
		{name: "a .pth file outside any environment", arrange: func(t *testing.T, worktree string) {
			write(t, filepath.Join(worktree, "tools", "Lib", "site-packages", "init.pth"), stealerLine)
		}},
		{name: "the hijacked release's import line", arrange: func(t *testing.T, worktree string) {
			install(t, makeEnvironment(t, worktree, ".venv"), "litellm-1.107.0", map[string]string{"litellm_init.pth": stealerLine, "litellm/__init__.py": ""})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/litellm_init.pth", Package: "litellm-1.107.0"}}},
		{name: "the line as a data file in the environment's own folder, which Python on Windows reads too", arrange: func(t *testing.T, worktree string) {
			install(t, makeEnvironment(t, worktree, ".venv"), "litellm-1.107.0", map[string]string{"../../litellm_init.pth": stealerLine, "../../sitecustomize.py": "import os"})
		}, want: []Hook{{Path: ".venv/litellm_init.pth", Package: "litellm-1.107.0"}, {Path: ".venv/sitecustomize.py", Package: "litellm-1.107.0"}}},
		{name: "an import line after a byte order mark and a path line, with Windows line ends", arrange: func(t *testing.T, worktree string) {
			install(t, makeEnvironment(t, worktree, ".venv"), "helper-2.0", map[string]string{"helper.pth": string(rune(0xFEFF)) + "helper\r\n" + stealerLine})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/helper.pth", Package: "helper-2.0"}}},
		{name: "a line that runs on its own after a bare carriage return", arrange: func(t *testing.T, worktree string) {
			install(t, makeEnvironment(t, worktree, ".venv"), "helper-2.0", map[string]string{"helper.pth": "helper\r" + stealerLine})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/helper.pth", Package: "helper-2.0"}}},
		{name: "a line that runs on its own after a form feed, which Python's splitlines breaks at", arrange: func(t *testing.T, worktree string) {
			install(t, makeEnvironment(t, worktree, ".venv"), "helper-2.0", map[string]string{"helper.pth": "helper\f" + stealerLine})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/helper.pth", Package: "helper-2.0"}}},
		{name: "known lines in files another package installs", arrange: func(t *testing.T, worktree string) {
			install(t, makeEnvironment(t, worktree, ".venv"), "tools-1.0", map[string]string{"distutils-precedence.pth": distutilsLine, "pywin32.pth": "import pywin32_bootstrap\n", "a1_coverage.pth": coverageLine, "pytest-cov.pth": pytestCovLine, "_distutils_hack/__init__.py": ""})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/a1_coverage.pth", Package: "tools-1.0"}, {Path: ".venv/Lib/site-packages/distutils-precedence.pth", Package: "tools-1.0"}, {Path: ".venv/Lib/site-packages/pytest-cov.pth", Package: "tools-1.0"}, {Path: ".venv/Lib/site-packages/pywin32.pth", Package: "tools-1.0"}}},
		{name: "uv's own line in an environment python -m venv made, which writes none", arrange: func(t *testing.T, worktree string) {
			site := makeEnvironment(t, worktree, ".venv")
			write(t, filepath.Join(worktree, ".venv", "pyvenv.cfg"), "home = C:\\Python312\ninclude-system-site-packages = false\nversion = 3.12.10\n")
			write(t, filepath.Join(site, "_virtualenv.pth"), "import _virtualenv")
		}, want: []Hook{{Path: ".venv/Lib/site-packages/_virtualenv.pth"}}},
		{name: "an editable finder from a package an index served", arrange: func(t *testing.T, worktree string) {
			install(t, makeEnvironment(t, worktree, ".venv"), "shop-0.1.0", map[string]string{"__editable__.shop-0.1.0.pth": finderLine, "__editable___shop_0_1_0_finder.py": ""})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/__editable__.shop-0.1.0.pth", Package: "shop-0.1.0"}}},
		{name: "an editable install from a folder outside the worktree", arrange: func(t *testing.T, worktree string) {
			site := makeEnvironment(t, worktree, ".venv")
			install(t, site, "shop-0.1.0", map[string]string{"__editable__.shop-0.1.0.pth": finderLine, "__editable___shop_0_1_0_finder.py": ""})
			editable(t, site, "shop-0.1.0", filepath.Join(filepath.Dir(worktree), "elsewhere"))
		}, want: []Hook{{Path: ".venv/Lib/site-packages/__editable__.shop-0.1.0.pth", Package: "shop-0.1.0"}}},
		{name: "customize modules in every form", arrange: func(t *testing.T, worktree string) {
			site := makeEnvironment(t, worktree, ".venv")
			install(t, site, "tune-1.0", map[string]string{"sitecustomize.py": "import os"})
			install(t, site, "tune2-1.0", map[string]string{"usercustomize/__init__.py": "import os"})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/sitecustomize.py", Package: "tune-1.0"}, {Path: ".venv/Lib/site-packages/usercustomize", Package: "tune2-1.0"}}},
		{name: "a customize module in a folder a path line adds", arrange: func(t *testing.T, worktree string) {
			install(t, makeEnvironment(t, worktree, ".venv"), "extras-1.0", map[string]string{"extras.pth": "extras_lib\n", "extras_lib/SiteCustomize.pyc": ""})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/extras_lib/SiteCustomize.pyc", Package: "extras-1.0"}}},
		{name: "a customize module in a folder a path line names with a trailing no-break space, which Python strips", arrange: func(t *testing.T, worktree string) {
			install(t, makeEnvironment(t, worktree, ".venv"), "extras-1.0", map[string]string{"extras.pth": "extras_lib" + string(rune(0xA0)) + "\n", "extras_lib/sitecustomize.py": ""})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/extras_lib/sitecustomize.py", Package: "extras-1.0"}}},
		{name: "a customize module in a folder a path line names by the site's drive with no root, and by a root with no drive", arrange: func(t *testing.T, worktree string) {
			site := makeEnvironment(t, worktree, ".venv")
			rooted := strings.TrimPrefix(filepath.Join(site, "rooted_lib"), filepath.VolumeName(site))
			install(t, site, "extras-1.0", map[string]string{"extras.pth": filepath.VolumeName(site) + "drive_lib\n" + rooted + "\n", "drive_lib/sitecustomize.py": "", "rooted_lib/usercustomize.py": ""})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/drive_lib/sitecustomize.py", Package: "extras-1.0"}, {Path: ".venv/Lib/site-packages/rooted_lib/usercustomize.py", Package: "extras-1.0"}}},
		{name: "a customize module in a zip archive a path line adds, which zipimport serves", arrange: func(t *testing.T, worktree string) {
			install(t, makeEnvironment(t, worktree, ".venv"), "extras-1.0", map[string]string{"extras.pth": "extras.zip\nplain.zip\n", "extras.zip": zipped(t, "sitecustomize.py"), "plain.zip": zipped(t, "extras/__init__.py")})
		}, want: []Hook{{Path: ".venv/Lib/site-packages/extras.zip", Package: "extras-1.0"}}},
		{name: "a hook no installed package names", arrange: func(t *testing.T, worktree string) {
			write(t, filepath.Join(makeEnvironment(t, worktree, ".venv"), "zz.pth"), stealerLine)
		}, want: []Hook{{Path: ".venv/Lib/site-packages/zz.pth"}}},
		{name: "a hook no installed package names yet while an install still writes", arrange: func(t *testing.T, worktree string) {
			write(t, filepath.Join(makeEnvironment(t, worktree, ".venv"), "zz.pth"), stealerLine)
		}, isInstalling: true, want: []Hook{{Path: ".venv/Lib/site-packages/zz.pth", Installing: true}}},
		{name: "environments under other names and in a project's subfolder", arrange: func(t *testing.T, worktree string) {
			install(t, makeEnvironment(t, worktree, "env"), "a-1.0", map[string]string{"a.pth": stealerLine})
			install(t, makeEnvironment(t, worktree, filepath.Join("backend", ".venv")), "b-1.0", map[string]string{"b.pth": stealerLine})
			install(t, makeEnvironment(t, worktree, filepath.Join("services", "api", ".venv")), "c-1.0", map[string]string{"c.pth": stealerLine})
		}, want: []Hook{{Path: "backend/.venv/Lib/site-packages/b.pth", Package: "b-1.0"}, {Path: "env/Lib/site-packages/a.pth", Package: "a-1.0"}, {Path: "services/api/.venv/Lib/site-packages/c.pth", Package: "c-1.0"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			worktree := t.TempDir()
			test.arrange(t, worktree)
			if !test.isInstalling {
				settle(t, worktree)
			}

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

// zipped is a zip archive holding one empty file named name.
func zipped(t *testing.T, name string) string {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	if _, err := writer.Create(name); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.String()
}

// settle dates every folder under root two minutes back, as an install that
// has ended leaves them.
func settle(t *testing.T, root string) {
	t.Helper()
	past := time.Now().Add(-2 * time.Minute)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return err
		}
		return os.Chtimes(path, past, past)
	})
	if err != nil {
		t.Fatal(err)
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
