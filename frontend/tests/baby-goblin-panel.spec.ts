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
// What the supervisor reads from the sub-agent's own transcript, and the
// lines its terminal draws of it as Claude Code would.
const LONG_PATH = "internal/fleettree/a-folder-name-long-enough-to-wrap/and-one-more-folder-below-it/agent_terminal_windows_test.go";
const TRANSCRIPT = [
  { kind: "prompt", text: "Find where the harness starts sub-agents and what each one records" },
  { kind: "call", tool: "Search", subject: "subagents/agent-", state: "done", result: ["Found 3 files"] },
  { kind: "call", tool: "Read", subject: LONG_PATH, state: "failed", result: ["Error: File does not exist."] },
  { kind: "call", tool: "Bash", subject: "go test ./internal/fleettree/", state: "done", result: ["=== RUN TestA", "--- PASS: TestA", "ok  fleettree 1.2s"], more: 2 },
  { kind: "reply", text: "Reading internal/fleettree/claude.go" },
];
const DRAWN = ["> Find where the harness starts sub-agents", "● Search(subagents/agent-)", "⎿  Found 3 files", "⎿  Error: File does not exist.", "● Bash(go test ./internal/fleettree/)", "⎿  === RUN TestA", "--- PASS: TestA", "… +2 lines", "● Reading internal/fleettree/claude.go"];
const OUTPUTS: Record<string, string[]> = {
  "subagent:tests": ["> Review the affected Go tests", "The affected tests pass"],
  "subagent:server": ["> Review the development server", "The app server answers"],
};

async function open(page: Page, width = 1600, height = 1000) {
  await page.setViewportSize({ width, height });
  await page.addInitScript(() => localStorage.setItem("cfo-first-open", "shown"));
  // No WebGL, so xterm draws with its DOM renderer on every machine, the cells
  // it draws readable with their colours, as a browser without a GPU draws them.
  await page.addInitScript(() => {
    const getContext = HTMLCanvasElement.prototype.getContext as (this: HTMLCanvasElement, kind: string, options?: unknown) => RenderingContext | null
    Object.defineProperty(HTMLCanvasElement.prototype, "getContext", { value(this: HTMLCanvasElement, kind: string, options?: unknown) { return kind === "webgl2" ? null : getContext.call(this, kind, options) } })
  })
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
        const detail = (event as CustomEvent<{ node: string; output: unknown }>).detail
        if (detail.node === this.node) this.dispatchEvent(new MessageEvent("output", { data: JSON.stringify(detail.output) }))
      }
      constructor(url: string | URL) {
        super(String(url))
        if (!String(url).includes("/agent?")) { window.addEventListener("fleet-snapshot", this.snapshot); return }
        this.node = new URL(url, location.href).searchParams.get("node") || ""
        window.addEventListener("child-output", this.receive)
        this.timer = setTimeout(() => this.dispatchEvent(new MessageEvent("output", { data: JSON.stringify(outputs[this.node] ? { lines: outputs[this.node] } : { entries: transcript }) })), this.node === "subagent:server" ? 1500 : 0)
      }
      close() { super.close(); clearTimeout(this.timer); window.removeEventListener("child-output", this.receive); window.removeEventListener("fleet-snapshot", this.snapshot) }
    }
    Object.defineProperty(window, "EventSource", { value: ChildStream })
  }, { outputs: OUTPUTS, transcript: TRANSCRIPT })
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    const node = url.searchParams.get("node") || "";
    if (url.pathname === "/api/tasks/fleet/agent" && node === AGENT) await route.fulfill({ json: { entries: TRANSCRIPT } });
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
  for (const line of DRAWN) await expect(terminal).toContainText(line);
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
  await baby(page, "Grub Jr. - Plumbing Mapper").locator(".tree-child-model").hover();
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

// A goblin's card is one row since 2026-10-17, so it shows no count of what
// runs under it: the canvas and its panel's What's working do.
test("baby goblins have no cards on the board, and their goblin's card no count of them", async ({ page }) => {
  await open(page);
  const cards = page.locator(".task-board .task-card-shell");
  for (const label of ["Grub Jr.", "Map harness plumbing", "Run the affected Go tests", "Dev server :5173", "Kip Jr.", "Trace the export"]) await expect(cards.filter({ hasText: label })).toHaveCount(0);
  const card = cards.filter({ has: page.locator(".card-title", { hasText: "Grub - Tree Surgeon" }) });
  await expect(card).toHaveCount(1);
  await expect(card.getByRole("img", { name: "Sub-agent" })).toHaveCount(0);
  await expect(card.getByRole("button", { name: /^Show what runs under/ })).toHaveCount(0);
});

