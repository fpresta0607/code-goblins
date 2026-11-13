import test from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { connectorAbout, connectorLogo, harnessMark, hasHarnessMark, modelMark, runLabel, shellLabel, shellMark } from "./connectors.ts";
import { harnessTip } from "./workflow.ts";

test("harnesses and model providers each carry their own mark", () => {
  assert.deepEqual(harnessMark("claude"), { brand: "claude" });
  assert.deepEqual(harnessMark("codex"), { glyph: "codex" });
  assert.deepEqual(harnessMark("pi"), { glyph: "pi" });
  assert.deepEqual(harnessMark("kimi"), { brand: "kimi" });
  assert.deepEqual(harnessMark("something-new"), { glyph: "terminal" });
  assert.deepEqual(["claude", "codex", "pi", "kimi", "something-new", "toString", ""].map(hasHarnessMark), [true, true, true, true, false, false, false]);
  const models: [string, object, string][] = [
    ["claude-opus-5-5", { brand: "anthropic" }, "Anthropic"], ["opus", { brand: "anthropic" }, "Anthropic"], ["claude-fable-5-1", { brand: "anthropic" }, "Anthropic"],
    ["gpt-5.6-sol", { brand: "openai" }, "OpenAI"], ["gpt-6-astra", { brand: "openai" }, "OpenAI"], ["o4-mini", { brand: "openai" }, "OpenAI"],
    ["kimi-k2", { brand: "moonshot" }, "Moonshot AI"], ["gemini-3-pro", { brand: "gemini" }, "Google"], ["example (no model calls)", { glyph: "sparkle" }, "Model"],
  ];
  for (const [model, mark, provider] of models) assert.deepEqual(modelMark(model), { mark, provider }, model);
});

test("a harness mark's tip names the harness, then the model by its short name and the effort it runs when known", () => {
  const cases: [string, string, string, string][] = [
    ["codex", "gpt-6-astra", "xhigh", "Codex · GPT-6 Astra · xhigh"],
    ["claude", "claude-opus-5-5", "", "Claude Code · Opus 5.5"],
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

// The Overlord, 2026-10-13: connector cards "like caldu code conenctors...
// pelase use the actual logo", each with one line on what it does; and on
// 2026-11-10, "the UI for connector should actually use the logo", when a
// name with its harness's prefix on it found the harness's logo, not its own.
test("a connector shows its service's own bundled logo, or its initial on a plain tile", () => {
  const cases: [string, ReturnType<typeof connectorLogo>][] = [
    ["github", { file: "github" }], ["GITHUB_TOKEN", { file: "github" }], ["GH_TOKEN", { file: "github" }], ["chrome-devtools", { file: "chrome" }],
    ["context7", { file: "context7" }], ["SUPABASE_SERVICE_ROLE_KEY", { file: "supabase" }], ["sentry", { file: "sentry" }], ["STRIPE_SECRET_KEY", { file: "stripe" }],
    ["vercel", { file: "vercel" }], ["FLY_API_TOKEN", { file: "fly" }], ["neon-auth", { file: "neon" }], ["notion", { file: "notion" }],
    ["openrouter", { file: "openrouter" }], ["OPENAI_API_KEY", { file: "openai" }], ["ANTHROPIC_API_KEY", { file: "anthropic" }], ["google-report", { file: "google" }],
    ["RESEND_API_KEY", { file: "resend" }], ["REDIS_URL", { file: "redis" }], ["UPSTASH_REDIS_REST_TOKEN", { file: "upstash" }], ["QDRANT_API_KEY", { file: "qdrant" }],
    ["railway", { file: "railway" }], ["ionos-vps", { file: "ionos" }], ["POSTGRES_PASSWORD", { file: "postgres" }],
    ["claude.ai Supabase", { file: "supabase" }], ["claude_ai_Supabase", { file: "supabase" }], ["plugin_supabase_supabase", { file: "supabase" }],
    ["claude.ai Gmail", { file: "gmail" }], ["claude.ai Google Drive", { file: "google-drive" }], ["claude.ai Google Calendar", { file: "google-calendar" }],
    ["claude.ai Vercel", { file: "vercel" }], ["claude.ai Claude Docs", { file: "anthropic" }],
    ["claude.ai PrecisionDocs", { initial: "P" }], ["claude.ai Microsoft 365", { initial: "M" }], ["playwright", { initial: "P" }], ["DATABASE_URL", { initial: "D" }],
    ["acme-internal", { initial: "A" }], ["butterfly", { initial: "B" }], ["ghost-writer", { initial: "G" }], ["", { initial: "?" }],
  ];
  for (const [name, logo] of cases) assert.deepEqual(connectorLogo(name), logo, name);
});

test("every logo a connector can name is a file bundled with the board, and no file is left without one", () => {
  // Arrange
  const names = ["github", "chrome-devtools", "context7", "supabase", "sentry", "stripe", "vercel", "fly", "neon", "notion", "openrouter", "openai", "anthropic", "gemini", "gmail",
    "google-drive", "google-calendar", "google", "kimi", "resend", "upstash", "redis", "qdrant", "railway", "ionos", "postgres"];
  const folder = new URL("./connector-logos/", import.meta.url);

  for (const name of names) {
    // Act
    const logo = connectorLogo(name);

    // Assert
    assert.ok("file" in logo, name);
    assert.match(readFileSync(new URL(logo.file + ".svg", folder), "utf8"), /^<svg xmlns="http:\/\/www\.w3\.org\/2000\/svg" viewBox="[\d ]+" fill="#[0-9A-Fa-f]{6}"><path d="[^"]+"\/><\/svg>\r?\n$/, name);
  }
  assert.equal(readdirSync(folder).length, names.length);
});

test("a connector says in one line what a known service does, and nothing for an unknown one", () => {
  for (const [name, about] of [["github", "Repositories, pull requests and Actions"], ["context7", "Current library documentation"], ["openrouter", "Models from many providers"], ["google-report", "Google APIs"], ["crawler", "Finds and reads every page and file of a site"], ["claude.ai Gmail", "Gmail messages and drafts"], ["claude.ai Supabase", "Supabase database, auth and storage"], ["webcrawler-mcp", ""], ["acme-internal", ""]]) {
    assert.equal(connectorAbout(name), about, name);
  }
});
