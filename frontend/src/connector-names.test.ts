import { test } from "node:test";
import assert from "node:assert/strict";
import { connectorName, toolWords, withoutPrefix } from "./connector-names.ts";

// The Overlord, 2026-11-10: "Strip prefixes like claude_ai_, mcp__ and
// plugin_ from every connector name", and the name reads in proper case.
test("a connector reads as its own name in proper case, without what its harness puts before it", () => {
  const cases: [string, string][] = [
    ["claude_ai_Supabase", "Supabase"], ["claude.ai Supabase", "Supabase"], ["mcp__claude_ai_Supabase", "Supabase"],
    ["claude.ai Google Drive", "Google Drive"], ["claude_ai_GitHub_Integration", "GitHub Integration"], ["claude.ai Microsoft 365", "Microsoft 365"],
    ["plugin_supabase_supabase", "Supabase"], ["plugin:data:amplitude-eu", "Data Amplitude EU"], ["mcp__plugin_context7_context7", "Context7"],
    ["chrome-devtools", "Chrome DevTools"], ["github", "GitHub"], ["google-report", "Google Report"], ["precisiondocs", "PrecisionDocs"],
    ["openaiDeveloperDocs", "OpenAI Developer Docs"], ["docs", "Docs"], ["acme-internal", "Acme Internal"], ["21st", "21st"], ["fly.io", "Fly.io"],
    ["AWS", "AWS"], ["", ""],
  ];
  for (const [name, want] of cases) assert.equal(connectorName(name), want, name);
});

test("a name that is only a prefix is kept, never emptied", () => {
  for (const [name, want] of [["plugin_", "plugin_"], ["mcp__", "mcp__"], ["  claude.ai Neon  ", "Neon"]]) assert.equal(withoutPrefix(name), want, name);
});

test("a tool reads in words, its acronyms in capitals and its connector's own name left out", () => {
  const cases: [string, string, string][] = [
    ["execute_sql", "Supabase", "Execute SQL"], ["list_projects", "Supabase", "List projects"], ["generate_typescript_types", "Supabase", "Generate TypeScript types"],
    ["get_project_url", "Supabase", "Get project URL"], ["resolve-library-id", "Context7", "Resolve library ID"], ["getIssueComments", "GitHub", "Get issue comments"],
    ["precisiondocs_add_parcels", "PrecisionDocs", "Add parcels"], ["precisiondocs", "PrecisionDocs", "PrecisionDocs"], ["search_stripe_documentation", "Stripe", "Search stripe documentation"],
    ["list_pr_ids", "GitHub", "List PR IDs"], ["microsoft_outlook_calendar.get_schedule", "Codex Apps", "Microsoft outlook calendar get schedule"], ["fetch", "", "Fetch"],
  ];
  for (const [tool, connector, want] of cases) assert.equal(toolWords(tool, connector), want, tool);
});
