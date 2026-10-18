import { doneCards, expect, openItem, recordDoneCards, test, type BrowserContext, type Page } from "./site";

// The Overlord, 2026-10-17, with a picture of Greta's card in the Command
// Center: "I opened up a Scrawl page via this little drop-down on the Command
// Center, and the button didn't open up the Command Center modal, it just took
// me right to the page ... And then also, when I finished it and sent it and
// done, it didn't react or update. It still shows I have a notification." And
// about 19:30Z: "Make sure every time I hit send it in, the command center is
// closed ... There does not need to be a text box in when there is a scroll
// open review page involved." A Scrawl review card opens in the Command
// Center, has no box for words, and his Send and End on its page closes it at
// once, a goblin's review and the CFO's alike. These tests send every
// snapshot themselves, so nothing reloads the board.
test.use({ timezoneId: "UTC", viewport: { width: 1440, height: 900 } });

const PAGE = "https://sermon.tailcc4238.ts.net:4388/session/1a4d8f434fcca24b";
const TITLE = "Before and after pictures of the Orchestration canvas, now showing only goblins in progress";
const greta = { id: "cg-canvas-live-only", title: "Canvas shows only live goblins", goblin_name: "Greta", goblin_title: "Canvas Captain", project: "code-goblins", phase: "working", generation: "g1", verified: false };
const wanda = { id: "pd-mcp-only", title: "Retire the web routes", goblin_name: "Wanda", goblin_title: "API Ace", project: "precisiondocs", phase: "waiting", generation: "g1", verified: false };
const quiet = {
  healthy: true, instance: "board-0m", cfo_runs: true, cfo_terminal: "cfo", cfo_harness: "claude", revision: 1, at: "2026-10-17T18:30:00Z",
  tasks: [greta, wanda], questions: [] as object[], reviews: [] as object[], runs: [], credentials: [], actions: [],
};
const asking = (item: { questions?: object[]; reviews?: object[] }, revision: number) => ({ ...quiet, revision, questions: item.questions || [], reviews: item.reviews || [] });
// A goblin's review he is asked to watch, as cfo present --watch publishes it:
// its page is the one the supervisor polls.
const gretaReview = { id: "cg-canvas-live-only-before-after", identity: "g".repeat(64), task: greta.id, title: TITLE, lavish: PAGE, lavish_page: "D:\\scratch\\cg-canvas-live-only\\review\\index.html", state: "open", created_at: "2026-10-17T18:09:52Z", updated_at: "2026-10-17T18:09:52Z" };
// The CFO's own review, as cfo review --lavish without --task publishes it.
const cfoReview = { ...gretaReview, id: "itascaunity-mockup-20261017", identity: "c".repeat(64), task: "", title: "Oona's mockup of the Russo and Presta one-page site, desktop and phone" };
// What the supervisor's poll of the page makes of his Send and End there.
const answeredOnPage = (review: typeof gretaReview, words: string) => ({ ...review, state: "answered", answered_by: "overlord", answered_in: "page", reason: "You answered on its page; " + (review.task ? "the goblin" : "the CFO") + " has it.", answer: words, updated_at: "2026-10-17T18:14:31Z" });

// Every board the test opens gets its snapshots from the test, through a
// stand-in event stream, one event at a time.
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
  await context.route("**/api/announce", (route) => route.fulfill({ json: { claimed: route.request().postDataJSON().keys } }));
  await context.route("**/api/reviews/*/preview", (route) => route.fulfill({ contentType: "text/html", body: "<!doctype html><title>Canvas</title><h1>Before and after</h1>" }));
}
const send = (page: Page, data: object) => page.evaluate((detail) => { window.dispatchEvent(new CustomEvent("fixture-stream", { detail })); }, { type: "snapshot", data });

const badge = (page: Page) => page.locator(".command-center-menu > summary");
const bar = (page: Page) => page.locator(".cfo-pin");
const card = (page: Page) => page.locator("dialog.question-modal");
const waitingList = (page: Page) => page.getByRole("region", { name: "Waiting on you" });

