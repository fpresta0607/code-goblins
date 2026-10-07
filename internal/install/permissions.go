package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// boardCommands are the cfo commands that only put an item in front of the
// Overlord in the Command Center and run no command of the caller's: a
// question, a run item, whose command runs only when Run is pressed on the
// card that shows it, a review item or Scrawl page, a live presentation, a
// document and a credential request, each with the withdrawal or clear of
// such an item. The only programs they start are fixed ones: cfo review
// starts lavish-axi to serve a page, and cfo auth request asks git whether
// an env file is ignored. Each refuses a caller that is neither the
// registered CFO nor a goblin naming its own task. Filing one is what auto
// mode's classifier refused as a public surface, leaving him nothing to see.
//
// cfo answer is left out: it types the CFO's decision, with free text, into
// a goblin's terminal, and that goblin acts on it under permissions of its
// own, so the classifier keeps judging it as it judges cfo send.
var boardCommands = []string{"question", "run-request", "review", "present", "deliver", "auth request"}

// shellTools are the Claude Code tools a CFO runs cfo through.
var shellTools = []string{"Bash", "PowerShell"}

// rulesRecordSuffix names the file beside a settings file that lists the
// allow rules cfo install added to it, so an uninstall removes exactly those
// and never a rule the adopter wrote, even the same one.
const rulesRecordSuffix = ".cfo-install.rules"

// PermissionRules are the allow rules cfo install adds to the user's Claude
// Code settings, one per board command and shell tool, in the space-star
// form Claude Code's own permission dialog writes. Auto mode resolves a
// narrow allow rule before its classifier runs, unless
// autoMode.classifyAllShell is on, and Claude Code matches each command of a
// compound command on its own, so a rule approves `cfo question ...` and
// never anything chained to it.
func PermissionRules() []string {
	rules := make([]string, 0, len(shellTools)*len(boardCommands))
	for _, tool := range shellTools {
		for _, command := range boardCommands {
			rules = append(rules, tool+"(cfo "+command+" *)")
		}
	}
	return rules
}

// Permissions is how a Claude Code settings file stands on the rules cfo
// install adds.
type Permissions struct {
	// Missing are the rules it lacks, in PermissionRules' order.
	Missing []string
	// ClassifyAllShell is autoMode.classifyAllShell, under which auto mode
	// sets every shell allow rule aside and its classifier judges each
	// command.
	ClassifyAllShell bool
}

// ReadPermissions reads the settings file at path, which may not exist yet.
func ReadPermissions(path string) (Permissions, error) {
	file, err := loadSettings(path)
	if err != nil {
		return Permissions{}, err
	}
	allow, err := file.allowRules()
	if err != nil {
		return Permissions{}, err
	}
	autoMode, _ := file.values["autoMode"].(map[string]any)
	classifyAllShell, _ := autoMode["classifyAllShell"].(bool)
	return Permissions{Missing: missingRules(allow), ClassifyAllShell: classifyAllShell}, nil
}

// missingRules are the rules PermissionRules names that allow does not hold.
func missingRules(allow []string) []string {
	var missing []string
	for _, rule := range PermissionRules() {
		if !slices.ContainsFunc(allow, func(held string) bool { return spaceStar(held) == rule }) {
			missing = append(missing, rule)
		}
	}
	return missing
}

// spaceStar writes a rule's trailing `:*` as the trailing ` *` Claude Code
// reads it as, so a rule an adopter wrote in the older form counts as held.
func spaceStar(rule string) string {
	if body, ok := strings.CutSuffix(rule, ":*)"); ok {
		return body + " *)"
	}
	return rule
}

// allowRules returns the document's permissions.allow list, or nil when it
// has none. A permissions key that is not an object, or an allow list that
// is not an array of strings, is a malformed document, refused as a
// malformed hooks block is.
func (f *settingsFile) allowRules() ([]string, error) {
	permissions, err := f.permissions()
	if err != nil || permissions == nil || permissions["allow"] == nil {
		return nil, err
	}
	list, ok := permissions["allow"].([]any)
	if !ok {
		return nil, fmt.Errorf("install: %s has a \"permissions.allow\" key that is not an array", f.path)
	}
	rules := make([]string, 0, len(list))
	for _, raw := range list {
		rule, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("install: %s has a \"permissions.allow\" entry that is not a string", f.path)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// setAllowRules writes rules as the document's permissions.allow list. An
// empty list leaves with its key, and a permissions block it leaves empty
// leaves too, so an uninstall leaves no hollow scaffolding behind.
func (f *settingsFile) setAllowRules(rules []string) error {
	permissions, err := f.permissions()
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		delete(permissions, "allow")
		if len(permissions) == 0 {
			delete(f.values, "permissions")
		}
		return nil
	}
	if permissions == nil {
		permissions = map[string]any{}
		f.values["permissions"] = permissions
	}
	list := make([]any, 0, len(rules))
	for _, rule := range rules {
		list = append(list, rule)
	}
	permissions["allow"] = list
	return nil
}

// permissions returns the document's permissions object, or nil when it has
// none.
func (f *settingsFile) permissions() (map[string]any, error) {
	raw, ok := f.values["permissions"]
	if !ok || raw == nil {
		return nil, nil
	}
	permissions, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("install: %s has a \"permissions\" key that is not an object", f.path)
	}
	return permissions, nil
}

// readRulesRecord reads the rules cfo install recorded adding to the
// settings file at settingsPath; a missing record is none.
func readRulesRecord(settingsPath string) ([]string, error) {
	data, err := fsx.ReadFile(settingsPath + rulesRecordSuffix)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("install: read %s: %w", settingsPath+rulesRecordSuffix, err)
	}
	var rules []string
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}), &rules); err != nil {
		return nil, fmt.Errorf("install: %s is not a list of rules: %w", settingsPath+rulesRecordSuffix, err)
	}
	return rules, nil
}

// writeRulesRecord records rules as the ones cfo install added to the
// settings file at settingsPath.
func writeRulesRecord(settingsPath string, rules []string) error {
	data, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("install: create %s: %w", filepath.Dir(settingsPath), err)
	}
	if err := fsx.AtomicWriteFile(settingsPath+rulesRecordSuffix, append(data, '\n')); err != nil {
		return fmt.Errorf("install: record the rules added to %s: %w", settingsPath, err)
	}
	return nil
}
