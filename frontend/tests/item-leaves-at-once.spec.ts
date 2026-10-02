import { expect, test, type BrowserContext, type Page } from "./site";

// The Overlord, 2026-10-02: "when I close a Command Center question or the CFO
// answers it, the alert hangs around for a stale second or two in the goblin
// board even after answered ... or reappears a second later as the same
// command, it's not idempotent." Measured on main-9f22183c: the live board
// took 3 to 13 seconds to build a snapshot, and the alert, the count and the
// CFO's bar kept asking for that long after the card had closed with its
// check. So these tests hold every snapshot back: what he acted on leaves in
// the frame he acts, on every open board, and no late snapshot brings it back.
test.use({ timezoneId: "UTC", viewport: { width: 1440, height: 900 } });

const ASKS = "May I merge the release train now?";
const WAITS = "Add the DNS record at your registrar";
const question = { id: "release-train-20261002", identity: "c".repeat(64), task: "", status: "pending", created_at: "2026-10-02T11:00:00Z", text: ASKS, options: ["Merge it", "Hold it"], recommended: "Merge it" };
const wait = { id: "waiting-cg-probe-7", identity: "d".repeat(64), task: "cg-probe", title: "Waiting on you: " + WAITS, state: "open", created_at: "2026-10-02T11:00:00Z", updated_at: "2026-10-02T11:00:00Z" };
const proof = { id: "proof-shots-20261002", identity: "c".repeat(64), task: "", title: "Do the proof screenshots read well?", state: "open", created_at: "2026-10-02T11:00:00Z", updated_at: "2026-10-02T11:00:00Z" };
const quiet = {
  healthy: true, instance: "board-0m", cfo_runs: true, cfo_terminal: "cfo", cfo_harness: "claude", revision: 1,
  tasks: [{ id: "cg-probe", title: "Probe the DNS", project: "code-goblins", phase: "working", generation: "g1", verified: false }],
  questions: [] as object[], reviews: [] as object[], runs: [], credentials: [], actions: [],
};
type Board = typeof quiet;
const asking = (item: { questions?: object[]; reviews?: object[] }, revision = 2): Board => ({ ...quiet, revision, questions: item.questions || [], reviews: item.reviews || [] });

// Every board the test opens gets its snapshots and its items events from the
// test, through a stand-in event stream, one event at a time: nothing arrives
// that the test did not send.
async function standInStream(context: BrowserContext) {
  await context.addInitScript(() => {
    class FixtureSource extends EventTarget {
      private publish = (event: Event) => {
        if (event instanceof CustomEvent) this.dispatchEvent(new MessageEvent(event.detail.type, { data: JSON.stringify(event.detail.data) }));
      };
      constructor() {
        super();
        window.addEventListener("fixture-stream", this.publish);
      }
      close() { window.removeEventListener("fixture-stream", this.publish); }
    }
    Object.defineProperty(window, "EventSource", { value: FixtureSource });
  });
  await context.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
}
const send = (page: Page, type: "snapshot" | "items", data: object) => page.evaluate((detail) => { window.dispatchEvent(new CustomEvent("fixture-stream", { detail })); }, { type, data });

// The supervisor's record of what the board announced: each key goes to the
// first board that asks for it.
async function announcer(context: BrowserContext) {
  const announced = new Set<string>();
  await context.route("**/api/announce", async (route) => {
    const asked: { keys?: string[]; news?: string[] } = route.request().postDataJSON();
    const claimed = [...asked.keys || [], ...asked.news || []].filter((key) => !announced.has(key));
    for (const key of claimed) announced.add(key);
    await route.fulfill({ json: { claimed } });
  });
}

// The supervisor taking what he sends: every action waits until the test lets
// it through or refuses it, so a test can look at the board while the request
// is still on its way.
async function actions(context: BrowserContext) {
  const posted: Record<string, unknown>[] = [];
  let release: (refusal?: string) => void = () => {};
  const held = new Promise<string | undefined>((resolve) => { release = resolve; });
  await context.route("**/api/actions", async (route) => {
    const body: Record<string, unknown> = route.request().postDataJSON();
    posted.push(body);
    const refusal = await held;
    if (refusal) await route.fulfill({ status: 409, json: { error: refusal } });
    else await route.fulfill({ status: 202, json: { ...body, status: "queued" } });
  });
  return { posted, accept: () => release(), refuse: (why: string) => release(why) };
}

