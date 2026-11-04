package supervisor

import (
	"cmp"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/harnessmap"
)

// codexMaxSkillDepth is how deep below a skills folder Codex looks for a
// SKILL.md, the file itself counted, so folder/SKILL.md is at depth 2.
const codexMaxSkillDepth = 6

// codexMaxSkillName is the longest skill name Codex loads.
const codexMaxSkillName = 64

// codexMentionName is a name Codex finds in a $name mention.
var codexMentionName = regexp.MustCompile(`^[A-Za-z0-9_:-]+$`)

// codexPluginManifests are where a plugin keeps the manifest Codex reads its
// name and skills from, in the order Codex looks.
var codexPluginManifests = []string{".codex-plugin", ".claude-plugin", ".cursor-plugin"}

// codexSkill is one SKILL.md Codex loads.
type codexSkill struct {
	name, description, path string
}

// codexSkills reads the skills Codex offers a session running in folder, as
// Codex 0.160.0 loads them: the .codex/skills and .agents/skills folders from
// the project's root down to folder, the user's skills in its own folder and
// in the shared skills folder, its bundled skills unless its configuration
// turns them off, and the skills of each plugin the configuration turns on,
// each behind its plugin's name and a colon. A skill the configuration's
// [[skills.config]] rules turn off is left out, and so is a name more than one
// skill holds, since Codex takes such a $name mention as no skill at all.
// Codex 0.160.0 has no prompts of its own to offer.
func codexSkills(folder string, root harnessmap.Root, sharedSkills string) []command {
	config, err := readCodexConfig(root.Path)
	if err != nil {
		return nil
	}
	visited := map[string]bool{}
	var skills []codexSkill
	read := func(dir string) {
		skills = append(skills, readCodexSkills(dir, "", false, 1, visited)...)
	}
	for _, project := range codexProjectFolders(folder) {
		read(filepath.Join(project, ".codex", "skills"))
		read(filepath.Join(project, ".agents", "skills"))
	}
	read(filepath.Join(root.Path, "skills"))
	read(sharedSkills)
	if config.isBundledEnabled {
		read(filepath.Join(root.Path, "skills", ".system"))
	}
	for id, isEnabled := range config.plugins {
		if isEnabled {
			skills = append(skills, codexPluginSkills(root.Path, id, visited)...)
		}
	}
	holders := map[string]int{}
	var enabled []codexSkill
	for _, skill := range skills {
		if config.isSkillEnabled(skill) {
			enabled = append(enabled, skill)
			holders[skill.name]++
		}
	}
	var commands []command
	for _, skill := range enabled {
		if holders[skill.name] == 1 {
			commands = append(commands, command{Name: skill.name, Description: skill.description, Source: "skill"})
		}
	}
	return commands
}

// codexProjectFolders are the folders from the project's root, the nearest
// folder at or above folder that holds .git, down to folder itself, or folder
// alone outside a project.
func codexProjectFolders(folder string) []string {
	var folders []string
	for current := folder; ; current = filepath.Dir(current) {
		folders = append(folders, current)
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			slices.Reverse(folders)
			return folders
		}
		if filepath.Dir(current) == current {
			return []string{folder}
		}
	}
}

// readCodexSkills reads the skills under dir, whose entries are at depth, as
// Codex walks a skills folder: into every folder that is not hidden, to its
// depth limit, each SKILL.md a skill behind namespace, or, unless a plugin
// that owns the folder fixed it, behind the name of the nearest plugin below
// dir that holds the skill.
func readCodexSkills(dir, namespace string, isNamespaceFixed bool, depth int, visited map[string]bool) []codexSkill {
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil || visited[canonical] {
		return nil
	}
	visited[canonical] = true
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	if depth > 1 && !isNamespaceFixed {
		if name, isPlugin := codexPluginName(dir); isPlugin {
			namespace = name
		}
	}
	var skills []codexSkill
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, err := os.Stat(path)
		switch {
		case err != nil:
		case info.IsDir():
			if !strings.HasPrefix(entry.Name(), ".") && depth < codexMaxSkillDepth {
				skills = append(skills, readCodexSkills(path, namespace, isNamespaceFixed, depth+1, visited)...)
			}
		case entry.Name() == "SKILL.md":
			if skill, ok := readCodexSkill(path, namespace); ok {
				skills = append(skills, skill)
			}
		}
	}
	return skills
}

// readCodexSkill reads one SKILL.md as Codex loads it: frontmatter with a
// description, and a name of its own or its folder's that a $name mention
// can carry.
func readCodexSkill(path, namespace string) (codexSkill, bool) {
	fields, err := commandFrontmatter(path)
	description := strings.Join(strings.Fields(fields.Description), " ")
	name := cmp.Or(strings.Join(strings.Fields(fields.Name), " "), filepath.Base(filepath.Dir(path)))
	if err != nil || description == "" || len([]rune(name)) > codexMaxSkillName {
		return codexSkill{}, false
	}
	if namespace != "" {
		name = namespace + ":" + name
	}
	if !codexMentionName.MatchString(name) {
		return codexSkill{}, false
	}
	return codexSkill{name: name, description: description, path: path}, true
}

// codexPluginName is the name a plugin's manifest under dir gives it, or the
// folder's own name when it gives none, and whether dir holds a plugin.
func codexPluginName(dir string) (string, bool) {
	manifest, ok := readCodexPluginManifest(dir)
	return cmp.Or(strings.TrimSpace(manifest.Name), filepath.Base(dir)), ok
}