async function boardAsked(page: Page, item: { questions?: object[]; reviews?: object[] }, waiting = 1) {
  await page.goto("/");
  await send(page, quiet);
  await expect(bar(page)).toContainText("All quiet");
  await send(page, asking(item, 2));
  await expect(badge(page)).toHaveAccessibleName("Command Center, " + waiting + " waiting on you");
}

for (const way of [
  { name: "its row", click: (page: Page) => waitingList(page).locator("li .inbox-summary").filter({ hasText: TITLE }).click() },
  { name: "its button", click: (page: Page) => waitingList(page).getByRole("button", { name: "Answer Greta - Canvas Captain: " + TITLE }).click() },
]) {
  test(`${way.name} in the list opens a goblin's Scrawl review in the Command Center, and its page opens only from there`, async ({ page, context }) => {
    // Arrange
    await standInStream(context);
    await boardAsked(page, { reviews: [gretaReview] });
    await badge(page).click();
    const opened: Page[] = [];
    context.on("page", (other) => opened.push(other));

    // Act
    await way.click(page);

    // Assert
    await expect(card(page)).toBeVisible();
    await expect(card(page).getByRole("heading", { name: TITLE })).toBeVisible();
    await expect(card(page).getByRole("link", { name: "Open review: " + TITLE })).toHaveAttribute("href", PAGE);
    await expect(card(page).getByRole("button", { name: "Skip this card" })).toBeVisible();
    await expect(waitingList(page)).toBeHidden();
    expect(opened, "the list opened no page of its own").toEqual([]);
  });
}

for (const who of [{ name: "a goblin's", review: gretaReview }, { name: "the CFO's", review: cfoReview }]) {
  test(`Send and End on ${who.name} Scrawl page closes its open card at once and it moves to History with what he sent`, async ({ page, context }) => {
    // Arrange
    await standInStream(context);
    await boardAsked(page, { reviews: [who.review] });
    await openItem(page, who.review.title);
    await expect(card(page).getByRole("heading", { name: who.review.title })).toBeVisible();
    await recordDoneCards(page);

    // Act
    await send(page, asking({ reviews: [answeredOnPage(who.review, "Looks right. Ship it.")] }, 3));

    // Assert
    await expect.poll(() => doneCards(page)).toContainEqual(expect.stringMatching(/^Answered/));
    await expect(card(page)).toBeHidden();
    await expect(badge(page)).toHaveAccessibleName("Command Center");
    await expect(bar(page)).toContainText("All quiet");
    await expect(page).toHaveTitle("Code Goblins");
    await badge(page).click();
    await expect(page.getByText("Nothing is waiting on you.")).toBeVisible();
    await page.locator(".inbox-history > summary").click();
    const row = page.locator(".inbox-history li").filter({ hasText: who.review.title });
    await row.getByRole("button", { name: /^Open: / }).click();
    await expect(row).toContainText("You wrote: Looks right. Ship it.");
  });
}

for (const shown of [
  { name: "a goblin's review its page answers", review: gretaReview, row: TITLE },
  { name: "the CFO's review given only its Scrawl link", review: { ...cfoReview, lavish_page: "" }, row: cfoReview.title },
  { name: "a goblin's wait on a Scrawl page", review: { ...gretaReview, id: "waiting-cg-canvas-live-only-12", title: "Waiting on you: " + TITLE + " (page " + PAGE + ")" }, row: TITLE },
]) {
  test(`${shown.name} has no box for words, only Skip and the button that opens its page`, async ({ page, context }) => {
    // Arrange
    await standInStream(context);
    await boardAsked(page, { reviews: [shown.review] });

    // Act
    await openItem(page, shown.row);

    // Assert
    await expect(card(page).locator(".question-card")).toBeVisible();
    await expect(card(page).getByRole("textbox")).toHaveCount(0);
    await expect(card(page).getByRole("button", { name: "Something else?" })).toHaveCount(0);
    await expect(card(page).locator(`a[href="${PAGE}"]`)).toHaveCount(1);
    await expect(card(page).getByRole("button", { name: "Skip this card" })).toBeVisible();
  });
}

