import { expect, holdStream, test, type Page } from "./site";
import { treeFleet } from "./fixtures/tree-branches-fleet";

// The Overlord, 2026-10-08: "baby goblins should be clickable to open their
// agent terminals". A baby goblin opens the same task panel a goblin opens,
// with its name and its task in one line above its agent terminal: a
// sub-agent's own transcript as it runs, a helper goblin's own terminal, and
// for any other child its goblin's terminal. Baby goblins live on the canvas,
// never as cards on the board, whose goblin card counts them and opens the
// canvas on that goblin. The Overlord, 13:20Z: "why are the idle ones
// unclickable also what would be cool is the same goblin naming convention
// like Kip Jr. Kip II etc with a job title for that specific agent session".

const AGENT = "subagent:toolu_A";
// What the supervisor reads from the sub-agent's own transcript.
const TRANSCRIPT = ["> Find where the harness starts sub-agents and what each one records", "● Grep(subagents/agent-)", "  ⎿ Found 3 files", "● Reading internal/fleettree/claude.go"];

async function open(page: Page, width = 1600) {
  await page.setViewportSize({ width, height: 1000 });
  await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
  await holdStream(page, treeFleet(Date.now()));
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/api/tasks/fleet/agent" && url.searchParams.get("node") === AGENT) await route.fulfill({ json: { lines: TRANSCRIPT } });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
}

const baby = (page: Page, label: string) => page.locator(".tree-branches").getByRole("button", { name: new RegExp("^" + label) });
const title = (page: Page) => page.locator("#panel-title");

// A heading on one line: its box no taller than one of its lines.
const isOneLine = (page: Page) => title(page).evaluate((heading) => heading.getBoundingClientRect().height < parseFloat(getComputedStyle(heading).lineHeight) * 1.5);

test("clicking a sub-agent opens its own transcript live, under its name and its task on one line", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  await baby(page, "Grub Jr. - Plumbing Mapper").click();
  await expect(title(page)).toHaveText(/^Grub Jr\. - Plumbing Mapper.*Find where the harness starts sub-agents/);
  expect(await isOneLine(page)).toBe(true);
  const terminal = page.getByRole("log", { name: "Terminal of Grub Jr. - Plumbing Mapper" });
  await expect(terminal).toBeVisible();
  for (const line of TRANSCRIPT) await expect(terminal).toContainText(line);
  expect((await terminal.boundingBox())!.y, "under its name and task").toBeGreaterThan((await title(page).boundingBox())!.y);
  await expect(page.getByRole("button", { name: "Back to the CFO", exact: true })).toBeVisible();
});

test("clicking a helper goblin opens that helper's own panel", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  await baby(page, "Pip - Rail Fitter").click();
  await expect(title(page)).toHaveText("Pip - Rail Fitter");
  await expect(page.locator(".panel-goblin-task")).toHaveText("Fit the phone rails");
});

test("clicking any other baby goblin opens its goblin's terminal under the baby goblin's name and task", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  await baby(page, "Grub III - Test Runner").click();
  await expect(title(page)).toHaveText(/^Grub III - Test Runner.*go test \.\/internal\/monitor/);
  expect(await isOneLine(page)).toBe(true);
  await expect(page.locator(".deck-slot:not([hidden])").getByRole("region", { name: "Goblin terminal" })).toHaveCount(1);
  await expect(page.getByRole("log")).toHaveCount(0);
});

test("hovering a baby goblin shows its task after the tip's 2 second wait", async ({ page }) => {
  await page.clock.install();
  await open(page);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  await expect(baby(page, "Grub Jr. - Plumbing Mapper")).toBeVisible();
  // Time moves only as the test moves it, however long the machine takes.
  await page.clock.pauseAt(Date.now() + 60_000);
  await baby(page, "Grub Jr. - Plumbing Mapper").hover();
  await page.clock.runFor(1500);
  await expect(page.getByRole("tooltip")).toHaveCount(0);
  await page.clock.runFor(600);
  await expect(page.getByRole("tooltip")).toHaveText("Find where the harness starts sub-agents and what each one records");
});

test("dragging from a baby goblin moves the canvas and opens nothing, and a key still opens it", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  const before = (await baby(page, "Kip Jr. - Export Tracer").boundingBox())!;
  await page.mouse.move(before.x + 20, before.y + 20);
  await page.mouse.down();
  await page.mouse.move(before.x + 140, before.y + 80, { steps: 8 });
  await page.mouse.up();
  const after = (await baby(page, "Kip Jr. - Export Tracer").boundingBox())!;
  expect(after.x - before.x).toBeCloseTo(120, 0);
  await expect(title(page)).toHaveText("CFO");
  // A key still opens it after the pan.
  await baby(page, "Kip Jr. - Export Tracer").focus();
  await page.keyboard.press("Enter");
  await expect(title(page)).toHaveText(/^Kip Jr\. - Export Tracer/);
});

