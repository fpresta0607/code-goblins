import { expect, holdStream, test, type Page } from "./site";
import { treeFleet } from "./fixtures/tree-branches-fleet";
import { parseSnapshot } from "../src/types"

// The Overlord, 2026-10-08: "baby goblins should be clickable to open their
// agent terminals". A baby goblin opens the same task panel a goblin opens,
// with its name and its task in one line above its agent terminal: a
// sub-agent's own transcript as it runs, a helper goblin's own terminal, and
// for any other child its own output, never its goblin's terminal (the
// Overlord, 2026-10-13, of Clem II and Clem IV: "seems to be same terminal
// when i click on each card"). Baby goblins live on the canvas,
// never as cards on the board, whose goblin card counts them and opens the
// canvas on that goblin. The Overlord, 13:20Z: "why are the idle ones
// unclickable also what would be cool is the same goblin naming convention
// like Kip Jr. Kip II etc with a job title for that specific agent session".

const AGENT = "subagent:toolu_A";
// What the supervisor reads from the sub-agent's own transcript.
const TRANSCRIPT = ["> Find where the harness starts sub-agents and what each one records", "● Grep(subagents/agent-)", "  ⎿ Found 3 files", "● Reading internal/fleettree/claude.go"];
const OUTPUTS: Record<string, string[]> = {
  "subagent:tests": ["> Review the affected Go tests", "The affected tests pass"],
  "subagent:server": ["> Review the development server", "The app server answers"],
};

async function open(page: Page, width = 1600) {
  await page.setViewportSize({ width, height: 1000 });
  await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
  const fleet = parseSnapshot(treeFleet(Date.now()))
  const children = fleet.tasks[0].tree!.children
  Object.assign(children.find(child => child.id === "process:5120:1")!, { id: "subagent:server", kind: "subagent", label: "Review the development server", detail: "general-purpose", task: "Review the development server", state: "working" })
  children.find(child => child.id === "process:6100:2")!.state = "silent"
  await holdStream(page, fleet);
  await page.addInitScript(({ outputs, transcript }) => {
    const BoardStream = window.EventSource
    class ChildStream extends BoardStream {
      private timer: ReturnType<typeof setTimeout> | undefined
      private node = ""
      private snapshot = (event: Event) => this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify((event as CustomEvent).detail) }))
      private receive = (event: Event) => {
        const detail = (event as CustomEvent<{ node: string; lines: string[] }>).detail
        if (detail.node === this.node) this.dispatchEvent(new MessageEvent("output", { data: JSON.stringify({ lines: detail.lines }) }))
      }
      constructor(url: string | URL) {
        super(String(url))
        if (!String(url).includes("/agent?")) { window.addEventListener("fleet-snapshot", this.snapshot); return }
        this.node = new URL(url, location.href).searchParams.get("node") || ""
        window.addEventListener("child-output", this.receive)
        this.timer = setTimeout(() => this.dispatchEvent(new MessageEvent("output", { data: JSON.stringify({ lines: outputs[this.node] || transcript }) })), this.node === "subagent:server" ? 1500 : 0)
      }
      close() { super.close(); clearTimeout(this.timer); window.removeEventListener("child-output", this.receive); window.removeEventListener("fleet-snapshot", this.snapshot) }
    }
    Object.defineProperty(window, "EventSource", { value: ChildStream })
  }, { outputs: OUTPUTS, transcript: TRANSCRIPT })
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    const node = url.searchParams.get("node") || "";
    if (url.pathname === "/api/tasks/fleet/agent" && node === AGENT) await route.fulfill({ json: { lines: TRANSCRIPT } });
    else if (url.pathname === "/api/tasks/fleet/agent" && OUTPUTS[node]) await new Promise((done) => setTimeout(done, node === "subagent:server" ? 1500 : 0)).then(() => route.fulfill({ json: { lines: OUTPUTS[node] } }));
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  return fleet
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

test("clicking two other baby goblins shows each one's own output under its name and task, never its goblin's terminal", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  for (const [name, task, node, other] of [["Grub III - Test Runner", /Review the affected Go tests/, "subagent:tests", "subagent:server"], ["Grub IV - Server Reviewer", /Review the development server/, "subagent:server", "subagent:tests"]] as const) {
    await baby(page, name).click();
    const terminal = page.getByRole("log", { name: "Terminal of " + name });
    await expect(terminal, "never the last child's output while its own is on its way").not.toContainText(OUTPUTS[other].at(-1)!, { timeout: 500 });
    await expect(title(page)).toHaveText(new RegExp("^" + name + ".*" + task.source));
    expect(await isOneLine(page)).toBe(true);
    for (const line of OUTPUTS[node]) await expect(terminal).toContainText(line);
    await expect(terminal, "only its own output").not.toContainText(OUTPUTS[other].at(-1)!);
    await expect(page.locator(".deck-slot:not([hidden])").getByRole("region", { name: "Goblin terminal" })).toHaveCount(0);
  }
});

