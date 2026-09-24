import test from "node:test";
import assert from "node:assert/strict";
import { connectorMark, harnessMark, modelMark } from "./connectors.ts";

test("every connector name the fleet declares resolves to its service mark", () => {
  const cases: [string, "mcp" | "credential", object][] = [
    ["github", "mcp", { brand: "github" }], ["GITHUB_TOKEN", "credential", { brand: "github" }], ["GH_TOKEN", "credential", { brand: "github" }],
    ["chrome-devtools", "mcp", { brand: "chrome" }], ["playwright", "mcp", { glyph: "browser-check" }], ["context7", "mcp", { glyph: "book" }],
    ["supabase", "mcp", { brand: "supabase" }], ["SUPABASE_SERVICE_ROLE_KEY", "credential", { brand: "supabase" }], ["sentry", "mcp", { brand: "sentry" }],
    ["STRIPE_SECRET_KEY", "credential", { brand: "stripe" }], ["vercel", "mcp", { brand: "vercel" }], ["FLY_API_TOKEN", "credential", { brand: "fly" }],
    ["neon", "mcp", { brand: "neon" }], ["neon-auth", "mcp", { brand: "neon" }], ["notion", "mcp", { brand: "notion" }],
    ["OPENROUTER_API_KEY", "credential", { brand: "openrouter" }], ["OPENAI_API_KEY", "credential", { glyph: "openai" }], ["ANTHROPIC_API_KEY", "credential", { brand: "anthropic" }],
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
  assert.deepEqual(harnessMark("codex"), { glyph: "openai" });
  assert.deepEqual(harnessMark("pi"), { glyph: "pi" });
  assert.deepEqual(harnessMark("kimi"), { brand: "kimi" });
  assert.deepEqual(harnessMark("something-new"), { glyph: "sparkle" });
  const models: [string, object, string][] = [
    ["claude-opus-5-5", { brand: "anthropic" }, "Anthropic"], ["opus", { brand: "anthropic" }, "Anthropic"], ["claude-fable-5-1", { brand: "anthropic" }, "Anthropic"],
    ["gpt-5.6-sol", { glyph: "openai" }, "OpenAI"], ["gpt-6-astra", { glyph: "openai" }, "OpenAI"], ["o4-mini", { glyph: "openai" }, "OpenAI"],
    ["kimi-k2", { brand: "moonshot" }, "Moonshot AI"], ["gemini-3-pro", { brand: "gemini" }, "Google"], ["example (no model calls)", { glyph: "sparkle" }, "Model"],
  ];
  for (const [model, mark, provider] of models) assert.deepEqual(modelMark(model), { mark, provider }, model);
});
