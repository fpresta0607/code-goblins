package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"gopkg.in/yaml.v3"
)

var ErrBusy = errors.New("pipeline: config-apply requires an idle window with the daemon stopped and no active runs")

// Render owns only the listed paths. Drift contains field names, never values.
func Render(before []byte, p Policy) ([]byte, []string, error) {
	if err := p.Validate(); err != nil {
		return nil, nil, err
	}
	doc, err := parseYAML(before)
	if err != nil {
		return nil, nil, err
	}
	root := doc.Content[0]
	var drift []string
	set := func(parent *yaml.Node, key, label string, value interface{}) error {
		var desired yaml.Node
		if err := desired.Encode(value); err != nil {
			return err
		}
		for i := 0; i < len(parent.Content); i += 2 {
			if parent.Content[i].Value != key {
				continue
			}
			var actual, want interface{}
			if err := parent.Content[i+1].Decode(&actual); err != nil {
				return errors.New("pipeline: invalid owned YAML value")
			}
			if err := desired.Decode(&want); err != nil {
				return err
			}
			if !reflect.DeepEqual(actual, want) {
				desired.HeadComment = parent.Content[i+1].HeadComment
				desired.LineComment = parent.Content[i+1].LineComment
				parent.Content[i+1] = &desired
				drift = append(drift, label)
			}
			return nil
		}
		parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &desired)
		drift = append(drift, label)
		return nil
	}
	if err := set(root, "agent", "agent", []string{p.Reviewer.Harness}); err != nil {
		return nil, nil, err
	}
	auto, err := mapping(root, "auto_fix")
	if err != nil {
		return nil, nil, err
	}
	for _, item := range []struct {
		key   string
		value int
	}{{"review", p.AutoFix.Review}, {"test", p.AutoFix.Test}, {"lint", p.AutoFix.Lint}, {"rebase", p.AutoFix.Rebase}, {"ci", p.AutoFix.CI}} {
		if err := set(auto, item.key, "auto_fix."+item.key, item.value); err != nil {
			return nil, nil, err
		}
	}
	args, err := mapping(root, "agent_args_override")
	if err != nil {
		return nil, nil, err
	}
	if err := set(args, "claude", "agent_args_override.claude", []string{"--model", p.Reviewer.Model, "--effort", p.Reviewer.Effort}); err != nil {
		return nil, nil, err
	}
	if len(drift) == 0 {
		return before, nil, nil
	}
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return nil, nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), drift, nil
}

func parseYAML(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&doc); err != nil {
		return nil, errors.New("pipeline: config must be a YAML mapping")
	}
	if err := decoder.Decode(&yaml.Node{}); err != io.EOF {
		return nil, errors.New("pipeline: multiple YAML documents are refused")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("pipeline: config must be a YAML mapping")
	}
	if err := simpleYAML(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// Aliases/merges and duplicate keys make surgical edits ambiguous, so refuse them.
func simpleYAML(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode || n.Anchor != "" || n.Tag == "!!merge" {
		return errors.New("pipeline: YAML aliases, anchors and merges require manual normalization")
	}
	if n.Kind == yaml.MappingNode {
		keys := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || keys[key.Value] {
				return errors.New("pipeline: duplicate or non-string YAML key")
			}
			keys[key.Value] = true
		}
	}
	for _, child := range n.Content {
		if err := simpleYAML(child); err != nil {
			return err
		}
	}
	return nil
}

func mapping(parent *yaml.Node, key string) (*yaml.Node, error) {
	for i := 0; i < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			if parent.Content[i+1].Kind != yaml.MappingNode {
				return nil, fmt.Errorf("pipeline: %s must be a mapping", key)
			}
			return parent.Content[i+1], nil
		}
	}
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, node)
	return node, nil
}

type Config struct {
	Path   string
	Policy Policy
	// Idle holds the native daemon singleton lock until the returned release runs.
	Idle func(context.Context) (func() error, error)
}
type ApplyResult struct {
	Drift  []string
	Backup string
}

func (c Config) Drift() ([]string, error) {
	before, err := os.ReadFile(c.Path)
	if err != nil {
		return nil, err
	}
	_, drift, err := Render(before, c.Policy)
	return drift, err
}

func (c Config) Apply(ctx context.Context) (result ApplyResult, err error) {
	if c.Idle == nil {
		return result, ErrBusy
	}
	release, err := c.Idle(ctx)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, release()) }()
	before, err := os.ReadFile(c.Path)
	if err != nil {
		return result, err
	}
	after, drift, err := Render(before, c.Policy)
	if err != nil {
		return result, err
	}
	result.Drift = drift
	if len(drift) == 0 {
		return result, nil
	}
	// The backup may contain unrelated credentials, so restrict it before it
	// holds any. The live file is rewritten in place instead of renamed over,
	// so a shared config keeps its own inherited permissions and the principals
	// running other pipelines do not lose access to it.
	backup, err := os.CreateTemp(filepath.Dir(c.Path), "config-backup-"+time.Now().UTC().Format("20060102T150405Z")+"-*.yaml")
	if err != nil {
		return result, err
	}
	backupPath := backup.Name()
	if err := backup.Close(); err != nil {
		return result, err
	}
	if err := auth.WriteSecretFile(backupPath, ""); err != nil {
		return result, err
	}
	if err := os.WriteFile(backupPath, before, 0600); err != nil {
		return result, err
	}
	result.Backup = backupPath
	// Detect an operator edit that raced the backup; never overwrite that edit.
	latest, err := os.ReadFile(c.Path)
	if err != nil {
		return result, err
	}
	if !bytes.Equal(latest, before) {
		return result, errors.New("pipeline: config changed during apply; original backed up, current file preserved")
	}
	if err := os.WriteFile(c.Path, after, 0600); err != nil {
		return result, err
	}
	return result, nil
}
