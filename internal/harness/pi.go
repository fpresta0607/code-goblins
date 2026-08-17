package harness

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type piCapabilities struct {
	model     bool
	thinking  bool
	extension bool
	tuiMode   bool
	efforts   map[string]bool
}

type piAdapter struct {
	mu           sync.RWMutex
	capabilities *piCapabilities
}

func (*piAdapter) Kind() Kind {
	return Pi
}

func (adapter *piAdapter) Validate(ctx context.Context, runner execx.Runner) error {
	result, err := validateExecutable(ctx, runner, "pi", "--help")
	if err != nil {
		return err
	}
	capabilities := parsePiHelp(string(result.Stdout))
	adapter.mu.Lock()
	adapter.capabilities = &capabilities
	adapter.mu.Unlock()
	return nil
}

func (adapter *piAdapter) Build(spec LaunchSpec) (Launch, error) {
	capabilities, err := adapter.capabilitiesSnapshot()
	if err != nil {
		return Launch{}, err
	}
	launch, err := buildBase(spec)
	if err != nil {
		return Launch{}, err
	}
	// Herdr's Windows agent start uses Start-Process -FilePath, which cannot
	// execute the npm .cmd shim pi installs as; pi launches typed instead.
	launch.TypedLaunch = true
	launch.Executable = "pi"
	// Pi asks "Trust project folder?" in every fresh worktree. Unlike kimi's
	// dialog, pi highlights "Trust" by default, so a bare Enter confirms it.
	launch.ConfirmMarkers = []string{"Trust project folder?"}
	launch.ConfirmKeys = []string{"enter"}
	if capabilities.tuiMode {
		launch.Args = append(launch.Args, "--tui-mode", "regular")
	}
	if hasValue(spec.Model) {
		if !capabilities.model {
			return Launch{}, errors.New("harness: installed Pi does not advertise --model")
		}
		launch.Args = append(launch.Args, "--model", spec.Model)
	}
	if hasValue(spec.Effort) {
		if !validSharedEffort(spec.Effort) || !capabilities.thinking || !capabilities.efforts[spec.Effort] {
			return Launch{}, fmt.Errorf("harness: installed Pi does not support effort %q", spec.Effort)
		}
		launch.Args = append(launch.Args, "--thinking", spec.Effort)
	}
	if spec.PiExtensionPath != "" {
		if !capabilities.extension {
			return Launch{}, errors.New("harness: installed Pi does not advertise --extension")
		}
		launch.Args = append(launch.Args, "--extension", spec.PiExtensionPath)
	}
	return launch, nil
}

func (adapter *piAdapter) capabilitiesSnapshot() (piCapabilities, error) {
	adapter.mu.RLock()
	defer adapter.mu.RUnlock()
	if adapter.capabilities == nil {
		return piCapabilities{}, errors.New("harness: validate Pi with --help before building a launch")
	}
	copy := *adapter.capabilities
	copy.efforts = make(map[string]bool, len(adapter.capabilities.efforts))
	for effort, supported := range adapter.capabilities.efforts {
		copy.efforts[effort] = supported
	}
	return copy, nil
}

func parsePiHelp(help string) piCapabilities {
	capabilities := piCapabilities{
		model:     optionAdvertised(help, "--model"),
		thinking:  optionAdvertised(help, "--thinking"),
		extension: optionAdvertised(help, "--extension"),
		tuiMode:   optionAdvertised(help, "--tui-mode"),
		efforts:   map[string]bool{},
	}
	thinkingLine, advertised := advertisedOptionLine(help, "--thinking")
	if !advertised {
		return capabilities
	}
	lower := strings.ToLower(thinkingLine)
	for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
		capabilities.efforts[effort] = containsToken(lower, effort)
	}
	return capabilities
}

func optionAdvertised(help, option string) bool {
	_, advertised := advertisedOptionLine(help, option)
	return advertised
}

func advertisedOptionLine(help, option string) (string, bool) {
	inOptions := false
	for _, line := range strings.Split(help, "\n") {
		if strings.TrimSpace(line) == "Options:" {
			inOptions = true
			continue
		}
		if !inOptions {
			continue
		}
		if optionSectionHeading(line) {
			return "", false
		}
		if optionDeclaration(line, option) {
			return line, true
		}
	}
	return "", false
}

func optionSectionHeading(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed != "" && len(line) == len(strings.TrimLeft(line, " \t")) && strings.HasSuffix(trimmed, ":")
}

func optionDeclaration(line, option string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmed, option) {
		return optionBoundary(trimmed, len(option))
	}
	separator := ", " + option
	index := strings.Index(trimmed, separator)
	if index < 0 {
		return false
	}
	if index == 0 || !strings.HasPrefix(trimmed, "-") {
		return false
	}
	return optionBoundary(trimmed, index+len(separator))
}

func optionBoundary(line string, index int) bool {
	return index == len(line) || line[index] == ' ' || line[index] == '\t' || line[index] == ',' || line[index] == '='
}

func containsToken(line, token string) bool {
	for offset := 0; ; {
		index := strings.Index(line[offset:], token)
		if index < 0 {
			return false
		}
		index += offset
		end := index + len(token)
		beforeOK := index == 0 || !isTokenCharacter(line[index-1])
		afterOK := end == len(line) || !isTokenCharacter(line[end])
		if beforeOK && afterOK {
			return true
		}
		offset = end
	}
}

func isTokenCharacter(character byte) bool {
	return (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_'
}

// Control stops pi with /quit. pi advertises no resume flag, so a switch that
// keeps pi still restarts it cold and hands it the written handoff instead.
func (*piAdapter) Control() Control {
	return Control{
		StopKeys:    []string{"escape"},
		StopCommand: "/quit",
	}
}
