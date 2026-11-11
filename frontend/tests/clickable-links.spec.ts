import { readFileSync } from "node:fs";
import { expect, holdStream, openTaskList, openTerminalAddress, test, type Page, type TestInfo } from "./site";
import { at, GOBLIN, NAME, open, openGoblin, pickStyle, snapshot } from "./session-board";

// Web addresses on the board open (#2360). The Overlord, 2026-11-11: "any
// type of URL like inside chat messages or UI or like the Galax chats. I
// can't click them to open them. Should be clickable links or buttons." A
// goblin's chat, its panel's report and its terminal each show addresses;
// every http or https one is a link that opens in a new tab, a pull request,
// issue or CI run by its short name, and nothing else in a message becomes
// something that runs.

const PR = "https://github.com/example/board/pull/412";
const ISSUE = "https://github.com/example/board/issues/2360";
const RUN = "https://github.com/example/board/actions/runs/99";
const PAGE_ADDRESS = "https://docs.example.org/guide/start";
const REVIEW = "http://127.0.0.1:4387/session/a1b2";
const LONG = "https://docs.example.org/" + "a-very-long-path-segment-".repeat(8) + "end";
const CHAT = { conversation: "fixture-conversation", more: false, messages: [
  { id: "m0", seq: 0, at, kind: "prompt", source: "overlord", text: "Where is the fix?" },
  { id: "m1", seq: 1, at, kind: "reply", blocks: [{ kind: "text", text: [
    `Opened ${PR} for ${ISSUE}, and CI is ${RUN}. Notes are on ${REVIEW}.`,
    `The guide is at ${PAGE_ADDRESS}, and [the fix](${PR}) waits.`,
    `Not links: javascript:alert(1), file:///C:/x and <img src=x onerror="window.ran=1">.`,
    `A long one: ${LONG}`,
    "```bash\ngh pr view " + PR + "\n```",
  ].join("\n\n") }] },
] };
const REPORT = `waiting on ci: checks of PR ${PR} in ${RUN}`;
const TERMINAL = `> opened ${PR}\r\n`;

// The addresses the links open are answered here, never by the web.
const answerAddresses = (page: Page) => page.context().route(/^https:\/\/(github\.com|docs\.example\.org)\//, (route) => route.fulfill({ contentType: "text/plain", body: "fixture page" }));

async function openChat(page: Page) {
  await answerAddresses(page);
  const relay = await open(page, snapshot({}, { phase: "waiting", waiting_on: "ci", activity: REPORT }), undefined, CHAT, TERMINAL);
  await openGoblin(page);
  await pickStyle(page, "Modern chat");
  const talk = page.getByRole("log", { name: "Messages of " + NAME });
  await expect(talk.getByText("waits.", { exact: false })).toBeVisible();
  return { talk, relay };
}

async function picture(page: Page, testInfo: TestInfo, name: string) {
  await page.screenshot({ path: testInfo.outputPath(name + ".png") });
}

// openCeoTerminal opens the CEO's terminal in Herdr: its live screen draws
// screen, and the pane's history, which Shift+PageUp opens over it, holds
// history. The supervisor's terminal routes are played here.
async function openCeoTerminal(page: Page, screen: string, history: string) {
  await answerAddresses(page);
  await page.addInitScript((frame) => {
    const originalFetch = window.fetch;
    window.fetch = (resource, options) => {
      if (resource !== "/api/terminal/stream") return originalFetch(resource, options);
      const body = JSON.parse(String(options?.body));
      const encoder = new TextEncoder();
      return Promise.resolve(new Response(new ReadableStream({ start(controller) {
        controller.enqueue(encoder.encode(JSON.stringify({ type: "terminal.ready", lease: body.control ? "sized" : "observed", identity: "fixture" }) + "\n"));
        controller.enqueue(encoder.encode(JSON.stringify({ type: "terminal.frame", encoding: "ansi", seq: 1, full: true, width: 80, height: 24, bytes: btoa(frame) }) + "\n"));
        options?.signal?.addEventListener("abort", () => controller.close(), { once: true });
      } })));
    };
  }, "\x1b[2J\x1b[H" + screen);
  await page.route("**/api/terminal/input", (route) => route.fulfill({ json: { seq: route.request().postDataJSON().seq } }));
  await page.route("**/api/terminal/history", (route) => route.fulfill({ json: { text: history, agent: "codex" } }));
  await page.goto("/tests/fixtures/terminal-input.html?role=cfo#herdr");
  await expect(page.getByRole("region", { name: "CEO terminal" })).toBeVisible();
  await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);
}

