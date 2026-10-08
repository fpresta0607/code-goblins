package supervisor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/harness"
)

func TestEngineCatalogReadsInstalledHarnessModelsAndTheirEfforts(t *testing.T) {
	store, h := testStore(t)
	machine := newFirstRunMachine(t)
	writeCatalog := func(relative, text string) {
		t.Helper()
		path := filepath.Join(machine.run.Home, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeCatalog(".codex/models_cache.json", `{"models":[{"slug":"catalog-model","display_name":"Catalog model","visibility":"list","default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"}]},{"slug":"hidden-model","visibility":"hide","supported_reasoning_levels":[{"effort":"high"}]}]}`)
	writeCatalog(".claude.json", `{"additionalModelOptionsCache":[{"value":"custom-model[1m]","label":"Custom model"}]}`)
	if err := os.MkdirAll(h.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "routing.json"), []byte(`{"lanes":{"build":{"harness":"codex","model":"routed-model","effort":"high"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTP(&Service{Store: store, Options: Options{FirstRun: machine.run}}, "board.local", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://board.local/api/engines", nil))

	if response.Code != 200 {
		t.Fatalf("catalog status = %d: %s", response.Code, response.Body.String())
	}
	var catalog EngineCatalog
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	find := func(id string) EngineHarness {
		t.Helper()
		for _, item := range catalog.Harnesses {
			if item.ID == id {
				return item
			}
		}
		t.Fatalf("missing harness %s: %+v", id, catalog)
		return EngineHarness{}
	}
	codex := find("codex")
	if !slices.ContainsFunc(codex.Models, func(model EngineModel) bool { return model.ID == "default" }) {
		t.Fatal("the harness's configured default disappeared when its catalog had models")
	}
	at := slices.IndexFunc(codex.Models, func(model EngineModel) bool { return model.ID == "catalog-model" })
	if codex.Reason != "" || at < 0 || codex.Models[at].Name != "Catalog model" || strings.Join(codex.Models[at].Efforts, ",") != "low,medium" || codex.Models[at].DefaultEffort != "medium" || slices.ContainsFunc(codex.Models, func(model EngineModel) bool { return model.ID == "hidden-model" }) || !slices.ContainsFunc(codex.Models, func(model EngineModel) bool { return model.ID == "routed-model" }) {
		t.Fatalf("codex catalog = %+v", codex)
	}
	if !slices.ContainsFunc(find("claude").Models, func(model EngineModel) bool { return model.ID == "custom-model[1m]" }) {
		t.Fatalf("claude catalog = %+v", find("claude"))
	}
	if find("pi").Reason == "" {
		t.Fatal("uninstalled pi was offered without its reason")
	}
}

// The fixture is Claude Code 2.1.293's catalog of 2026-10-07 for the
// organization it was signed in to, trimmed to three of its models.
func TestEngineCatalogOffersClaudeModelsFromClaudeCodesOwnCatalog(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	machine := newFirstRunMachine(t)
	recorded, err := os.ReadFile(filepath.Join("testdata", "claude-model-catalog-cc.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalogs := filepath.Join(machine.run.Home, ".claude", "cache", "model-catalog")
	writeFile(t, filepath.Join(machine.run.Home, ".claude.json"), `{"oauthAccount":{"organizationUuid":"signed-in-org"},"additionalModelOptionsCache":[{"value":"claude-fable-5-1[1m]","label":"Fable"}]}`)
	writeFile(t, filepath.Join(catalogs, "signed-in-org-3ed20817107d-cc.json"), string(recorded))
	other := func(fetchedAt, model string) string {
		return `{"version":2,"fetchedAt":` + fetchedAt + `,"catalog":{"surface":"cc","config":{"id":"cc","models":[{"id":"` + model + `","name":"` + model + `"}]}}}`
	}
	writeFile(t, filepath.Join(catalogs, "signed-in-org-0697c9c0325f-cc.json"), other("1", "claude-older-catalog-only"))
	writeFile(t, filepath.Join(catalogs, "another-org-e146c75e2fa2-cc.json"), other("9791415559001", "claude-another-org-only"))
	writeFile(t, filepath.Join(catalogs, "tok-1bb99ad6f7a4-ccd.json"), other("9791415559001", "claude-desktop-only"))
	handler := NewHTTP(&Service{Store: store, Options: Options{FirstRun: machine.run}}, "board.local", nil)
	response := httptest.NewRecorder()

	// Act
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://board.local/api/engines", nil))

	// Assert
	var catalog EngineCatalog
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); response.Code != 200 || err != nil {
		t.Fatalf("catalog status = %d, %v: %s", response.Code, err, response.Body.String())
	}
	at := slices.IndexFunc(catalog.Harnesses, func(item EngineHarness) bool { return item.ID == "claude" })
	if at < 0 || catalog.Harnesses[at].Reason != "" {
		t.Fatalf("claude = %+v", catalog.Harnesses)
	}
	models := map[string]EngineModel{}
	for _, model := range catalog.Harnesses[at].Models {
		if _, isTwice := models[model.ID]; isTwice {
			t.Fatalf("%s offered twice: %+v", model.ID, catalog.Harnesses[at].Models)
		}
		models[model.ID] = model
	}
	if sonnet := models["claude-sonnet-5-5"]; sonnet.Name != "Sonnet 5.5" || strings.Join(sonnet.Efforts, ",") != "low,medium,high,xhigh,max" {
		t.Fatalf("sonnet = %+v, want Claude Code's name and efforts", sonnet)
	}
	if opus := models["claude-opus-5-5"]; opus.Name != "Opus 5.5" || opus.DefaultEffort != defaultEffort {
		t.Fatalf("opus = %+v, want Claude Code's name and the fleet's %s default", opus, defaultEffort)
	}
	if haiku, ok := models["claude-haiku-4-5-20251001"]; !ok || len(haiku.Efforts) != 0 {
		t.Fatalf("a model without effort options = %+v, %v", haiku, ok)
	}
	if _, ok := models["claude-fable-5-1[1m]"]; !ok {
		t.Fatal("the additional model option disappeared")
	}
	for _, absent := range []string{"claude-older-catalog-only", "claude-another-org-only", "claude-desktop-only"} {
		if _, ok := models[absent]; ok {
			t.Fatalf("%s came from a catalog Claude Code does not use now", absent)
		}
	}
}

func TestEngineCatalogReportsUnreadableCatalogAndMissingSignIn(t *testing.T) {
	store, _ := testStore(t)
	machine := newFirstRunMachine(t)
	if err := os.WriteFile(filepath.Join(machine.run.Home, ".codex", "models_cache.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(machine.run.Home, ".claude", ".credentials.json")); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTP(&Service{Store: store, Options: Options{FirstRun: machine.run}}, "board.local", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://board.local/api/engines", nil))

	if response.Code != 200 {
		t.Fatalf("catalog status = %d: %s", response.Code, response.Body.String())
	}
	var catalog EngineCatalog
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	for _, item := range catalog.Harnesses {
		if item.ID == "codex" && !strings.Contains(item.Reason, "catalog") || item.ID == "claude" && !strings.Contains(item.Reason, "Sign in") {
			t.Fatalf("unavailable harness = %+v", item)
		}
	}
}

func TestPiEngineCatalogReadsConfiguredModelAndModelSpecificReasoning(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".pi", "agent", "models-store.json"), `{"provider":{"models":[{"id":"reasoning","name":"Reasoning model","reasoning":true},{"id":"plain","name":"Plain model","reasoning":false}]}}`)
	writeFile(t, filepath.Join(root, ".pi", "agent", "settings.json"), `{"defaultProvider":"provider","defaultModel":"configured","defaultThinkingLevel":"high"}`)

	models, err := readEngineModels(root, harness.Pi)

	if err != nil || len(models) != 3 {
		t.Fatalf("pi catalog = %+v, %v", models, err)
	}
	for _, model := range models {
		if model.ID == "provider/plain" && len(model.Efforts) != 0 || model.ID == "provider/reasoning" && len(model.Efforts) == 0 || model.ID == "provider/configured" && model.DefaultEffort != "high" {
			t.Fatalf("pi model = %+v", model)
		}
	}
}
