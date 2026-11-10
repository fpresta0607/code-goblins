import { CFO_DOOR, expect, test, type Locator } from "./site";
import { at, open, snapshot } from "./session-board";

// The Overlord, 2026-11-10, of rows that read "Called claude_ai_Supabase
// list_projects" beside a plug and "ToolSearch select:mcp__..." beside a
// sparkle: "for tool search the icon should be the wrench tool. the UI for
// connector should actually use the logo", and a row such as
// claude_ai_Supabase execute_sql reads as Supabase, with its logo, and
// Execute SQL (#2302). The supervisor is played here: the conversation is
// made up, and its calls are named as Claude Code names them.
const SELECT = "select:mcp__claude_ai_Supabase__list_projects,mcp__claude_ai_Supabase__list_branches,mcp__claude_ai_Acme_Billing__create_invoice,mcp__chrome-devtools__take_snapshot";
const call = (id: string, tool: string, subject = "", state = "done") => ({ kind: "call", call_id: id, tool, subject, state, result: state === "running" ? "" : "ok" });
const text = (words: string) => ({ kind: "text", text: words });
const CONVERSATION = { conversation: "fixture-conversation", more: false, messages: [
  { id: "prompt-0", seq: 0, at, kind: "prompt", source: "overlord", text: "Which projects does Supabase hold?" },
  { id: "reply-1", seq: 1, at, kind: "reply", blocks: [
    text("Loading the tools first."), call("search-0", "ToolSearch", SELECT),
    text("Listing the projects."), call("known-0", "mcp__claude_ai_Supabase__list_projects"),
    text("Billing next."), call("unknown-0", "mcp__claude_ai_Acme_Billing__create_invoice"),
    text("A look at the page."), call("server-0", "mcp__chrome-devtools__take_snapshot"),
    text("Counting the rows."), call("sql-0", "mcp__claude_ai_Supabase__execute_sql", "select count(*) from projects", "running"),
  ] },
  { id: "reply-2", seq: 2, at, kind: "reply", blocks: [
    text("Three more reads."),
    call("fold-0", "mcp__claude_ai_Supabase__list_projects"), call("fold-1", "mcp__claude_ai_Supabase__list_branches"), call("fold-2", "mcp__claude_ai_Supabase__get_project_url"),
    text("Done."),
  ] },
] };

// The logo a row draws: a bundled file that loaded, never a broken picture.
async function expectLogo(row: Locator, file: string) {
  const logo = row.locator("summary .call-icon img");
  await expect(logo).toHaveAttribute("src", "/assets/connectors/" + file + ".svg");
  await expect(logo).toHaveAttribute("loading", "lazy");
  await expect.poll(() => logo.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth > 0)).toBe(true);
  await expect(row.locator("summary .call-icon .icon")).toHaveCount(0);
}

for (const width of [390, 1440]) {
  test(`a connector's call wears its logo and reads as its name and its tool in words, and a tool search wears a wrench, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height: 1100 });
    await open(page, snapshot(), undefined, CONVERSATION);

    // Act
    await page.getByRole("button", { name: CFO_DOOR }).first().click();
    const messages = page.getByRole("log", { name: "Messages of CEO" });
    await expect(messages.getByText("Done.")).toBeVisible();
    const row = (label: string) => messages.locator(".message-steps > details.message-call:not(.message-call-group)").filter({ hasText: label });

    // Assert: a known connector, by its name and logo.
    const known = row("Supabase · List projects");
    await expect(known).toHaveCount(1);
    await expect(known.locator("summary .call-what")).toHaveText("Supabase · List projects");
    await expectLogo(known, "supabase");

    // Assert: an unknown connector falls back to its initial on a tile.
    const unknown = row("Acme Billing · Create invoice");
    await expect(unknown.locator("summary .call-what")).toHaveText("Acme Billing · Create invoice");
    await expect(unknown.locator("summary .call-icon .connector-initial")).toHaveText("A");
    await expect(unknown.locator("summary .call-icon img")).toHaveCount(0);

    // Assert: an MCP server's tool reads the same way.
    const server = row("Chrome DevTools · Take snapshot");
    await expect(server.locator("summary .call-what")).toHaveText("Chrome DevTools · Take snapshot");
    await expectLogo(server, "chrome");

    // Assert: a running call keeps what it was called on beside its words.
    const sql = row("Supabase · Execute SQL");
    await expect(sql.locator("summary .call-what")).toHaveText("Supabase · Execute SQL select count(*) from projects");
    await expect(sql.locator("summary")).toContainText("Running");
    await expectLogo(sql, "supabase");
    // What it was called on is cut before its tool's words are.
    const isWhole = await sql.locator("summary .call-action").evaluate((action) => {
      const words = document.createRange();
      words.selectNodeContents(action);
      return action.getBoundingClientRect().width >= words.getBoundingClientRect().width - 0.01;
    });
    expect(isWhole, "Execute SQL shows whole").toBe(true);

    // Assert: a tool search wears the wrench and says how many tools it loaded.
    const search = row("Loaded 4 tools");
    await expect(search.locator("summary .call-what")).toHaveText("Loaded 4 tools");
    await expect(search.locator("summary .call-icon .icon path")).toHaveAttribute("d", /^M20\.9 7\.1A5 5/);

    // Assert: no row shows a harness's prefix, and a folded run counts by connector.
    for (const prefix of ["claude_ai", "mcp__", "select:", "Called "]) await expect(messages.locator("summary").filter({ hasText: prefix })).toHaveCount(0);
    const fold = messages.locator("details.message-call-group", { hasText: "Used Supabase 3 times" });
    await expect(fold).toHaveCount(1);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`connector-rows-${width}.png`) });

    // Act: the raw call stays one click away.
    await known.locator("summary").click();
    await search.locator("summary").click();
    await fold.locator("> summary").click();

    // Assert
    await expect(known.locator(".disclosure-content .call-raw")).toHaveText("mcp__claude_ai_Supabase__list_projects");
    await expect(search.locator(".disclosure-content .call-loaded li")).toHaveText(["Supabase · List projects", "Supabase · List branches", "Acme Billing · Create invoice", "Chrome DevTools · Take snapshot"]);
    await expect(search.locator(".disclosure-content .call-full")).toHaveText(SELECT);
    await expect(fold.locator(".disclosure-content details.message-call summary .call-what")).toHaveText(["Supabase · List projects", "Supabase · List branches", "Supabase · Get project URL"]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`connector-rows-open-${width}.png`) });
  });
}