for (const [device, width, height] of [["phone", 390, 844], ["desktop", 1440, 1000]] as const) {
  test.describe(`on a ${device}`, () => {
    test.use({ viewport: { width, height } });

    test("every address in a goblin's chat is a link that opens it in a new tab", async ({ page }, testInfo) => {
      // Arrange
      const { talk } = await openChat(page);

      // Act
      const links = talk.locator("a.message-link");

      // Assert
      for (const [name, address] of [["board PR #412", PR], ["board issue #2360", ISSUE], ["board CI run 99", RUN], ["Review page", REVIEW], [PAGE_ADDRESS, PAGE_ADDRESS], ["the fix", PR], [LONG, LONG]] as const) {
        const link = links.getByText(name, { exact: true });
        await expect(link).toHaveCount(1);
        await expect(link).toHaveAttribute("href", address);
        await expect(link).toHaveAttribute("target", "_blank");
        await expect(link).toHaveAttribute("rel", "noreferrer");
      }
      await expect(talk.locator("pre a.message-link")).toHaveText(PR);
      await expect(talk.getByText("board PR #412", { exact: true })).toHaveAttribute("title", PR);
      // A known address reads as a chip, and one written as a Markdown label as a plain link.
      await expect(talk.locator("a.message-chip")).toHaveText(["board PR #412", "board issue #2360", "board CI run 99", "Review page"]);
      await expect(talk.getByText("the fix", { exact: true })).not.toHaveClass(/message-chip/);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
      await talk.getByText(LONG, { exact: true }).scrollIntoViewIfNeeded();
      await picture(page, testInfo, `chat-links-${device}`);
    });

    test("only http and https become links, and markup in a message is shown as text", async ({ page }) => {
      // Arrange
      const { talk } = await openChat(page);

      // Act
      const reply = talk.locator('[data-message="m1"]');

      // Assert
      await expect(reply).toContainText('javascript:alert(1), file:///C:/x and <img src=x onerror="window.ran=1">.');
      await expect(page.locator('a[href^="javascript:"], a[href^="file:"]')).toHaveCount(0);
      await expect(reply.locator("img")).toHaveCount(0);
      expect(await page.evaluate(() => (window as { ran?: number }).ran)).toBeUndefined();
    });

    test("a link opens in a tab of its own, from a click or the keyboard, and the board stays where it was", async ({ page, context }) => {
      // Arrange
      const { talk } = await openChat(page);
      const board = page.url();
      const link = talk.getByRole("link", { name: "board PR #412" });

      // Act
      const [clicked] = await Promise.all([context.waitForEvent("page"), link.click()]);
      await link.focus();
      const [pressed] = await Promise.all([context.waitForEvent("page"), page.keyboard.press("Enter")]);

      // Assert
      for (const opened of [clicked, pressed]) {
        await opened.waitForLoadState();
        expect(opened.url()).toBe(PR);
        await opened.close();
      }
      expect(page.url()).toBe(board);
      await expect(link).toBeFocused();
    });

    test("the panel's report links the words it says each address by", async ({ page }, testInfo) => {
      // Arrange
      await openChat(page);
      const panel = page.locator(".context-pane");

      // Act
      await panel.getByRole("button", { name: "Task", exact: true }).click();
      const sentence = panel.locator(".panel-activity");
      await panel.locator(".raw-details > summary").click();

      // Assert
      await expect(sentence).toBeVisible();
      await expect(sentence).toHaveText("Checks of PR #412 in a CI run.");
      await expect(sentence.getByRole("link", { name: "PR #412" })).toHaveAttribute("href", PR);
      await expect(sentence.getByRole("link", { name: "a CI run" })).toHaveAttribute("href", RUN);
      const details = panel.locator(".raw-details-text");
      await expect(details.getByRole("link", { name: "board PR #412" })).toHaveAttribute("href", PR);
      await expect(details.getByRole("link", { name: "board CI run 99" })).toHaveAttribute("href", RUN);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
      await picture(page, testInfo, `panel-report-${device}`);
    });

    test("an address a goblin's terminal draws opens in a tab of its own", async ({ page, context }) => {
      // Arrange
      const { relay } = await openChat(page);
      await pickStyle(page, "Terminal");
      const screen = page.locator(".terminal-view:not(.staged) .xterm-screen");
      await expect(screen).toBeVisible();
      const size = relay.resizes.at(-1);
      expect(size, "the terminal reported its grid").toBeTruthy();
      const box = (await screen.boundingBox())!;
      const cell = { width: box.width / size!.cols, height: box.height / size!.rows };
      // The address starts after "> opened " on the first row.
      const point = { x: box.x + (9 + 5.5) * cell.width, y: box.y + 0.5 * cell.height };

      // Act
      await page.mouse.move(point.x - cell.width, point.y);
      await page.mouse.move(point.x, point.y);
      const [opened] = await Promise.all([context.waitForEvent("page"), page.mouse.click(point.x, point.y)]);

      // Assert
      await opened.waitForLoadState();
      expect(opened.url()).toBe(PR);
      await opened.close();
    });
  });
}