test("baby goblins have no cards on the board, and a goblin's count opens the canvas centered on it", async ({ page }) => {
  await open(page);
  const cards = page.locator(".task-board .task-card-shell");
  for (const label of ["Grub Jr.", "Map harness plumbing", "Run the affected Go tests", "Dev server :5173", "Kip Jr.", "Trace the export"]) await expect(cards.filter({ hasText: label })).toHaveCount(0);
  const card = cards.filter({ has: page.locator(".card-title", { hasText: "Grub - Tree Surgeon" }) });
  const count = card.getByRole("button", { name: /^Show what runs under Grub - Tree Surgeon on the canvas/ });
  await expect(count.getByRole("img", { name: "Sub-agent" })).toHaveCount(1);
  await count.click();
  await expect(page.getByRole("button", { name: "Orchestration", exact: true })).toHaveAttribute("aria-pressed", "true");
  const canvas = (await page.locator(".flow-canvas").boundingBox())!;
  const goblin = page.locator(".flow-node").filter({ hasText: "Grub - Tree Surgeon" });
  await expect.poll(async () => { const box = (await goblin.boundingBox())!; return Math.abs(box.x + box.width / 2 - (canvas.x + canvas.width / 2)); }, "the goblin in the middle of the canvas").toBeLessThanOrEqual(1);
  for (const part of [await goblin.boundingBox(), ...await Promise.all((await page.getByRole("list", { name: "What runs under Grub - Tree Surgeon" }).locator(":scope > li").all()).map((li) => li.boundingBox()))]) {
    expect(part!.y, "the goblin and its baby goblins in view").toBeGreaterThanOrEqual(canvas.y);
    expect(part!.y + part!.height).toBeLessThanOrEqual(canvas.y + canvas.height);
  }
  await expect(page.locator(".flow-node").filter({ hasText: "Polish the canvas" }), "closer than the whole tree's fit").not.toBeInViewport();
});

test("at phone width a baby goblin on its rail opens its panel too", async ({ page }) => {
  await open(page, 390);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  await page.getByRole("button", { name: /^Show what runs under Grub - Tree Surgeon/ }).click();
  await page.getByRole("list", { name: "What runs under Grub - Tree Surgeon" }).getByRole("button", { name: /^Grub Jr\. - Plumbing Mapper/ }).click();
  await expect(title(page)).toHaveText(/^Grub Jr\. - Plumbing Mapper/);
  await expect(page.getByRole("log", { name: "Terminal of Grub Jr. - Plumbing Mapper" })).toContainText("Grep(subagents/agent-)");
});

test("baby goblins are named after their goblin in the order they started, each with a title from its own description", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  const names = (label: string) => page.getByRole("list", { name: "What runs under " + label }).locator(".tree-child strong").allTextContents();
  expect(await names("Grub - Tree Surgeon")).toEqual(["Grub Jr. - Plumbing Mapper", "Grub II - OAuth Researcher", "Grub III - Test Runner", "Grub IV - Server Keeper", "Grub V - Process Wrangler"]);
  expect(await names("Kip - Echo Chaser")).toEqual(["Kip Jr. - Export Tracer"]);
  expect(await names("Moss - Pixel Wrangler"), "a helper goblin keeps its own name").toEqual(["Moss Jr. - Branch Measurer", "Moss II - List Checker", "Pip - Rail Fitter"]);
});

test("every baby goblin, idle and silent ones too, opens its panel and none is greyed", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  for (const child of await page.locator(".tree-branches .tree-child").all()) expect(await child.evaluate((node) => getComputedStyle(node).opacity), await child.locator("strong").textContent() || "").toBe("1");
  await baby(page, "Grub IV - Server Keeper").click();
  await expect(title(page)).toHaveText(/^Grub IV - Server Keeper.*npm run dev/);
  await page.getByRole("button", { name: "Back to the CFO", exact: true }).click();
  await baby(page, "Grub V - Process Wrangler").click();
  await expect(title(page)).toHaveText(/^Grub V - Process Wrangler.*python -I survey\.py/);
});

test("a baby goblin shows no raw command line, its task is in its tip and its panel, and its state is a plain chip that is never yellow", async ({ page }) => {
  await page.clock.install();
  await open(page);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  await expect(page.locator(".tree-branches").first()).toBeVisible();
  for (const branches of await page.locator(".tree-branches").all()) await expect(branches).not.toContainText(/cmd\.exe|WINDOWS/);
  const silent = baby(page, "Grub V - Process Wrangler");
  const chip = silent.locator(".state-chip");
  await expect(chip).toHaveText("Silent 34m");
  const look = (locator: typeof chip) => locator.evaluate((node) => ({ color: getComputedStyle(node).color, dot: getComputedStyle(node.querySelector(".status-dot")!).backgroundColor }));
  const working = await look(baby(page, "Grub Jr. - Plumbing Mapper").locator(".state-chip"));
  const idle = await look(baby(page, "Grub IV - Server Keeper").locator(".state-chip"));
  expect(await look(chip), "the plain text of every state, and the dot of an idle one").toEqual({ color: working.color, dot: idle.dot });
  await silent.hover();
  await page.clock.runFor(2100);
  await expect(page.getByRole("tooltip")).toHaveText("python -I survey.py");
});
