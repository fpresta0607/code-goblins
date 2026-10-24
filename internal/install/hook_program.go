package install

import (
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// HookProgram is the program a release ships beside cfo.exe for a goblin's
// hooks: cfo built as a windowed program. Claude Code starts each hook
// hidden, and Windows gives a console program started that way a console
// host of its own, a second process for every hook, where it starts a
// windowed program alone, which reads the hook's payload and writes its
// answer through the same pipes. Measured on 2026-10-19: twenty shell tool
// calls' hooks started 40 cfo.exe and 40 console hosts, and as the hook
// program 40 and none.
const HookProgram = "cfo-hook.exe"

// copyHookProgram puts the hook program shipped beside the running binary in
// programs, the home's bin. One a hook runs at that moment is moved aside as
// the binary is. A build with none beside it, such as one built from source
// with go build alone, leaves the home's own, which the goblins' hooks run
// only while it is the same build as cfo.exe (HookCommand).
func (s Service) copyHookProgram(programs string, report *reporter) error {
	source := filepath.Join(filepath.Dir(s.Binary), HookProgram)
	data, err := fsx.ReadFile(source)
	if errors.Is(err, fs.ErrNotExist) {
		report.same("hooks", "no hook program beside "+s.Binary+"; goblins' hooks run cfo.exe unless the home's "+HookProgram+" is this build")
		return nil
	}
	if err != nil {
		return fmt.Errorf("install: read the hook program %s: %w", source, err)
	}
	changed, _, err := replaceProgram(filepath.Join(programs, HookProgram), data)
	if err != nil {
		return err
	}
	if !changed {
		report.same("hooks", HookProgram+" in "+programs+" is already this build")
		return nil
	}
	report.change("hooks", fmt.Sprintf("copied %s to %s in %s", source, HookProgram, programs))
	return nil
}

// CarryHookProgram puts the hook program shipped beside binary in programs,
// the home's bin, as an install does, and writes what it did to out. cfo
// update carries it once the candidate serves: until it does, the goblins'
// hooks run cfo.exe, since the hook program left in the home is no longer
// the same build.
func CarryHookProgram(programs, binary string, out io.Writer) error {
	return Service{Binary: binary}.copyHookProgram(programs, &reporter{out: out})
}

// HookCommand is the program a goblin's hooks run from bin, a home's
// programs: its hook program where that is the same build as its cfo.exe,
// and cfo.exe otherwise, so a hook program an update could not replace, or
// one an install from source left, never runs another build's hooks.
func HookCommand(bin string) string {
	program, hook := filepath.Join(bin, "cfo.exe"), filepath.Join(bin, HookProgram)
	if _, err := os.Stat(hook); err != nil || !sameBuild(program, hook) {
		return program
	}
	return hook
}

// sameBuild says whether the programs at a and b were built from the same
// source, with the same Go and the same modules. The hook program is cfo
// built with -H windowsgui, which its build settings record and which is all
// they may differ by.
func sameBuild(a, b string) bool {
	first, err := buildinfo.ReadFile(a)
	if err != nil {
		return false
	}
	second, err := buildinfo.ReadFile(b)
	if err != nil {
		return false
	}
	return buildIdentity(first) == buildIdentity(second)
}

func buildIdentity(info *debug.BuildInfo) string {
	identity := []string{info.GoVersion, info.Main.Path, info.Main.Version, info.Main.Sum}
	for _, setting := range info.Settings {
		value := setting.Value
		if setting.Key == "-ldflags" {
			value = strings.Join(strings.Fields(strings.NewReplacer("-H windowsgui", "", "-H=windowsgui", "").Replace(value)), " ")
			if value == "" {
				continue
			}
		}
		identity = append(identity, setting.Key+"="+value)
	}
	for _, module := range info.Deps {
		identity = append(identity, module.Path+"@"+module.Version+" "+module.Sum)
	}
	return strings.Join(identity, "\n")
}
