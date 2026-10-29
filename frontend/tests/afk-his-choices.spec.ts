import { expect, openItem, test, type Page } from "./site";

// The Overlord, 2026-10-28, with a picture of a card in Modern Chat: "afk
// commands dont have proper multiple choice answers like normal command cfo
// prompts please fix so its options arent always done / later". A step only
// he can take that the CFO logged with choices, under AFK mode, is the CFO's
// own question once AFK mode turns off, with those choices and its
// recommendation: its card lists them, recommended first, and Other, in the
// Command Center over the board and over the CFO's Modern Chat alike. The
// supervisor is played here, and the card stands in for the Georgetown one.
const DECISION = "Approve Odie's one-time production repair of PrecisionDocs project Georgetown";
const CHOICES = ["Approve the repair", "Dry-run it again first", "Leave Georgetown as it is"];
const RECOMMENDED = CHOICES[1];
const CARD = {
  id: "afk-his-20261028T100000.000000000Z", identity: "c".repeat(64), task: "", status: "pending", created_at: "2026-10-28T10:00:00Z",
  text: DECISION + "\n- Link: `https://github.com/acme/app/issues/2012`", options: CHOICES, recommended: RECOMMENDED,
};
const SNAPSHOT = {
  healthy: true, instance: "fixture", revision: 1, cfo_runs: true, cfo_terminal: "cfo-term", cfo_harness: "claude", afk: { state: "off" }, attention: [],
  tasks: [], questions: [CARD], reviews: [], runs: [], credentials: [], actions: [],
};

// open loads the board with SNAPSHOT as its one snapshot and an empty
// conversation for every session the board reads as messages.
async function open(page: Page) {
  await page.addInitScript((snapshot) => {
    localStorage.setItem("cfo-first-open", "shown");
    localStorage.setItem("cfo-notifications-asked", "true");
    class Stream extends EventTarget {
      onerror: (() => void) | null = null;
      readyState = 1;
      constructor(address: string | URL) {
        super();
        const isMessages = new URL(address, location.href).pathname === "/api/messages";
        const [type, data] = isMessages ? ["page", JSON.stringify({ messages: [], more: false })] : ["snapshot", snapshot];
        setTimeout(() => this.dispatchEvent(new MessageEvent(type, { data })));
      }
      close() { this.readyState = 2; }
    }
    Object.defineProperty(window, "EventSource", { value: Stream });
  }, JSON.stringify(SNAPSHOT));
  await page.route("**/api/**", async (route) => {
    if (new URL(route.request().url()).pathname === "/api/announce") await route.fulfill({ json: { claimed: [] } });
    else await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
  });
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => socket.send(JSON.stringify({ type: "history", bytes: 0 })));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
}

// expectHisChoices fails unless the open card lists the step's choices,
// recommended first and marked, then Other, and neither Done nor Later.
async function expectHisChoices(page: Page) {
  const card = page.locator("dialog.question-modal .question-card");
  await expect(card.locator(".question-body")).toContainText(DECISION);
  await expect(card.locator(".question-choice .question-option > span:first-child")).toHaveText([RECOMMENDED, CHOICES[0], CHOICES[2], "Other"]);
  await expect(card.locator(".question-choice").filter({ has: page.locator(".recommendation") })).toHaveText(RECOMMENDED + "Recommended");
  await expect(card.getByRole("radio")).toHaveCount(CHOICES.length + 1);
}

test("a his step logged with choices offers them and Other in the Command Center", async ({ page }) => {
  // Arrange
  await open(page);

  // Act
  await openItem(page, DECISION);

  // Assert
  await expectHisChoices(page);
});

test("the same card offers the same choices over the CFO's Modern Chat", async ({ page }) => {
  // Arrange
  await open(page);
  await page.keyboard.press("Control+Alt+1");
  await expect(page.locator("#panel-title")).toHaveText("CFO");
  await expect(page.getByRole("log", { name: "Messages of CFO" })).toBeVisible();

  // Act
  await openItem(page, DECISION);

  // Assert
  await expectHisChoices(page);
});