for (const [where, address] of [["live screen", PR], ["history", ISSUE]] as const) {
  test(`an address the CEO's terminal draws on its ${where} opens in a tab of its own`, async ({ page }) => {
    // Arrange
    await openCeoTerminal(page, `> opened ${PR}\r\n$ `, `> filed ${ISSUE}\n$`);
    if (where === "history") await page.getByRole("textbox", { name: "Terminal input", exact: true }).press("Shift+PageUp");
    const rows = page.locator(where === "history" ? ".terminal-history:not(.away) .xterm-rows" : ".terminal-surface:not(.terminal-history) .xterm-rows");
    await expect(rows).toContainText(address);

    // Act
    const opened = await openTerminalAddress(page, rows, address);

    // Assert
    expect(opened.url()).toBe(address);
  });
}

test("a paused goblin's Details links the pull request its last report names", async ({ page }) => {
  // Arrange
  const paused = { ...GOBLIN, phase: "paused", at, activity: "lifecycle-paused: overlord; task session and branch", last_report: `working: opened PR ${PR} for review`,
    lifecycle: { phase: "paused", action: "pause", at, kept: [], stopped: [], problems: [], handoff_saved: true, validation_kept: false, pause: { reason: "overlord", at } } };
  await holdStream(page, { healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [], tasks: [paused] });
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await page.locator(".task-card").filter({ hasText: "Zeke" }).first().click();
  const header = await openTaskList(page);

  // Act
  await header.locator(".raw-details > summary").click();

  // Assert
  const details = header.locator(".raw-details-text");
  await expect(details).toContainText("Opened PR #412 for review.");
  await expect(details.getByRole("link", { name: "PR #412", exact: true })).toHaveAttribute("href", PR);
  await expect(details.getByRole("link", { name: "PR #412", exact: true })).toHaveAttribute("target", "_blank");
});

test("an ended session's last report links the pull request it names", async ({ page }) => {
  // Arrange
  const query = new URLSearchParams({ phase: "paused", report: `working: opened PR ${PR} for review` });

  // Act
  await page.goto("/tests/fixtures/ended-session.html?" + query);

  // Assert
  const report = page.locator(".ended-session-report");
  await expect(report).toContainText("Opened PR #412 for review.");
  await expect(report.getByRole("link", { name: "PR #412", exact: true })).toHaveAttribute("href", PR);
  await expect(report.getByRole("link", { name: "PR #412", exact: true })).toHaveAttribute("target", "_blank");
});

test("a report with no address puts no link in the panel", async ({ page }) => {
  // Arrange
  await open(page, snapshot({}, { phase: "waiting", waiting_on: "ci", activity: "waiting on ci: the checks" }), undefined, CHAT, TERMINAL);

  // Act
  await openGoblin(page);
  await page.locator(".context-pane").getByRole("button", { name: "Task", exact: true }).click();

  // Assert
  await expect(page.locator(".context-pane .panel-activity")).toBeVisible();
  await expect(page.locator(".context-pane .panel-activity")).toHaveText("The checks.");
  await expect(page.locator(".context-pane .panel-activity a")).toHaveCount(0);
});