// The Overlord, 2026-10-17 about 18:20Z, at "Wanda - API Ace is waiting on you"
// with only Back, Next, an X and a pencil: "These follow-ups should not be
// notifications in the Command Center, that can be removed. But the initial
// one is critical, and should still stay." The CFO's question's answer reaches
// the goblin only through the CFO, so the follow-up stays gone once he answers
// it; the goblin's own question's answer reaches it at once, so its wait comes
// back as its own card.
const followUp = { id: "waiting-pd-mcp-only-1008309", identity: "w".repeat(64), task: wanda.id, title: "Waiting on you: Waiting for the Overlord's answer on the parity tool sizing. Both cuts are live, nothing is in flight, and I start nothing new meanwhile.", state: "open", created_at: "2026-10-17T18:16:11Z", updated_at: "2026-10-17T18:16:11Z" };
for (const decision of [
  { name: "the CFO's question for the goblin", after: "answering it clears both", isWaitBack: false, kind: "cfo_answer", question: { id: "pd-connector-parity-20261017", identity: "c".repeat(64), task: "", for_task: wanda.id, status: "pending", created_at: "2026-10-17T18:16:04Z", text: "Build connector tools for the 63 parity routes?", options: ["Build all but the site plan editor", "Build all of them", "End her task here"], recommended: "Build all but the site plan editor" } },
  { name: "the goblin's own question the CFO left to him", after: "once he answers it the wait comes back as its own card", isWaitBack: true, kind: "goblin_answer", question: { id: "notify-pd-mcp-only-1008308", identity: "w".repeat(64), task: wanda.id, generation: "g1", status: "pending", created_at: "2026-10-17T18:15:30Z", text: "Do I build connector tools for the 63 parity routes now, or does the task end here?", options: ["Build all but the site plan editor", "Build all of them", "End the task here"], recommended: "Build all but the site plan editor" } },
]) {
  test(`a goblin's wait beside ${decision.name} shows only the decision, and ${decision.after}`, async ({ page, context }) => {
    // Arrange
    await standInStream(context);
    const posted: Record<string, unknown>[] = [];
    await context.route("**/api/actions", async (route) => {
      const body: Record<string, unknown> = route.request().postDataJSON();
      posted.push(body);
      await route.fulfill({ status: 202, json: { ...body, status: "queued" } });
    });
    await boardAsked(page, { questions: [decision.question], reviews: [followUp] });
    await badge(page).click();

    // Act
    const rows = await waitingList(page).locator("li").allInnerTexts();
    await openItem(page, decision.question.text);
    await expect(card(page).getByRole("form", { name: decision.question.text })).toBeVisible();
    await expect(card(page).locator(".stack-count")).toHaveCount(0);
    await card(page).getByText("Build all but the site plan editor", { exact: true }).click();
    await card(page).locator(".send-decision").click();
    await expect.poll(() => posted.map((body) => body.kind)).toEqual([decision.kind]);
    await send(page, asking({ questions: [{ ...decision.question, status: "queued", answer_id: String(posted[0].id), answer: "Build all but the site plan editor", answer_kind: "option" }], reviews: [followUp] }, 3));

    // Assert
    expect(rows).toHaveLength(1);
    expect(rows[0]).toContain(decision.question.text.slice(0, 30));
    if (decision.isWaitBack) {
      await expect(card(page)).toContainText("Waiting for the Overlord's answer on the parity tool sizing");
      await expect(badge(page)).toHaveAccessibleName("Command Center, 1 waiting on you");
      return;
    }
    await expect(card(page)).toBeHidden();
    await expect(badge(page)).toHaveAccessibleName("Command Center");
    await expect(bar(page)).toContainText("All quiet");
    await badge(page).click();
    await expect(page.getByText("Nothing is waiting on you.")).toBeVisible();
    await expect(page.getByText("Waiting for the Overlord's answer on the parity tool sizing")).toHaveCount(0);
  });
}
