import { expect, test, type Locator, type Page } from "./site";
import { babyGoblinFleet, TALK, TRANSCRIPT } from "./fixtures/baby-goblin-fleet";

// The Overlord, 2026-11-26 (#2932): "Gotta clean up baby goblin display
// terminals don't connect like other goblins and naming is hard to discern
// and or can't open cards from kanban view nor are linked and displayed
// properly in modern chats". A goblin, Grub, has two sub-agents at work, one
// finished, a background shell and a test run; its sub-agents are its baby
// goblins, and every view names each the same and opens it to one panel,
// whose terminal connects as a goblin's does. The supervisor is played here:
// the board's snapshot, the goblin's conversation and each baby goblin's
// output on streams a test holds, and the refusal a child's output answers.

// The name each running sub-agent goes by: its goblin's, in the order the
// goblin's sub-agents started, the finished one first, and its own words.
const MAPPER = "Grub II - Map harness plumbing";
const CHECKER = "Grub III - Check the lineage list at phone width";

interface Refusal { status: number; error: string }

// open shows the board at a size, its baby goblins' output streams held:
// refused names a child whose stream the supervisor refuses, once or for
// good, with the refusal a plain read of that child's output answers.
async function open(page: Page, width: number, height: number, refused: Record<string, { times: number } & Refusal> = {}) {
  await page.setViewportSize({ width, height });
  await page.addInitScript(() => localStorage.setItem("ceo-first-open", "shown"));
  // No WebGL, so a baby goblin's terminal draws its text with xterm's DOM
  // renderer, where a test reads it.
  await page.addInitScript(() => {
    const getContext = HTMLCanvasElement.prototype.getContext as (this: HTMLCanvasElement, kind: string, options?: unknown) => RenderingContext | null;
    Object.defineProperty(HTMLCanvasElement.prototype, "getContext", { value(this: HTMLCanvasElement, kind: string, options?: unknown) { return kind === "webgl2" ? null : getContext.call(this, kind, options); } });
  });
  await page.addInitScript(({ snapshot, talk, transcript, refusals }) => {
    // A child's streams, newest last, which a test drops or sends output on.
    const children: { node: string; stream: Stream }[] = [];
    const refusedSoFar: Record<string, number> = {};
    // Once a stream has dropped, the sub-agent has written one more line.
    let hasDropped = false;
    const written = () => hasDropped ? [...transcript, { kind: "reply", text: "Back after the drop" }] : transcript;
    Object.assign(window, { childStreams: children });
    class Stream extends EventTarget {
      static CONNECTING = 0; static OPEN = 1; static CLOSED = 2;
      onerror: ((event: Event) => void) | null = null;
      readyState = 0;
      fail(state: number) {
        this.readyState = state;
        const event = new Event("error");
        this.onerror?.(event);
        this.dispatchEvent(event);
      }
      // drop ends a stream the way the supervisor does after ten minutes: the
      // browser says it is connecting again and, unless the page closed it,
      // opens it again by itself and reads the output anew.
      drop() {
        hasDropped = true;
        this.fail(0);
        setTimeout(() => { if (this.readyState === 0) this.say("output", { entries: written() }); }, 300);
      }
      say(type: string, data: unknown) {
        this.readyState = 1;
        this.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data) }));
      }
      constructor(address: string | URL) {
        super();
        const url = new URL(address, location.href);
        const later = (act: () => void) => setTimeout(() => { if (this.readyState !== 2) act(); });
        if (url.pathname === "/api/messages") {
          later(() => {
            this.say("page", talk);
            this.say("session", { conversation: talk.conversation, turn: "", state: "running", why: "", newest: 1, head: 1 });
          });
        } else if (url.pathname.endsWith("/agent")) {
          const node = url.searchParams.get("node") || "";
          children.push({ node, stream: this });
          const refusal = refusals[node];
          refusedSoFar[node] = (refusedSoFar[node] || 0) + 1;
          // A refused stream ends before it opens, as EventSource does on an
          // answer other than 200, and never tries again by itself.
          if (refusal && refusedSoFar[node] <= refusal.times) later(() => this.fail(2));
          else later(() => this.say("output", { entries: written() }));
        } else later(() => this.say("snapshot", JSON.parse(snapshot)));
      }
      close() { this.readyState = 2; }
    }
    Object.defineProperty(window, "EventSource", { value: Stream });
  }, { snapshot: JSON.stringify(babyGoblinFleet(Date.now())), talk: TALK, transcript: TRANSCRIPT, refusals: refused });
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    const refusal = refused[url.searchParams.get("node") || ""];
    if (url.pathname === "/api/tasks/grub/agent" && refusal) await route.fulfill({ status: refusal.status, json: { error: refusal.error } });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  // The goblin's own terminal, which the chat keeps connected behind it.
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.onMessage((data) => {
      if (typeof data !== "string") return;
      const sent = JSON.parse(data) as { type?: string; cols?: number; rows?: number };
      if (sent.type === "resize") { socket.send(JSON.stringify({ type: "size", cols: sent.cols, rows: sent.rows })); socket.send(Buffer.from("ready\r\n")); }
    });
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
}