const toasts = (page: Page) => page.locator(".toasts .toast");
const badge = (page: Page) => page.locator(".command-center-menu > summary");
const bar = (page: Page) => page.locator(".cfo-pin");
const card = (page: Page) => page.locator("dialog.question-modal");

// A board that was quiet, then is asked: the item's alert, its count and the
// CFO's bar are all on screen.
async function boardAsked(page: Page, item: { questions?: object[]; reviews?: object[] }, says: string) {
  await page.goto("/");
  await send(page, "snapshot", quiet);
  await expect(bar(page)).toContainText("All quiet");
  await send(page, "snapshot", asking(item));
  await expect(bar(page)).toContainText(says);
  await expect(toasts(page)).toContainText(says);
  await expect(badge(page)).toHaveAccessibleName("Command Center, 1 waiting on you");
}
async function openCard(page: Page, says: string) {
  if (!await card(page).isVisible()) await bar(page).getByRole("button", { name: "Open Command Center" }).click();
  await expect(card(page)).toContainText(says);
}
// What the board shows of the item in the very frame of a click on selector:
// read before anything else can reach the page.
const clickAndLook = (page: Page, selector: string) => page.evaluate((selector) => {
  document.querySelector<HTMLElement>(selector)!.click();
  return { toasts: document.querySelectorAll(".toasts .toast").length, badge: document.querySelector(".command-center-menu > summary")!.getAttribute("aria-label"), bar: document.querySelector(".cfo-pin")!.textContent };
}, selector);
async function expectGone(page: Page, says: string) {
  await expect(toasts(page)).toHaveCount(0);
  await expect(badge(page)).toHaveAccessibleName("Command Center");
  await expect(bar(page)).not.toContainText(says);
  await expect(bar(page)).toContainText("All quiet");
  await expect(page).toHaveTitle("Code Goblins");
}

const WAYS = [
  { name: "a question he answers", item: { questions: [question] }, says: ASKS, pick: "Merge it", press: "dialog.question-modal .send-decision", kind: "cfo_answer", heading: "Sent" },
  { name: "a question he dismisses", item: { questions: [question] }, says: ASKS, pick: "", press: 'dialog.question-modal [aria-label="Dismiss this question"]', kind: "question_clear", heading: "Dismissed" },
  { name: "a goblin's wait he dismisses", item: { reviews: [wait] }, says: WAITS, pick: "", press: "dialog.question-modal .status-dismiss", kind: "review_clear", heading: "Cleared" },
  { name: "a review he clears", item: { reviews: [proof] }, says: "Do the proof screenshots read well?", pick: "", press: 'dialog.question-modal [aria-label="Clear this item without answering"]', kind: "review_clear", heading: "Cleared" },
];

for (const way of WAYS) {
  test(`${way.name} leaves its alert, the count and the CFO's bar in the frame he acts, before the board hears back`, async ({ page, context }) => {
    // Arrange
    await standInStream(context);
    await announcer(context);
    const supervisor = await actions(context);
    await boardAsked(page, way.item, way.says);
    await openCard(page, way.says);
    if (way.pick) await card(page).getByText(way.pick, { exact: true }).click();

    // Act: no snapshot follows, and the request is still on its way.
    const shown = await clickAndLook(page, way.press);

    // Assert
    expect(shown).toEqual({ toasts: 0, badge: "Command Center", bar: expect.stringContaining("All quiet") });
    await expect.poll(() => supervisor.posted.map((body) => body.kind)).toEqual([way.kind]);
    await expect(card(page).locator(".done-card").getByRole("heading", { name: way.heading })).toBeVisible();
    await expectGone(page, way.says);
  });
}

test("a snapshot taken before his answer and arriving after it does not bring the item back", async ({ page, context }) => {
  // Arrange
  await standInStream(context);
  await announcer(context);
  const supervisor = await actions(context);
  await boardAsked(page, { questions: [question] }, ASKS);
  await openCard(page, ASKS);
  await card(page).getByText("Merge it", { exact: true }).click();
  await clickAndLook(page, "dialog.question-modal .send-decision");
  supervisor.accept();
  await expect(card(page)).toBeHidden();

  // Act: the supervisor built this one before it took the answer.
  await send(page, "snapshot", asking({ questions: [question] }, 3));
  await send(page, "snapshot", { ...asking({ questions: [question] }, 4), tasks: [{ ...quiet.tasks[0], title: "Probe the DNS again" }] });

  // Assert: the board took both snapshots in, and the question stayed gone.
  await expect(page.getByText("Probe the DNS again")).toBeVisible();
  await expectGone(page, ASKS);
  await expect(card(page)).toBeHidden();
});