type codexPluginManifest struct {
	Name   string          `json:"name"`
	Skills json.RawMessage `json:"skills"`
}

// readCodexPluginManifest reads the first manifest a plugin folder holds.
func readCodexPluginManifest(dir string) (codexPluginManifest, bool) {
	var manifest codexPluginManifest
	for _, metadata := range codexPluginManifests {
		data, err := readConfigFile(filepath.Join(dir, metadata, "plugin.json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		return manifest, err == nil && json.Unmarshal(data, &manifest) == nil
	}
	return manifest, false
}

// codexPluginSkills reads the skills of the plugin id names, name@marketplace,
// from its active version in Codex's plugin cache: the folders its manifest
// names for skills, or its skills folder, and the skills its install made of
// its commands, but for one a skill of its own already holds the name of.
func codexPluginSkills(home, id string, visited map[string]bool) []codexSkill {
	name, marketplace, ok := strings.Cut(id, "@")
	if !ok || name == "" || marketplace == "" || strings.ContainsAny(id, `/\`) || name == ".." || marketplace == ".." {
		return nil
	}
	base := filepath.Join(home, "plugins", "cache", marketplace, name)
	version := codexActivePluginVersion(base)
	if version == "" {
		return nil
	}
	root := filepath.Join(base, version)
	manifest, isPlugin := readCodexPluginManifest(root)
	if !isPlugin {
		return nil
	}
	namespace := cmp.Or(strings.TrimSpace(manifest.Name), version)
	folders := []string{filepath.Join(root, "skills")}
	var paths []string
	var one string
	if json.Unmarshal(manifest.Skills, &one) == nil {
		paths = []string{one}
	} else if json.Unmarshal(manifest.Skills, &paths) != nil {
		paths = nil
	}
	if len(paths) > 0 {
		folders = nil
		for _, path := range paths {
			if strings.HasPrefix(path, "./") && !slices.Contains(strings.Split(filepath.ToSlash(path), "/"), "..") {
				folders = append(folders, filepath.Join(root, filepath.FromSlash(path)))
			}
		}
	}
	var skills []codexSkill
	for _, folder := range folders {
		skills = append(skills, readCodexSkills(folder, namespace, true, 1, visited)...)
	}
	for _, migrated := range readCodexSkills(filepath.Join(root, ".codex-plugin", "migrated-command-skills"), namespace, true, 1, visited) {
		if !slices.ContainsFunc(skills, func(skill codexSkill) bool { return skill.name == migrated.name }) {
			skills = append(skills, migrated)
		}
	}
	return skills
}

// codexActivePluginVersion is the version folder of a cached plugin Codex
// loads: "local" when there is one, or else the latest by semantic version.
func codexActivePluginVersion(base string) string {
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	var versions []string
	for _, entry := range entries {
		if entry.IsDir() && codexPluginVersion.MatchString(entry.Name()) && entry.Name() != "." && entry.Name() != ".." {
			versions = append(versions, entry.Name())
		}
	}
	if len(versions) == 0 {
		return ""
	}
	if slices.Contains(versions, "local") {
		return "local"
	}
	slices.SortFunc(versions, compareCodexPluginVersions)
	return versions[len(versions)-1]
}

// codexPluginVersion is a version folder name Codex accepts.
var codexPluginVersion = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)

// semanticVersion is a version by semver 2.0.0: its numbers, pre-release and
// build.
var semanticVersion = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

// compareCodexPluginVersions orders two version folders as Codex does: by
// semantic version when both are one, and by name otherwise.
func compareCodexPluginVersions(left, right string) int {
	leftParts, rightParts := semanticVersion.FindStringSubmatch(left), semanticVersion.FindStringSubmatch(right)
	if leftParts == nil || rightParts == nil {
		return strings.Compare(left, right)
	}
	for index := 1; index <= 3; index++ {
		leftNumber, _ := strconv.ParseUint(leftParts[index], 10, 64)
		rightNumber, _ := strconv.ParseUint(rightParts[index], 10, 64)
		if leftNumber != rightNumber {
			return cmp.Compare(leftNumber, rightNumber)
		}
	}
	if order := comparePreRelease(leftParts[4], rightParts[4]); order != 0 {
		return order
	}
	return strings.Compare(leftParts[5], rightParts[5])
}

// comparePreRelease orders two pre-releases of one version: none comes after
// any, numeric identifiers by number and before the others, and a shorter
// list of equal identifiers first.
func comparePreRelease(left, right string) int {
	switch {
	case left == right:
		return 0
	case left == "":
		return 1
	case right == "":
		return -1
	}
	leftIDs, rightIDs := strings.Split(left, "."), strings.Split(right, ".")
	for index := range min(len(leftIDs), len(rightIDs)) {
		leftNumber, leftErr := strconv.ParseUint(leftIDs[index], 10, 64)
		rightNumber, rightErr := strconv.ParseUint(rightIDs[index], 10, 64)
		switch {
		case leftErr == nil && rightErr == nil && leftNumber != rightNumber:
			return cmp.Compare(leftNumber, rightNumber)
		case leftErr == nil && rightErr != nil:
			return -1
		case leftErr != nil && rightErr == nil:
			return 1
		case leftErr != nil && rightErr != nil && leftIDs[index] != rightIDs[index]:
			return strings.Compare(leftIDs[index], rightIDs[index])
		}
	}
	return cmp.Compare(len(leftIDs), len(rightIDs))
}
