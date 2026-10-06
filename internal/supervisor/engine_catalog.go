package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/routing"
)

type EngineCatalog struct {
	Harnesses []EngineHarness `json:"harnesses"`
}

type EngineHarness struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Reason string        `json:"reason,omitempty"`
	Models []EngineModel `json:"models"`
}

type EngineModel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Efforts       []string `json:"efforts"`
	DefaultEffort string   `json:"default_effort"`
}

func (s *Service) engineCatalog(ctx context.Context, runner execx.Runner) (EngineCatalog, error) {
	root, err := os.UserHomeDir()
	if err != nil {
		return EngineCatalog{}, err
	}
	lookPath := exec.LookPath
	if setup := s.Options.FirstRun; setup != nil {
		root, lookPath = setup.Home, setup.LookPath
	}
	policy, err := routing.Load(s.Store.Home.Data)
	if err != nil {
		return EngineCatalog{}, err
	}
	catalog := EngineCatalog{Harnesses: []EngineHarness{}}
	for _, capability := range CFOCapabilities() {
		kind := harness.Kind(capability.Agent)
		item := EngineHarness{ID: capability.Agent, Name: capability.Name, Models: []EngineModel{}}
		path, pathErr := lookPath(item.ID)
		signIn, signInErr := os.Stat(filepath.Join(root, filepath.FromSlash(firstRunSignIn[item.ID])))
		switch {
		case pathErr != nil:
			item.Reason = "Install " + item.Name
		case kind == harness.Claude && !strings.EqualFold(filepath.Ext(path), ".exe"):
			item.Reason = "Install the native build of Claude Code"
		case signInErr != nil || !signIn.Mode().IsRegular() || signIn.Size() == 0:
			item.Reason = "Sign in to " + item.Name
		}
		if item.Reason == "" {
			item.Models, err = readEngineModels(root, kind)
			if err != nil {
				item.Reason = "Model catalog could not be read: " + err.Error()
			}
		}
		if item.Reason == "" {
			add := func(id, effort string) {
				if id == "" || !spawnValue.MatchString(id) {
					return
				}
				if !slices.ContainsFunc(item.Models, func(model EngineModel) bool { return model.ID == id }) {
					name := id
					if id == "default" {
						name = "Default"
					}
					item.Models = append(item.Models, EngineModel{ID: id, Name: name, Efforts: harness.Efforts(kind), DefaultEffort: effort})
				}
			}
			add(harness.DefaultModel(kind), defaultEffort)
			if kind != harness.Claude {
				add("default", "")
			}
			for _, lane := range policy.Lanes {
				if lane.Harness == item.ID {
					add(lane.Model, lane.Effort)
				}
			}
			for _, rule := range policy.Rules {
				if rule.Switch.Harness == item.ID {
					add(rule.Switch.Model, rule.Switch.Effort)
				}
			}
			if kind == harness.Pi {
				adapter, adapterErr := harness.DefaultRegistry().Get(kind)
				if adapterErr == nil {
					adapterErr = adapter.Validate(ctx, runner)
				}
				if adapterErr != nil {
					item.Reason = "Installed pi capabilities could not be read: " + adapterErr.Error()
				} else {
					for index := range item.Models {
						item.Models[index].Efforts = slices.DeleteFunc(item.Models[index].Efforts, func(effort string) bool {
							_, err := adapter.Build(harness.LaunchSpec{BriefPath: filepath.Join(root, "brief.md"), TaskTmp: root, Scratch: root, Model: item.Models[index].ID, Effort: effort})
							return err != nil
						})
					}
				}
			}
			sort.Slice(item.Models, func(first, second int) bool { return item.Models[first].Name < item.Models[second].Name })
		}
		catalog.Harnesses = append(catalog.Harnesses, item)
	}
	return catalog, nil
}

func readEngineModels(root string, kind harness.Kind) ([]EngineModel, error) {
	models := []EngineModel{}
	path := map[harness.Kind]string{harness.Claude: ".claude.json", harness.Codex: ".codex/models_cache.json", harness.Pi: ".pi/agent/models-store.json"}[kind]
	data, err := fsx.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if errors.Is(err, os.ErrNotExist) {
		if kind != harness.Pi {
			return models, nil
		}
		data, err = []byte("{}"), nil
	}
	if err != nil {
		return nil, err
	}
	switch kind {
	case harness.Claude:
		var cache struct {
			Models []struct{ Value, Label string } `json:"additionalModelOptionsCache"`
		}
		if err := json.Unmarshal(data, &cache); err != nil {
			return nil, err
		}
		for _, model := range cache.Models {
			if spawnValue.MatchString(model.Value) {
				name := model.Label
				if name == "" {
					name = model.Value
				}
				models = append(models, EngineModel{ID: model.Value, Name: name, Efforts: harness.Efforts(kind)})
			}
		}
	case harness.Codex:
		var cache struct {
			Models []struct {
				Slug             string
				DisplayName      string `json:"display_name"`
				Visibility       string
				DefaultReasoning string                    `json:"default_reasoning_level"`
				Reasoning        []struct{ Effort string } `json:"supported_reasoning_levels"`
			}
		}
		if err := json.Unmarshal(data, &cache); err != nil {
			return nil, err
		}
		for _, model := range cache.Models {
			if model.Visibility == "hide" || !spawnValue.MatchString(model.Slug) {
				continue
			}
			item := EngineModel{ID: model.Slug, Name: model.DisplayName, Efforts: []string{}, DefaultEffort: model.DefaultReasoning}
			if item.Name == "" {
				item.Name = item.ID
			}
			for _, level := range model.Reasoning {
				if slices.Contains(harness.Efforts(kind), level.Effort) {
					item.Efforts = append(item.Efforts, level.Effort)
				}
			}
			models = append(models, item)
		}
	case harness.Pi:
		var cache map[string]struct {
			Models []struct {
				ID, Name  string
				Reasoning bool
			}
		}
		if err := json.Unmarshal(data, &cache); err != nil {
			return nil, err
		}
		for provider, source := range cache {
			for _, model := range source.Models {
				id := provider + "/" + model.ID
				if !spawnValue.MatchString(id) {
					continue
				}
				item := EngineModel{ID: id, Name: model.Name, Efforts: []string{}}
				if item.Name == "" {
					item.Name = id
				}
				if model.Reasoning {
					item.Efforts = harness.Efforts(kind)
				}
				models = append(models, item)
			}
		}
		settings, err := fsx.ReadFile(filepath.Join(root, ".pi", "agent", "settings.json"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err == nil {
			var configured struct {
				Provider string `json:"defaultProvider"`
				Model    string `json:"defaultModel"`
				Effort   string `json:"defaultThinkingLevel"`
			}
			if err := json.Unmarshal(settings, &configured); err != nil {
				return nil, err
			}
			id := configured.Provider + "/" + configured.Model
			if configured.Provider != "" && configured.Model != "" && spawnValue.MatchString(id) {
				at := slices.IndexFunc(models, func(model EngineModel) bool { return model.ID == id })
				if at < 0 {
					models = append(models, EngineModel{ID: id, Name: id, Efforts: []string{}})
					at = len(models) - 1
					if slices.Contains(harness.Efforts(kind), configured.Effort) {
						models[at].Efforts = harness.Efforts(kind)
					}
				}
				if slices.Contains(models[at].Efforts, configured.Effort) {
					models[at].DefaultEffort = configured.Effort
				}
			}
		}
	default:
		return nil, fmt.Errorf("no model catalog for %s", kind)
	}
	return models, nil
}