test("at phone width a baby goblin on its rail opens its panel too", async ({ page }) => {
  await open(page, 390);
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  await page.getByRole("button", { name: /^Show what runs under Grub - Tree Surgeon/ }).click();
  await page.getByRole("list", { name: "What runs under Grub - Tree Surgeon" }).getByRole("button", { name: /^Grub Jr\. - Plumbing Mapper/ }).click();
  await expect(title(page)).toHaveText(/^Grub Jr\. - Plumbing Mapper/);
  await expect(page.getByRole("log", { name: "Terminal of Grub Jr. - Plumbing Mapper" })).toContainText("● Search(subagents/agent-)");
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

for (const [name, node, last, output, next] of [
  ["Grub III - Test Runner", "subagent:tests", OUTPUTS["subagent:tests"].at(-1)!, { lines: [...OUTPUTS["subagent:tests"], "", "The new test also passes"] }, "The new test also passes"],
  ["Grub Jr. - Plumbing Mapper", AGENT, DRAWN.at(-1)!, { entries: [...TRANSCRIPT, { kind: "reply", text: "Found the output capture" }] }, "● Found the output capture"],
] as const) {
  test(`${name} follows new output without reopening the panel`, async ({ page }) => {
    await open(page)
    await page.getByRole("button", { name: "Orchestration", exact: true }).click()
    await baby(page, name).click()
    const terminal = page.getByRole("log", { name: "Terminal of " + name })
    await expect(terminal).toContainText(last)
    await page.evaluate(({ node, output }) => window.dispatchEvent(new CustomEvent("child-output", { detail: { node, output } })), { node, output })
    await expect(terminal).toContainText(next)
    await expect(terminal).not.toContainText("(pid ")
  })
}

// The Overlord, 2026-10-15: "Baby goblins should look and feel just like the
// same Claude shells, right? They're Claude shells to begin with". A
// sub-agent's transcript is drawn in a goblin terminal's surface, at its font
// and size, with Claude Code's marks in Claude Code's colours: a call's dot in
// the colour of its outcome, its tool in bold, what came back in grey under
// the elbow, an error in red, the rest folded dim, and Claude Code's working
// line in its orange while the baby goblin works.
const row = (page: Page, start: string) => page.locator(".agent-terminal .xterm-rows > div").filter({ hasText: new RegExp("^\\s*" + start.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")) }).first()
// The look of the first cell run holding text in the row starting with start.
// The spinner redraws rows, so a run read as it is replaced is read again.
const colourOf = (page: Page, start: string, text: string) => row(page, start).locator("span").filter({ hasText: text }).evaluateAll((spans) => spans.filter((span) => span.isConnected).map((span) => ({ color: getComputedStyle(span).color, weight: getComputedStyle(span).fontWeight }))[0] ?? { color: "", weight: "" })

test("a sub-agent's transcript is drawn in the goblin terminal's surface with Claude Code's marks and colours", async ({ page }) => {
  // Tall enough to show the whole transcript.
  await open(page, 1600, 1500)
  await page.getByRole("button", { name: "Orchestration", exact: true }).click()
  await baby(page, "Grub Jr. - Plumbing Mapper").click()
  const terminal = page.getByRole("log", { name: "Terminal of Grub Jr. - Plumbing Mapper" })
  await expect(terminal.locator(".xterm-rows")).toContainText("● Search(subagents/agent-)")
  const look = await terminal.locator(".xterm").evaluate((xterm) => ({ size: getComputedStyle(xterm.querySelector(".xterm-rows")!).fontSize, family: getComputedStyle(xterm.querySelector(".xterm-rows")!).fontFamily, background: getComputedStyle(xterm.querySelector(".xterm-viewport")!).backgroundColor }))
  expect(look, "a goblin terminal's font, size and background").toEqual({ size: "20px", family: '"JetBrains Mono", "Cascadia Mono", Consolas, monospace', background: "rgb(7, 16, 21)" })
  await expect.poll(() => colourOf(page, "● Search", "●"), "a done call's dot is green").toEqual({ color: "rgb(78, 186, 101)", weight: "400" })
  await expect.poll(async () => (await colourOf(page, "● Search", "Search")).weight, "the tool in bold").toBe("700")
  await expect.poll(async () => (await colourOf(page, "● Read(", "●")).color, "a failed call's dot is red").toBe("rgb(255, 107, 128)")
  await expect.poll(async () => (await colourOf(page, "⎿  Error", "Error")).color, "its error is red").toBe("rgb(255, 107, 128)")
  await expect.poll(async () => (await colourOf(page, "⎿  Found", "Found")).color, "what came back is grey").toBe("rgb(153, 153, 153)")
  await expect.poll(async () => (await colourOf(page, "> Find", "Find")).color, "the prompt in the terminal's own colour").toBe("rgb(216, 233, 226)")
  await expect(terminal.locator(".xterm-rows")).toContainText(/[·✢✳✶✻✽] Working… \(50m \d+s\)/)
  await expect.poll(() => terminal.locator(".xterm-rows span").filter({ hasText: "Working…" }).evaluateAll((spans) => spans.filter((span) => span.isConnected).map((span) => getComputedStyle(span).color)[0] ?? ""), "the working line in Claude Code's orange").toBe("rgb(215, 119, 87)")
})

for (const width of [1600, 390]) {
  test(`a long path wraps where the terminal's row ends, under the text, at ${width} wide`, async ({ page }) => {
    await open(page, width)
    await page.getByRole("button", { name: "Orchestration", exact: true }).click()
    if (width < 600) await page.getByRole("button", { name: /^Show what runs under Grub - Tree Surgeon/ }).click()
    await page.getByRole("list", { name: "What runs under Grub - Tree Surgeon" }).getByRole("button", { name: /^Grub Jr\. - Plumbing Mapper/ }).click()
    const rows = page.locator(".agent-terminal .xterm-rows")
    await expect(rows).toContainText("● Read(")
    const lines = await rows.locator(":scope > div").evaluateAll((divs) => divs.map((div) => div.textContent!.replace(/\s+$/, "")))
    const cols = await page.locator(".agent-terminal .xterm-rows").evaluate((element) => element.parentElement!.getBoundingClientRect().width / (element.querySelector("span")!.getBoundingClientRect().width / element.querySelector("span")!.textContent!.length))
    const start = lines.findIndex((line) => line.startsWith("● Read("))
    const end = lines.findIndex((line, index) => index > start && line.startsWith("  ⎿"))
    const call = lines.slice(start, end)
    expect(call.length, "the path takes more than one row").toBeGreaterThan(1)
    for (const line of call.slice(0, -1)) expect(Math.abs(line.length - cols), `a full row: ${line}`).toBeLessThan(1)
    for (const line of call.slice(1)) expect(line, "under the text, never under the dot").toMatch(/^ {2}\S/)
    expect(call.map((line) => line.slice(2)).join(""), "nothing lost at the breaks").toBe("Read(" + LONG_PATH + ")")
  })
}

test("a working sub-agent keeps its last lines in view as its transcript grows past the screen", async ({ page }) => {
  await open(page)
  await page.getByRole("button", { name: "Orchestration", exact: true }).click()
  await baby(page, "Grub Jr. - Plumbing Mapper").click()
  const rows = page.locator(".agent-terminal .xterm-rows")
  await expect(rows).toContainText("● Reading internal/fleettree/claude.go")
  const steps = Array.from({ length: 60 }, (_, index) => ({ kind: "call", tool: "Bash", subject: `go test -run TestStep${index}`, state: "done", result: [`ok  step ${index}`] }))
  await page.evaluate((output) => window.dispatchEvent(new CustomEvent("child-output", { detail: { node: "subagent:toolu_A", output } })), { entries: [...TRANSCRIPT, ...steps] })
  await expect(rows).toContainText("● Bash(go test -run TestStep59)")
  await expect(rows, "the oldest lines scrolled up out of view").not.toContainText("Find where the harness")
  await expect(rows, "the working line under the last call").toContainText("Working…")
})

test("a transcript replaced while its previous output is queued starts on a clean terminal", async ({ page }) => {
  await open(page)
  await page.getByRole("button", { name: "Orchestration", exact: true }).click()
  await baby(page, "Grub Jr. - Plumbing Mapper").click()
  await expect(page.locator(".agent-terminal .xterm-rows")).toContainText("Reading internal/fleettree/claude.go")
  await page.evaluate(() => {
    const send = (entries: unknown[]) => window.dispatchEvent(new CustomEvent("child-output", { detail: { node: "subagent:toolu_A", output: { entries } } }))
    send(Array.from({ length: 60 }, (_, index) => ({ kind: "reply", text: `Previous step ${index}` })))
    send([{ kind: "prompt", text: "Replacement prompt" }, { kind: "reply", text: "Replacement reply" }])
  })
  await expect(row(page, "> Replacement prompt")).toBeVisible()
  await expect(row(page, "● Replacement reply")).toBeVisible()
  await expect(page.locator(".agent-terminal .xterm-rows")).not.toContainText("Previous step")
})

test("a process has no baby goblin card even when it starts working", async ({ page }) => {
  const fleet = await open(page)
  await page.getByRole("button", { name: "Orchestration", exact: true }).click()
  await expect(baby(page, "Grub V - Process Wrangler")).toHaveCount(0)
  fleet.tasks[0].tree!.children.find(child => child.id === "process:6100:2")!.state = "working"
  fleet.revision++
  await page.evaluate(snapshot => window.dispatchEvent(new CustomEvent("fleet-snapshot", { detail: snapshot })), fleet)
  await expect(baby(page, "Grub V - Process Wrangler")).toHaveCount(0)
})
