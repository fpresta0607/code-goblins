import { doneCards, expect, openItem, recordDoneCards, test, type BrowserContext, type Page } from "./site";

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

// Every board the test opens gets its snapshots and their changes from the
// test, through a stand-in event stream, one event at a time: nothing arrives
// that the test did not send.
async function standInStream(context: BrowserContext) {
  await context.addInitScript(() => {
    localStorage.setItem("cfo-notifications-asked", "true");
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
const send = (page: Page, type: "snapshot" | "change", data: object) => page.evaluate((detail) => { window.dispatchEvent(new CustomEvent("fixture-stream", { detail })); }, { type, data });

// The supervisor's record of what the board announced: each key goes to the
// first board that asks for it.
async function announcer(context: BrowserContext) {
  const announced = new Set<string>();
  await context.route("**/api/announce", async (route) => {
    const asked: { keys: string[] } = route.request().postDataJSON();
    const claimed = asked.keys.filter((key) => !announced.has(key));
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

// A board that was quiet, then is asked: the item's count and the CFO's bar
// are on screen, and that is its one signal: an item shows no toast.
async function boardAsked(page: Page, item: { questions?: object[]; reviews?: object[] }, says: string) {
  await page.goto("/");
  await send(page, "snapshot", quiet);
  await expect(bar(page)).toContainText("All quiet");
  await send(page, "snapshot", asking(item));
  await expect(bar(page).getByRole("button", { name: "Open Command Center: 1 waiting on you" })).toBeVisible();
  await expect(bar(page)).not.toContainText(says);
  await expect(badge(page)).toHaveAccessibleName("Command Center, 1 waiting on you");
  await expect(toasts(page)).toHaveCount(0);
}
// row is the item's words in the Command Center's list, when they differ
// from its card's, as a credential request's ask does.
async function openCard(page: Page, says: string, row = says) {
  if (!await card(page).isVisible()) await openItem(page, row);
  await expect(card(page)).toContainText(says);
}
// What the board shows of the item in the very frame of a click on selector.
// React draws what a click changed in the microtasks that follow it, so the
// look comes after those and before anything else can reach the page, and
// painted says whether the browser drew a frame in between: it must not have.
const clickAndLook = (page: Page, selector: string) => page.evaluate(async (selector) => {
  let painted = false;
  requestAnimationFrame(() => { painted = true; });
  document.querySelector<HTMLElement>(selector)!.click();
  for (let turn = 0; turn < 5; turn++) await Promise.resolve();
  return { painted, toasts: document.querySelectorAll(".toasts .toast").length, badge: document.querySelector(".command-center-menu > summary")!.getAttribute("aria-label"), bar: document.querySelector(".cfo-pin")!.textContent, command: document.querySelector(".cfo-pin .cfo-command")?.getAttribute("aria-label") || "", card: document.querySelector("dialog.question-modal .done-card h3")?.textContent || "" };
}, selector);
async function expectGone(page: Page, says: string) {
  await expect(toasts(page)).toHaveCount(0);
  await expect(badge(page)).toHaveAccessibleName("Command Center");
  await expect(bar(page)).not.toContainText(says);
  await expect(bar(page)).toContainText("All quiet");
  await expect(bar(page).getByRole("button", { name: /^Open Command Center/ })).toHaveCount(0);
  await expect(page).toHaveTitle("Code Goblins");
}

const WAYS = [
  { name: "a question he answers", item: { questions: [question] }, says: ASKS, pick: "Merge it", open: "", press: "dialog.question-modal .send-decision", kind: "cfo_answer", heading: "Sending" },
  { name: "a question he dismisses", item: { questions: [question] }, says: ASKS, pick: "", open: "", press: 'dialog.question-modal [aria-label="Skip this card"]', kind: "question_clear", heading: "Dismissed" },
  { name: "a goblin's wait he dismisses", item: { reviews: [wait] }, says: WAITS, pick: "", open: "", press: 'dialog.question-modal [aria-label="Skip this card"]', kind: "review_clear", heading: "Cleared" },
  { name: "a review he clears", item: { reviews: [proof] }, says: "Do the proof screenshots read well?", pick: "", open: "", press: 'dialog.question-modal [aria-label="Skip this card"]', kind: "review_clear", heading: "Cleared" },
];

test("an answer waiting for the Codex composer reads Queued with its reason", async ({ page, context }) => {
  // Arrange
  await standInStream(context);
  await announcer(context);
  const reason = "The CFO is typing. Your answer will wait.";
  await context.route("**/api/actions", (route) => route.fulfill({ status: 202, json: { ...route.request().postDataJSON(), status: "queued", message: reason } }));
  await boardAsked(page, { questions: [question] }, ASKS);
  await send(page, "snapshot", { ...asking({ questions: [question] }), cfo_harness: "codex", revision: 3 });
  await openCard(page, ASKS);
  await card(page).getByText("Merge it", { exact: true }).click();
  await recordDoneCards(page);

  // Act
  await card(page).locator(".send-decision").click();

  // Assert
  await expect.poll(() => doneCards(page)).toContainEqual(expect.stringMatching(/^Queued/));
  const shown = await doneCards(page);
  for (const queued of shown.filter((text) => text.startsWith("Queued"))) expect(queued).toContain(reason);
  expect(shown.filter((text) => text.includes("CFO received"))).toEqual([]);
});

for (const way of WAYS) {
  test(`${way.name} leaves the count and the CFO's bar in the frame he acts, before the board hears back`, async ({ page, context }) => {
    // Arrange
    await standInStream(context);
    await announcer(context);
    const supervisor = await actions(context);
    await boardAsked(page, way.item, way.says);
    await openCard(page, way.says);
    if (way.pick) await card(page).getByText(way.pick, { exact: true }).click();
    if (way.open) await page.locator(way.open).click();

    // Act: no snapshot follows, and the request is still on its way.
    const shown = await clickAndLook(page, way.press);

    // Assert
    expect(shown).toEqual({ painted: false, toasts: 0, badge: "Command Center", bar: expect.stringContaining("All quiet"), command: "", card: way.heading });
    await expect.poll(() => supervisor.posted.map((body) => body.kind)).toEqual([way.kind]);
    await expectGone(page, way.says);
  });
}

// The Overlord works in the desktop window: the board in WebView2, maximized
// on his 2560 by 1600 screen at 150 percent, which is 1707 CSS pixels wide at
// device scale 1.5, with the CFO's panel open beside the board.
test.describe("in the desktop window's size, with the CFO's panel open", () => {
  test.use({ viewport: { width: 1707, height: 1000 }, deviceScaleFactor: 1.5 });

  test("a goblin's question stays with the CFO, his own question waits quietly, and goblins' questions the CFO left ten minutes wait on him too, said on the CFO's bar, alerting nothing", async ({ page, context }, testInfo) => {
    // Arrange
    await standInStream(context);
    await announcer(context);
    await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
      socket.send(JSON.stringify({ type: "history", bytes: 0 }));
      socket.send(Buffer.from("READY\r\n"));
    });
    await page.goto("/");
    await send(page, "snapshot", quiet);
    await bar(page).getByRole("button", { name: "Open the CFO's terminal" }).first().click();
    await expect(page.locator("#panel-title")).toHaveText("CFO");

    // Act: the goblin asks the CFO, then the CFO asks the Overlord.
    await send(page, "snapshot", asking({ questions: [{ ...question, task: "cg-probe" }] }));
    await expect(badge(page)).toHaveAccessibleName("Command Center");
    await expect(toasts(page)).toHaveCount(0);
    await expect(card(page)).toBeHidden();
    await send(page, "snapshot", asking({ questions: [question] }, 3));

    // Assert
    await expect(badge(page)).toHaveAccessibleName("Command Center, 1 waiting on you");
    await expect(toasts(page)).toHaveCount(0);
    await expect(card(page)).toBeHidden();
    await testInfo.attach("one quiet item with the CFO's terminal open", { body: await page.screenshot(), contentType: "image/png" });
    await page.getByRole("button", { name: "Close panel", exact: true }).click();
    const left = [660, 630, 600].map((age, i) => ({ ...question, id: "left-" + i, task: "cg-probe", created_at: new Date(Date.parse("2026-10-02T12:21:00Z") - age * 1000).toISOString() }));
    await send(page, "snapshot", { ...quiet, revision: 4, at: "2026-10-02T12:21:00Z", questions: left });
    await expect(bar(page)).toContainText("3 questions wait for the CFO (11 min). 1 goblin at work.");
    await expect(badge(page)).toHaveAccessibleName("Command Center, 3 waiting on you");
    await page.waitForTimeout(500);
    await expect(toasts(page)).toHaveCount(0);
    await bar(page).getByRole("button", { name: "Open the CFO's terminal" }).first().click();
    await expect(page.locator("#panel-title")).toHaveText("CFO");
    // The CFO's panel opens on its Messages view, with the keyboard in its box.
    await expect(page.getByRole("textbox", { name: "Message CFO" })).toBeFocused();
  });

  test("a question he answers leaves the count and the CFO's bar in the frame he acts", async ({ page, context }, testInfo) => {
    // Arrange
    await standInStream(context);
    await announcer(context);
    const supervisor = await actions(context);
    await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
      socket.send(JSON.stringify({ type: "history", bytes: 0 }));
      socket.send(Buffer.from("READY\r\n"));
    });
    await page.goto("/");
    await send(page, "snapshot", quiet);
    await bar(page).getByRole("button", { name: "Open the CFO's terminal" }).first().click();
    await expect(page.locator("#panel-title")).toHaveText("CFO");
    await expect(page.locator(".canvas-region")).toBeVisible();
    await send(page, "snapshot", asking({ questions: [question] }));
    await expect(bar(page).getByRole("button", { name: "Open Command Center: 1 waiting on you" })).toBeVisible();
    await expect(badge(page)).toHaveAccessibleName("Command Center, 1 waiting on you");
    await expect(toasts(page)).toHaveCount(0);
    await openCard(page, ASKS);
    await card(page).getByText("Merge it", { exact: true }).click();
    await testInfo.attach("the question, before he answers", { body: await page.screenshot(), contentType: "image/png" });

    // Act
    const shown = await clickAndLook(page, "dialog.question-modal .send-decision");

    // Assert
    expect(shown).toEqual({ painted: false, toasts: 0, badge: "Command Center", bar: expect.stringContaining("All quiet"), command: "", card: "Sending" });
    await expect.poll(() => supervisor.posted.map((body) => body.kind)).toEqual(["cfo_answer"]);
    await testInfo.attach("the frame after his answer", { body: await page.screenshot(), contentType: "image/png" });
    await expectGone(page, ASKS);
  });
});

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

test("an ID published again after its item closed shows its count and its CFO bar button", async ({ page, context }) => {
  // Arrange: he answers the question and the supervisor's snapshot shows it closed.
  await standInStream(context);
  await announcer(context);
  const supervisor = await actions(context);
  await boardAsked(page, { questions: [question] }, ASKS);
  await openCard(page, ASKS);
  await card(page).getByText("Merge it", { exact: true }).click();
  await clickAndLook(page, "dialog.question-modal .send-decision");
  supervisor.accept();
  await expect(card(page)).toBeHidden();
  await send(page, "snapshot", asking({ questions: [{ ...question, status: "succeeded", answer: "Merge it", answer_kind: "option", answered_option: "Merge it", answered_by: "overlord", answered_at: "2026-10-02T11:05:00Z" }] }, 3));
  await expectGone(page, ASKS);
  const again = "May I merge the next release train?";

  // Act: the supervisor dropped that record, and an agent asks under the same ID.
  await send(page, "snapshot", asking({ questions: [{ ...question, created_at: "2026-10-12T09:00:00Z", text: again }] }, 4));

  // Assert
  await expect(badge(page)).toHaveAccessibleName("Command Center, 1 waiting on you");
  await expect(bar(page).getByRole("button", { name: "Open Command Center: 1 waiting on you" })).toBeVisible();
  await expect(bar(page)).not.toContainText(again);
  await openCard(page, again);
});

for (const kind of ["question", "review"] as const) {
  test(`a reused ${kind} clears its draft, while CFO registration keeps it`, async ({ page, context }) => {
    // Arrange
    await standInStream(context);
    await announcer(context);
    const original = kind === "question" ? question : proof;
    const publish = (changes: object, revision: number) => asking(kind === "question" ? { questions: [{ ...original, ...changes }] } : { reviews: [{ ...original, ...changes }] }, revision);
    await boardAsked(page, kind === "question" ? { questions: [question] } : { reviews: [proof] }, kind === "question" ? ASKS : proof.title);
    await openCard(page, kind === "question" ? ASKS : proof.title);
    if (kind === "question") await card(page).getByText("Other", { exact: true }).click();
    await card(page).getByRole("textbox").fill("Keep this unfinished answer");

    // Act: a new CFO adopts the same publication.
    await send(page, "snapshot", publish({ identity: "e".repeat(64) }, 3));

    // Assert
    await expect(card(page).getByRole("textbox")).toHaveValue("Keep this unfinished answer");

    // Act: the record goes away and its ID is published again.
    await send(page, "snapshot", { ...quiet, revision: 4 });
    await expect(card(page)).toBeHidden();
    const again = "Please review the next release";
    await send(page, "snapshot", publish({ created_at: "2026-10-12T09:00:00Z", text: again, title: again }, 5));
    await openCard(page, again);

    // Assert
    if (kind === "question") {
      await expect(card(page).locator("input[type=radio]:checked")).toHaveCount(0);
      await card(page).getByText("Other", { exact: true }).click();
    }
    await expect(card(page).getByRole("textbox")).toHaveValue("");
    await expect(card(page).getByRole("button", { name: kind === "question" ? "Send decision" : "Send answer" })).toBeDisabled();
  });
}

test("a reused run does not keep the previous publication's pending send", async ({ page, context }) => {
  // Arrange
  await standInStream(context);
  await announcer(context);
  const supervisor = await actions(context);
  const run = { id: "release-check", identity: "c".repeat(64), state: "ready", title: "Check the first release", shell: "powershell", command: "Write-Output ready", created_at: question.created_at };
  await page.goto("/");
  await send(page, "snapshot", { ...quiet, revision: 2, runs: [run] });
  await openCard(page, run.title);
  await card(page).getByRole("button", { name: "Run in PowerShell" }).click();
  await expect.poll(() => supervisor.posted.length).toBe(1);
  await expect(card(page).getByRole("button", { name: "Run in PowerShell" })).toBeDisabled();

  // Act
  const again = { ...run, created_at: "2026-10-12T09:00:00Z", title: "Check the next release" };
  await send(page, "snapshot", { ...quiet, revision: 3, runs: [again] });

  // Assert
  await expect(card(page)).toContainText(again.title);
  await expect(card(page).getByRole("button", { name: "Run in PowerShell" })).toBeEnabled();
  supervisor.accept();
});

test("a reused credential request clears its typed value, while CFO registration keeps it", async ({ page, context }) => {
  // Arrange
  await standInStream(context);
  await announcer(context);
  const request = { id: "cred-release", generation: "f".repeat(32), identity: "c".repeat(64), by: "cfo", project: "probe", repository: "C:/dev/probe", names: ["PROBE_VALUE"], why: "Configure the first release", state: "open", created_at: question.created_at, expires_at: "2026-10-13T09:00:00Z" };
  await page.goto("/");
  await send(page, "snapshot", { ...quiet, revision: 2, credentials: [request] });
  await openCard(page, request.why, "Paste PROBE_VALUE for probe");
  const field = card(page).getByLabel("Value for PROBE_VALUE");
  const value = crypto.randomUUID();
  await field.fill(value);
  await expect(field).toHaveValue("\u2022".repeat(value.length));

  // Act: adoption keeps the same publication.
  await send(page, "snapshot", { ...quiet, revision: 3, credentials: [{ ...request, identity: "e".repeat(64) }] });

  // Assert
  await expect(field).toHaveValue("\u2022".repeat(value.length));

  // Act
  const again = { ...request, created_at: "2026-10-12T09:00:00Z", why: "Configure the next release" };
  await send(page, "snapshot", { ...quiet, revision: 4, credentials: [again] });

  // Assert
  await expect(card(page)).toContainText(again.why);
  await expect(field).toHaveValue("");
  await expect(card(page).getByRole("button", { name: "Store PROBE_VALUE" })).toHaveCount(0);
});

test("a late refusal for an old publication does not mark the reused question's draft failed", async ({ page, context }) => {
  // Arrange
  await standInStream(context);
  await announcer(context);
  const supervisor = await actions(context);
  await boardAsked(page, { questions: [question] }, ASKS);
  await openCard(page, ASKS);
  await card(page).getByText("Merge it", { exact: true }).click();
  await card(page).getByRole("button", { name: "Send decision" }).click();
  await expect.poll(() => supervisor.posted.length).toBe(1);
  await card(page).getByRole("button", { name: "Close the Command Center" }).click();
  const again = "May I merge the next release train?";
  await send(page, "snapshot", asking({ questions: [{ ...question, created_at: "2026-10-12T09:00:00Z", text: again }] }, 3));
  await openCard(page, again);
  await card(page).getByText("Other", { exact: true }).click();
  await card(page).getByRole("textbox").fill("This is the new answer");

  // Act
  const refused = page.waitForResponse((response) => response.url().endsWith("/api/actions") && response.status() === 409);
  supervisor.refuse("the old question has closed");
  await refused;

  // Assert
  await expect(card(page).getByRole("textbox")).toHaveValue("This is the new answer");
  await expect(card(page).getByRole("alert")).toHaveCount(0);
  await expect(card(page).getByRole("button", { name: "Send decision" })).toBeEnabled();
});

test("a send the board refuses puts the item back, with a few words of why", async ({ page, context }) => {
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
  await expect(bar(page).getByRole("button", { name: "Open Command Center: 1 waiting on you" })).toBeVisible();
  await expect(card(page).locator(".notice")).toHaveText("The primary CFO changed. Refresh before sending.");
  await expect(card(page).getByRole("alert")).toHaveCount(0);
});

test("the CFO's answer reaches the board ahead of the full snapshot, and a snapshot built before it does not undo it", async ({ page, context }) => {
  // Arrange: the item waits on him and the Command Center is closed.
  await standInStream(context);
  await announcer(context);
  await boardAsked(page, { questions: [question] }, ASKS);
  await page.keyboard.press("Escape");
  await expect(card(page)).toBeHidden();
  const answered = { ...question, status: "succeeded", answer: "Merge it", answer_kind: "option", answered_option: "Merge it", answered_by: "cfo", answered_at: "2026-10-02T11:05:00Z", message: "Answered by the CFO." };

  // Act: only the Command Center's items arrive.
  await send(page, "change", { keys: { revision: { value: 3 }, questions: { items: { [question.id]: { value: answered } } } } });

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
  await expect(bar(other).getByRole("button", { name: "Open Command Center: 1 waiting on you" })).toBeVisible();
  await openCard(other, ASKS);
  await recordDoneCards(other);
  await openCard(page, ASKS);
  await card(page).getByText("Merge it", { exact: true }).click();

  // Act: he answers on the first board; no snapshot reaches either.
  await clickAndLook(page, "dialog.question-modal .send-decision");

  // Assert
  await expect.poll(() => doneCards(other)).toContainEqual(expect.stringMatching(/^Answered/));
  await expectGone(other, ASKS);
  await expect(card(other)).toBeHidden();
});

test("a card open for an item that closed somewhere else shows what became of it and leaves", async ({ page, context }) => {
  // Arrange
  await standInStream(context);
  await announcer(context);
  await boardAsked(page, { reviews: [wait] }, WAITS);
  await openCard(page, WAITS);
  await recordDoneCards(page);

  // Act: the goblin reported again, which takes its wait back.
  await send(page, "snapshot", asking({ reviews: [{ ...wait, state: "withdrawn", reason: "The goblin moved on.", updated_at: "2026-10-02T11:06:00Z" }] }, 3));

  // Assert
  await expect.poll(() => doneCards(page)).toContain("ClosedWithdrawn: The goblin moved on.");
  await expect(card(page)).toBeHidden();
  await expectGone(page, WAITS);
});

// The Overlord, 2026-10-13 at about 13:58Z, at a bar reading "1 question waits
// for the CFO (20 min). 15 goblins at work.": "Why is it telling me there's a
// question that waits for me for twenty minutes, and I there's no command
// center?" Olive's question had waited on the CFO since 13:43Z, and six of the
// fifteen goblins were paused. The bar names only what his Command Center
// shows, where he can answer it himself, and counts only the goblins at work.
test("the CFO's bar names only a goblin's question his Command Center shows him, which he answers there, and counts only the goblins at work", async ({ page, context }, testInfo) => {
  // Arrange
  await standInStream(context);
  await announcer(context);
  const supervisor = await actions(context);
  const RING = "Should the Project Home map's surrounding-context ring shrink from 2,500 m to 1,000 m, or stay as it is?";
  const olive = { id: "notify-pd-billing-margin-review-1001446", identity: "e".repeat(64), task: "pd-billing-margin-review", status: "pending", created_at: "2026-10-13T13:43:01Z", text: RING, options: ["Shrink to 1,000 m", "Keep 2,500 m"], recommended: "Shrink to 1,000 m" };
  const goblin = (id: string, fields: Record<string, unknown> = {}) => ({ id, title: "Goblin " + id, project: "code-goblins", phase: "working", generation: id + "-1", verified: false, ...fields });
  const paused = (id: string, reason: string) => goblin(id, { phase: "paused", lifecycle: { phase: "paused", action: "pause", at: "2026-10-13T13:30:00Z", pause: { reason, until: "", at: "2026-10-13T13:30:00Z" }, kept: [], stopped: [], problems: [], handoff_saved: true, validation_restarts: false } });
  const tasks = [goblin("pd-billing-margin-review", { title: "Billing margin review" }), ...Array.from({ length: 8 }, (_, i) => goblin("working-" + i)),
    paused("memory-a", "memory"), paused("memory-b", "memory"), paused("dependency", "dependency"), paused("allowance", "allowance"), paused("question", "question"), paused("overlord", "overlord")];
  await page.goto("/");

  // Act
  await send(page, "snapshot", { ...quiet, revision: 2, at: "2026-10-13T14:03:01Z", tasks, questions: [olive] });

  // Assert
  await expect(bar(page)).toContainText("1 question waits for the CFO (20 min). 9 goblins at work.");
  await expect(badge(page)).toHaveAccessibleName("Command Center, 1 waiting on you");
  await expect(toasts(page)).toHaveCount(0);
  await testInfo.attach("the bar and the question it names", { body: await page.screenshot(), contentType: "image/png" });
  await openCard(page, RING);
  await card(page).getByText("Shrink to 1,000 m", { exact: true }).click();
  await card(page).locator(".send-decision").click();
  await expect(card(page)).toBeVisible();
  await expect(card(page)).toContainText("Sending");
  supervisor.accept();
  await expect.poll(() => supervisor.posted.map((body) => [body.kind, body.question_id])).toEqual([["goblin_answer", olive.id]]);
});

// The Overlord, 2026-10-18, after Send and end on Kenny's page on local host:
// "we should get an immediate result in my command center that it's cleared
// and that question no longer shows." A goblin's question asked on its Scrawl
// page, which the CFO left long enough that it waits on him, leaves Waiting on
// you within a second of the supervisor reading his answer on the page, on
// local host or the tailnet, and its open card finishes with his answer.
for (const [where, link] of [["local host", "http://127.0.0.1:4387/session/030284fb3915be1f"], ["the tailnet", "https://pc.example.ts.net:4388/session/030284fb3915be1f"]]) {
  test(`his Send and end on a goblin's page on ${where} takes its question off Waiting on you within a second`, async ({ page, context }) => {
    // Arrange
    await standInStream(context);
    await announcer(context);
    const ASKED = "Approve the three mockups (page " + link + ")";
    const asked = { ...question, id: "notify-cg-probe-1000231", task: "cg-probe", generation: "g1", seq: 1000231, text: ASKED, options: ["Build it as shown", "Change it"], recommended: "Build it as shown" };
    const at = "2026-10-02T11:11:00Z";
    await page.goto("/");
    await send(page, "snapshot", quiet);
    await send(page, "snapshot", { ...asking({ questions: [asked] }), at });
    await expect(badge(page)).toHaveAccessibleName("Command Center, 1 waiting on you");
    await openCard(page, ASKED);
    await recordDoneCards(page);

    // Act
    const sent = Date.now();
    await send(page, "snapshot", { ...asking({ questions: [{ ...asked, status: "succeeded", message: "You answered on its page.", answer: "Build it as shown", answer_kind: "option", answered_option: "Build it as shown", answered_by: "overlord", answered_in: "page", answered_at: at }] }, 3), at });
    await expect(badge(page)).toHaveAccessibleName("Command Center", { timeout: 1000 });
    const gone = Date.now() - sent;

    // Assert
    expect(gone).toBeLessThan(1000);
    await expect.poll(() => doneCards(page)).toContainEqual(expect.stringContaining("You answered on its page: Build it as shown"));
    await expectGone(page, ASKED);
  });
}