const title = (page: Page) => page.locator("#panel-title");
// A baby goblin found by what it does, whatever it is called, so a test of
// another part than its name never fails by its name.
const MAPPING = /Map harness plumbing/, CHECKING = /Check the lineage list at phone width/;
// What each was asked, which its panel says beside its name.
const MAPPING_TASK = /^Find where the harness starts sub-agents/, CHECKING_TASK = /^Check how the lineage list draws at phone width/;
const grubCard = (page: Page) => page.locator(".task-board .ranked-item").filter({ has: page.locator(".task-card", { hasText: "Grub - Tree Surgeon" }) });

// expectBabyPanel checks a baby goblin opened to its one panel: its name and
// task above its own terminal, which draws its transcript.
async function expectBabyPanel(page: Page, asked: RegExp) {
  await expect(page.locator(".baby-header")).toBeVisible();
  await expect(page.locator(".baby-header .panel-child-task")).toHaveText(asked);
  const terminal = page.locator(".agent-terminal");
  await expect(terminal).toContainText("Search(subagents/agent-)");
  return terminal;
}

// The orchestration view's baby goblins: on the canvas, or at phone width on
// the lineage list's rail under its goblin's count.
async function orchestrationBabies(page: Page, isPhone: boolean): Promise<Locator> {
  await page.getByRole("button", { name: "Orchestration", exact: true }).click();
  if (!isPhone) return page.locator(".tree-branches");
  await page.getByRole("button", { name: /^Show what runs under Grub - Tree Surgeon/ }).click();
  return page.locator(".tree-rail");
}

// The board's baby goblins, under the goblin's card, in its kanban or
// stacked layout.
async function boardBabies(page: Page, layout: "kanban" | "stacked"): Promise<Locator> {
  if (layout === "stacked" && await page.getByRole("button", { name: "Stacked layout" }).count()) await page.getByRole("button", { name: "Stacked layout" }).click();
  await grubCard(page).getByRole("button", { name: /^Show what runs under Grub - Tree Surgeon: 2 working/ }).click();
  return grubCard(page).locator(".tree-rail");
}

async function openChat(page: Page) {
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await page.locator(".task-board .task-card", { hasText: "Grub - Tree Surgeon" }).click();
  await expect(title(page)).toHaveText("Grub - Tree Surgeon");
  return page.getByRole("log", { name: "Messages of Grub - Tree Surgeon" });
}

test.describe("1. a baby goblin's terminal connects as a goblin's does", () => {
  for (const [width, height] of [[1600, 1000], [390, 844]]) {
    test(`it fills its panel under its name at ${width}px`, async ({ page }) => {
      await open(page, width, height);
      await (await orchestrationBabies(page, width < 600)).getByRole("button", { name: MAPPING }).click();
      const terminal = await expectBabyPanel(page, MAPPING_TASK);
      const header = (await page.locator(".baby-header").boundingBox())!, drawn = (await terminal.boundingBox())!;
      const panel = (await page.locator(".context-pane").boundingBox())!;
      expect(drawn.y - (header.y + header.height), "the terminal starts right under its header").toBeLessThan(24);
      expect(panel.y + panel.height - (drawn.y + drawn.height), "and runs to the panel's bottom").toBeLessThan(24);
    });
  }

  test("a stream the supervisor refuses for now is tried again until it connects", async ({ page }) => {
    await open(page, 1600, 1000, { "subagent:toolu_A": { times: 2, status: 429, error: "Too many event streams" } });
    await (await orchestrationBabies(page, false)).getByRole("button", { name: MAPPING }).click();
    await expectBabyPanel(page, MAPPING_TASK);
    await expect(page.locator(".agent-terminal").getByText(/Reconnecting/)).toHaveCount(0);
  });

  test("a stream refused for good says why and offers Reconnect, never Reconnecting forever", async ({ page }) => {
    await open(page, 1600, 1000, { "subagent:toolu_A": { times: 99, status: 403, error: "Child output is available only on this computer" } });
    await (await orchestrationBabies(page, false)).getByRole("button", { name: MAPPING }).click();
    await expect(page.locator(".baby-header")).toBeVisible();
    const terminal = page.locator(".agent-terminal");
    await expect(terminal).toContainText("Child output is available only on this computer");
    await expect(terminal.getByRole("button", { name: "Reconnect" })).toBeVisible();
    await expect(terminal.getByText(/Reconnecting/)).toHaveCount(0);
  });

  test("a stream that drops keeps its screen while it connects again", async ({ page }) => {
    await open(page, 1600, 1000);
    await (await orchestrationBabies(page, false)).getByRole("button", { name: MAPPING }).click();
    const terminal = await expectBabyPanel(page, MAPPING_TASK);
    // The supervisor ends a stream after ten minutes, and the browser's
    // EventSource then says it is connecting again.
    await page.evaluate(() => { const held = (window as unknown as { childStreams: { stream: { drop: () => void } }[] }).childStreams; held[held.length - 1].stream.drop(); });
    await expect(terminal).toContainText("Search(subagents/agent-)");
    await expect(terminal).toContainText("Back after the drop");
    await expect(terminal).toContainText("Reading internal/tree/claude.go");
  });
});

