import test from "node:test";
import assert from "node:assert/strict";
import { connectorMark, harnessMark, modelMark, runLabel, shellLabel, shellMark } from "./connectors.ts";
import { harnessTip } from "./workflow.ts";

test("every connector name the fleet declares resolves to its service mark", () => {
  const cases: [string, "mcp" | "credential", object][] = [
    ["github", "mcp", { brand: "github" }], ["GITHUB_TOKEN", "credential", { brand: "github" }], ["GH_TOKEN", "credential", { brand: "github" }],
    ["chrome-devtools", "mcp", { brand: "chrome" }], ["playwright", "mcp", { glyph: "browser-check" }], ["context7", "mcp", { glyph: "book" }],
    ["supabase", "mcp", { brand: "supabase" }], ["SUPABASE_SERVICE_ROLE_KEY", "credential", { brand: "supabase" }], ["sentry", "mcp", { brand: "sentry" }],
    ["STRIPE_SECRET_KEY", "credential", { brand: "stripe" }], ["vercel", "mcp", { brand: "vercel" }], ["FLY_API_TOKEN", "credential", { brand: "fly" }],
    ["neon", "mcp", { brand: "neon" }], ["neon-auth", "mcp", { brand: "neon" }], ["notion", "mcp", { brand: "notion" }],
    ["OPENROUTER_API_KEY", "credential", { brand: "openrouter" }], ["OPENAI_API_KEY", "credential", { brand: "openai" }], ["ANTHROPIC_API_KEY", "credential", { brand: "anthropic" }],
    ["RESEND_API_KEY", "credential", { brand: "resend" }], ["REDIS_URL", "credential", { brand: "redis" }], ["UPSTASH_REDIS_REST_TOKEN", "credential", { brand: "upstash" }],
    ["QDRANT_API_KEY", "credential", { brand: "qdrant" }], ["railway", "mcp", { brand: "railway" }], ["ionos-vps", "mcp", { brand: "ionos" }],
    ["POSTGRES_PASSWORD", "credential", { brand: "postgres" }], ["DATABASE_URL", "credential", { glyph: "database" }],
  ];
  for (const [name, kind, mark] of cases) assert.deepEqual(connectorMark(name, kind), mark, name);
});

test("an unknown name gets a neutral mark, never bare text or a lookalike service", () => {
  assert.deepEqual(connectorMark("acme-internal", "mcp"), { brand: "mcp" });
  assert.deepEqual(connectorMark("ACME_SIGNING_KEY", "credential"), { glyph: "key" });
  assert.deepEqual(connectorMark("butterfly", "mcp"), { brand: "mcp" }, "fly matches only as a prefix");
  assert.deepEqual(connectorMark("ghost-writer", "mcp"), { brand: "mcp" }, "gh matches only the GitHub token names");
  assert.deepEqual(connectorMark("", "credential"), { glyph: "key" });
});

test("harnesses and model providers each carry their own mark", () => {
  assert.deepEqual(harnessMark("claude"), { brand: "claude" });
  assert.deepEqual(harnessMark("codex"), { brand: "openai" });
  assert.deepEqual(harnessMark("pi"), { glyph: "pi" });
  assert.deepEqual(harnessMark("kimi"), { brand: "kimi" });
  assert.deepEqual(harnessMark("something-new"), { glyph: "terminal" });
  const models: [string, object, string][] = [
    ["claude-opus-5-5", { brand: "anthropic" }, "Anthropic"], ["opus", { brand: "anthropic" }, "Anthropic"], ["claude-fable-5-1", { brand: "anthropic" }, "Anthropic"],
    ["gpt-5.6-sol", { brand: "openai" }, "OpenAI"], ["gpt-6-astra", { brand: "openai" }, "OpenAI"], ["o4-mini", { brand: "openai" }, "OpenAI"],
    ["kimi-k2", { brand: "moonshot" }, "Moonshot AI"], ["gemini-3-pro", { brand: "gemini" }, "Google"], ["example (no model calls)", { glyph: "sparkle" }, "Model"],
  ];
  for (const [model, mark, provider] of models) assert.deepEqual(modelMark(model), { mark, provider }, model);
});

test("a harness mark's tip names the harness, then the model and effort it runs when known", () => {
  const cases: [string, string, string, string][] = [
    ["codex", "gpt-6-astra", "xhigh", "Codex · gpt-6-astra · xhigh"],
    ["claude", "claude-opus-5-5", "", "Claude Code · claude-opus-5-5"],
    ["pi", "", "", "Pi"],
    ["opencode", "", "high", "opencode · high"],
  ];
  for (const [harness, model, effort, tip] of cases) assert.equal(harnessTip(harness, model, effort), tip, harness);
});

test("a run's shell shows its mark and full name", () => {
  const cases: [string, ReturnType<typeof shellMark>, string][] = [
    ["bash", { brand: "bash" }, "Git Bash"],
    ["powershell", { glyph: "terminal" }, "Windows PowerShell"],
    ["pwsh", { glyph: "terminal" }, "PowerShell 7"],
  ];
  for (const [shell, mark, label] of cases) {
    assert.deepEqual(shellMark(shell), mark, shell);
    assert.equal(shellLabel(shell), label, shell);
  }
});

test("the one button on a run card says where the command runs", () => {
  // Arrange
  const cases: [string, boolean, string][] = [
    ["powershell", false, "Run in PowerShell"],
    ["pwsh", false, "Run in PowerShell 7"],
    ["bash", false, "Run in Git Bash"],
    ["powershell", true, "Run as administrator"],
  ];

  for (const [shell, admin, want] of cases) {
    // Act
    const label = runLabel(shell, admin);

    // Assert
    assert.equal(label, want, shell);
  }
});