// The desktop window's own script (linksScript in cmd/goblins-window/links.go)
// runs in the board's page after each load. It runs here as the window runs
// it, read from its source, over a stand-in for WebView2's bridge that keeps
// what the page asks the window to open.
const LINKS_GO = readFileSync(new URL("../../cmd/goblins-window/links.go", import.meta.url), "utf8");
const OPEN_MESSAGE = /const openMessage = "([^"]+)"/.exec(LINKS_GO)![1];
const LINK_FAILED = /const linkFailedEvent = "([^"]+)"/.exec(LINKS_GO)![1];
const LINKS_START = LINKS_GO.indexOf("const linksScript = `") + "const linksScript = `".length;
const LINKS_SCRIPT = LINKS_GO.slice(LINKS_START, LINKS_GO.indexOf("})();`", LINKS_START) + "})();".length).replaceAll("` + openMessage + `", OPEN_MESSAGE);
const RUN_LINKS = `window.addEventListener("DOMContentLoaded", () => { ${LINKS_SCRIPT} });`;
const WINDOW = `const posted = []; Object.assign(window, { posted, chrome: { webview: { postMessage: (text) => posted.push(text) } } });
${RUN_LINKS}`;
// The same window, whose bridge hands each message to the test the moment it
// is posted, so one a page posts as it leaves is kept.
const HEARD_WINDOW = `Object.assign(window, { chrome: { webview: { postMessage: (text) => window.windowHeard(text) } } });
${RUN_LINKS}`;
const posted = (page: Page) => page.evaluate(() => (window as unknown as { posted: string[] }).posted);

test.describe("in the desktop window", () => {
  test.use({ viewport: { width: 1440, height: 1000 } });

  test("every way off the board opens in the default browser, and the board stays", async ({ page, context }) => {
    // Arrange
    await context.addInitScript({ content: WINDOW });
    const { talk } = await openChat(page);
    const board = page.url();
    await page.evaluate(() => document.body.append(Object.assign(document.createElement("a"), { href: "https://docs.example.org/same-tab", textContent: "A same-tab link" })));

    // Act: a link that opens a new tab, one that opens in the same tab, a
    // script that sets the page's location, and a move within the board.
    await talk.getByRole("link", { name: "board PR #412" }).click();
    await page.getByRole("link", { name: "A same-tab link" }).click();
    await page.evaluate(() => location.assign("https://docs.example.org/script"));
    await page.evaluate(() => { location.hash = "within-the-board"; });

    // Assert
    await expect.poll(() => posted(page)).toEqual([OPEN_MESSAGE + PR, OPEN_MESSAGE + "https://docs.example.org/same-tab", OPEN_MESSAGE + "https://docs.example.org/script"]);
    expect(page.url()).toBe(board + "#within-the-board");
    expect(context.pages()).toHaveLength(1);
    await expect(talk.getByRole("link", { name: "board PR #412" })).toBeVisible();
  });

  test("a navigation the page cannot cancel goes ahead and asks the window for nothing", async ({ page, context }) => {
    // Arrange: a page before the board in the tab's history, then the board
    // in the window.
    const heard: string[] = [];
    await context.exposeBinding("windowHeard", (_source, text: string) => { heard.push(text); });
    await answerAddresses(page);
    await page.goto(PAGE_ADDRESS);
    await context.addInitScript({ content: HEARD_WINDOW });
    await openChat(page);

    // Act: the navigate event the Navigation API fires, not cancelable, for a
    // traversal to another document, then going back off the board, which
    // Chromium starts without one.
    await page.evaluate((url) => {
      const { navigation } = window as unknown as { navigation: EventTarget };
      navigation.dispatchEvent(Object.assign(new Event("navigate", { cancelable: false }), { destination: { url } }));
    }, PAGE_ADDRESS);
    await page.goBack();

    // Assert
    await expect(page).toHaveURL(PAGE_ADDRESS);
    expect(heard).toEqual([]);
  });

  test("a link the window could not open is said in the board, with its address to copy and no link", async ({ page, context }, testInfo) => {
    // Arrange
    await context.addInitScript({ content: WINDOW });
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await openChat(page);

    // Act: the window tells the page, as linkFailed in links.go writes it.
    await page.evaluate(([event, address]) => window.dispatchEvent(new CustomEvent(event, { detail: address })), [LINK_FAILED, PAGE_ADDRESS]);

    // Assert
    const fallback = page.locator(".link-fallback");
    await expect(fallback).toContainText("That link could not be opened. Its address, to copy: " + PAGE_ADDRESS);
    await expect(fallback.locator("code")).toHaveText(PAGE_ADDRESS);
    await expect(fallback.getByRole("link")).toHaveCount(0);
    await expect(fallback).toHaveAttribute("role", "status");
    await picture(page, testInfo, "link-fallback-desktop");

    // Act
    await fallback.getByRole("button", { name: "Copy " + PAGE_ADDRESS }).click();

    // Assert
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(PAGE_ADDRESS);

    // Act
    await fallback.getByRole("button", { name: "Close" }).click();

    // Assert
    await expect(fallback).toHaveCount(0);
  });
});