test.describe("2. a baby goblin reads by one name, whose it is and what it does, in every view", () => {
  test("on the canvas, in its panel and in the chat at 1600px", async ({ page }) => {
    await open(page, 1600, 1000);
    const canvas = await orchestrationBabies(page, false);
    await expect(canvas.getByRole("button", { name: new RegExp("^" + MAPPER) })).toBeVisible();
    await expect(canvas.getByRole("button", { name: new RegExp("^" + CHECKER) })).toBeVisible();
    await canvas.getByRole("button", { name: new RegExp("^" + CHECKER) }).click();
    await expect(title(page)).toHaveText(new RegExp("^" + CHECKER));
    const chat = await openChat(page);
    await expect(chat.locator(".message-baby").nth(0)).toContainText(MAPPER);
    await expect(chat.locator(".message-baby").nth(1)).toContainText(CHECKER);
  });

  for (const layout of ["kanban", "stacked"] as const) {
    test(`on the board's ${layout} card`, async ({ page }) => {
      await open(page, 1600, 1000);
      const rail = await boardBabies(page, layout);
      await expect(rail.getByRole("button", { name: new RegExp("^" + MAPPER) })).toBeVisible();
      await expect(rail.getByRole("button", { name: new RegExp("^" + CHECKER) })).toBeVisible();
    });
  }

  test("in the lineage list at 390px", async ({ page }) => {
    await open(page, 390, 844);
    const rail = await orchestrationBabies(page, true);
    await expect(rail.getByRole("button", { name: new RegExp("^" + MAPPER) })).toBeVisible();
    await expect(rail.getByRole("button", { name: new RegExp("^" + CHECKER) })).toBeVisible();
  });
});

test.describe("3. a goblin's kanban card shows its running baby goblins and opens each", () => {
  for (const [layout, width, height] of [["kanban", 1600, 1000], ["stacked", 1600, 1000], ["stacked", 390, 844]] as const) {
    test(`in the ${layout} layout at ${width}px`, async ({ page }) => {
      await open(page, width, height);
      const rail = await boardBabies(page, layout);
      // Only what runs as a baby goblin hangs there: its two sub-agents at
      // work, not the finished one, the shell or the test run.
      await expect(rail.getByRole("button")).toHaveCount(2);
      await expect(page.locator(".task-board .ranked-item").filter({ hasText: "Kip - Echo Chaser" }).locator(".tree-count")).toHaveCount(0);
      await rail.getByRole("button", { name: CHECKING }).click();
      await expectBabyPanel(page, CHECKING_TASK);
    });
  }
});

test.describe("4. every sub-agent call in Modern Chat is linked to its baby goblin", () => {
  for (const [width, height] of [[1600, 1000], [390, 844]]) {
    test(`it shows the baby goblin's name and state and opens its panel at ${width}px`, async ({ page }) => {
      await open(page, width, height);
      const chat = await openChat(page);
      // A sub-agent started in the background: its call came back at once,
      // and its baby goblin is still at work.
      const mapper = chat.getByRole("button", { name: /^Open .*Map harness plumbing/ });
      await expect(mapper).toBeVisible();
      await expect(mapper).toContainText("Working");
      await expect(chat.getByRole("button", { name: /^Open .*Check the lineage list at phone width/ })).toContainText("Working");
      // A call whose sub-agent its goblin's tree no longer holds says what it
      // did by its own words, never "Baby goblin", and opens nothing.
      const gone = chat.locator(".message-baby").nth(2);
      await expect(gone).toContainText("Survey the old canvas");
      await expect(gone).not.toContainText("Baby goblin");
      await expect(gone.getByRole("button")).toHaveCount(0);
      await mapper.click();
      await expectBabyPanel(page, MAPPING_TASK);
    });
  }
});