test("hovering a baby goblin shows its task at once", async ({ page }) => {
  await page.clock.install();
  await open(page);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  await expect(baby(page, "Grub Jr. - Plumbing Mapper")).toBeVisible();
  // Time stands still, so no wait of any length lets a tip show.
  await page.clock.pauseAt(Date.now() + 60_000);
  await baby(page, "Grub Jr. - Plumbing Mapper").hover();
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
  expect(await names("Grub - Tree Surgeon")).toEqual(["Grub Jr. - Plumbing Mapper", "Grub II - OAuth Researcher", "Grub III - Test Runner", "Grub IV - Server Reviewer"]);
  expect(await names("Kip - Echo Chaser")).toEqual(["Kip Jr. - Export Tracer"]);
  expect(await names("Moss - Pixel Wrangler"), "a helper goblin keeps its own name").toEqual(["Moss Jr. - Branch Measurer", "Moss II - List Checker", "Pip - Rail Fitter"]);
});

test("working subagents open their panels and processes have no baby card", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  for (const child of await page.locator(".tree-branches .tree-child").all()) expect(await child.evaluate((node) => getComputedStyle(node).opacity), await child.locator("strong").textContent() || "").toBe("1");
  await baby(page, "Grub IV - Server Reviewer").click();
  await expect(title(page)).toHaveText(/^Grub IV - Server Reviewer.*Review the development server/);
  await page.getByRole("button", { name: "Back to the CFO", exact: true }).click();
  await expect(baby(page, "Grub V - Process Wrangler")).toHaveCount(0)
});

test("a baby goblin shows no raw command line, its task is in its tip and its panel, and its state is a plain chip that is never yellow", async ({ page }) => {
  await page.clock.install();
  await open(page);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  await expect(page.locator(".tree-branches").first()).toBeVisible();
  for (const branches of await page.locator(".tree-branches").all()) await expect(branches).not.toContainText(/cmd\.exe|WINDOWS/);
  const reviewer = baby(page, "Grub III - Test Runner");
  const chip = reviewer.locator(".state-chip");
  await expect(chip).toHaveText("Working 40m");
  const look = (locator: typeof chip) => locator.evaluate((node) => ({ color: getComputedStyle(node).color, dot: getComputedStyle(node.querySelector(".status-dot")!).backgroundColor }));
  const working = await look(baby(page, "Grub Jr. - Plumbing Mapper").locator(".state-chip"));
  expect(await look(chip)).toEqual(working);
  await reviewer.hover();
  await expect(page.getByRole("tooltip")).toHaveText("Review the affected Go tests");
});

for (const [name, node, initial, next] of [
  ["Grub III - Test Runner", "subagent:tests", OUTPUTS["subagent:tests"], "The new test also passes"],
  ["Grub Jr. - Plumbing Mapper", AGENT, TRANSCRIPT, "Found the output capture"],
] as const) {
  test(`${name} follows new output without reopening the panel`, async ({ page }) => {
    await open(page)
    await page.getByRole("button", { name: "Orchestration", exact: true }).click()
    await baby(page, name).click()
    const terminal = page.getByRole("log", { name: "Terminal of " + name })
    await expect(terminal).toContainText(initial.at(-1)!)
    await page.evaluate(({ node, lines }) => window.dispatchEvent(new CustomEvent("child-output", { detail: { node, lines } })), { node, lines: [...initial, "", next] })
    await expect(terminal).toContainText(next)
    await expect(terminal).not.toContainText("(pid ")
  })
}

test("a process has no baby goblin card even when it starts working", async ({ page }) => {
  const fleet = await open(page)
  await page.getByRole("button", { name: "Orchestration", exact: true }).click()
  await expect(baby(page, "Grub V - Process Wrangler")).toHaveCount(0)
  fleet.tasks[0].tree!.children.find(child => child.id === "process:6100:2")!.state = "working"
  fleet.revision++
  await page.evaluate(snapshot => window.dispatchEvent(new CustomEvent("fleet-snapshot", { detail: snapshot })), fleet)
  await expect(baby(page, "Grub V - Process Wrangler")).toHaveCount(0)
})