test("a send the board refuses puts the item back, with why", async ({ page, context }) => {
  // Arrange
  await standInStream(context);
  await announcer(context);
  const supervisor = await actions(context);
  await boardAsked(page, { questions: [question] }, ASKS);
  await openCard(page, ASKS);
  await card(page).getByText("Merge it", { exact: true }).click();
  await clickAndLook(page, "dialog.question-modal .send-decision");
  await expectGone(page, ASKS);

  // Act
  supervisor.refuse("the primary CFO changed; refresh before sending");

  // Assert
  await expect(badge(page)).toHaveAccessibleName("Command Center, 1 waiting on you");
  await expect(bar(page)).toContainText(ASKS);
  await expect(card(page).getByRole("alert")).toContainText("the primary CFO changed; refresh before sending");
});

test("the CFO's answer reaches the board ahead of the full snapshot, and a snapshot built before it does not undo it", async ({ page, context }) => {
  // Arrange: the alert is on screen and the Command Center is closed.
  await standInStream(context);
  await announcer(context);
  await boardAsked(page, { questions: [question] }, ASKS);
  await page.keyboard.press("Escape");
  await expect(card(page)).toBeHidden();
  const answered = { ...question, status: "succeeded", answer: "Merge it", answer_kind: "option", answered_option: "Merge it", answered_by: "cfo", answered_at: "2026-10-02T11:05:00Z", message: "Answered by the CFO." };

  // Act: only the Command Center's items arrive.
  await send(page, "items", { instance: quiet.instance, revision: 3, questions: [answered], reviews: [], runs: [], credentials: [], actions: [] });

  // Assert
  await expectGone(page, ASKS);

  // Act: a full snapshot built before the answer follows.
  await send(page, "snapshot", { ...asking({ questions: [question] }, 3), tasks: [{ ...quiet.tasks[0], title: "Probe the DNS again" }] });

  // Assert
  await expect(page.getByText("Probe the DNS again")).toBeVisible();
  await expectGone(page, ASKS);
  await expect(card(page)).toBeHidden();
});

test("a second board drops the item he answered on the first before any snapshot, and its open card shows the check and leaves", async ({ page, context }) => {
  // Arrange: two boards, the question's card open on both.
  await standInStream(context);
  await announcer(context);
  await actions(context);
  const other = await context.newPage();
  await boardAsked(page, { questions: [question] }, ASKS);
  await other.goto("/");
  await send(other, "snapshot", quiet);
  await send(other, "snapshot", asking({ questions: [question] }));
  await expect(bar(other)).toContainText(ASKS);
  await openCard(other, ASKS);
  await openCard(page, ASKS);
  await card(page).getByText("Merge it", { exact: true }).click();

  // Act: he answers on the first board; no snapshot reaches either.
  await clickAndLook(page, "dialog.question-modal .send-decision");

  // Assert
  await expect(card(other).locator(".done-card").getByRole("heading", { name: "Answered" })).toBeVisible();
  await expectGone(other, ASKS);
  await expect(card(other)).toBeHidden();
});

test("a card open for an item that closed somewhere else shows what became of it and leaves", async ({ page, context }) => {
  // Arrange
  await standInStream(context);
  await announcer(context);
  await boardAsked(page, { reviews: [wait] }, WAITS);
  await openCard(page, WAITS);

  // Act: the goblin reported again, which takes its wait back.
  await send(page, "snapshot", asking({ reviews: [{ ...wait, state: "withdrawn", reason: "The goblin moved on.", updated_at: "2026-10-02T11:06:00Z" }] }, 3));

  // Assert
  await expect(card(page).locator(".done-card").getByRole("heading", { name: "Closed" })).toBeVisible();
  await expect(card(page).locator(".done-card")).toContainText("Withdrawn: The goblin moved on.");
  await expect(card(page)).toBeHidden();
  await expectGone(page, WAITS);
});
