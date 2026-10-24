import { expect, test, type Locator, type Page } from "./site";

// A merge train shows as one card with its pull requests: a running one at
// the top of In progress, a finished one at the top of Completed. The
// supervisor is played by one held snapshot.
const REPO = "https://github.com/northwind/northwind-api";
const car = (number: number, state: string, title: string, note = "", goblin = "", goblin_title = "") => ({ number, url: REPO + "/pull/" + number, title, task: "nw-" + number, state, note, goblin, goblin_title });
const SNAPSHOT = {
  healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", revision: 1, attention: [],
  sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }],
  tasks: [
    { id: "nw-sync", title: "Say why a billing sync fails", project: "northwind-api", phase: "working", verified: false, generation: "nw-sync-1", harness: "claude", since: new Date(Date.now() - 42 * 60_000).toISOString() },
  ],
  merge_trains: [
    {
      id: "northwind-api-20261007-160000", repository: "northwind/northwind-api", base: "main", pr: REPO + "/pull/900", state: "testing", runs: 2,
      started: new Date(Date.now() - 20 * 60_000).toISOString(), note: "CI failed on #55, #56, #57, #58 (test), so its first half rides alone next: #55, #56",
      cars: [car(55, "riding", "Retry the webhook"), car(56, "riding", "Export the audit trail with every field the auditors asked for in their last review"), car(57, "waiting", "Fix the docs links"), car(58, "waiting", "Rename the sync job")],
    },
    {
      id: "northwind-api-20261007-120000", repository: "northwind/northwind-api", base: "main", pr: REPO + "/pull/899", state: "stopped", runs: 3,
      started: new Date(Date.now() - 5 * 3600_000).toISOString(), finished: new Date(Date.now() - 4 * 3600_000).toISOString(), note: "#53 breaks CI on main: test (" + REPO + "/actions/runs/7)",
      cars: [car(51, "landed", "Ship the export", "", "Ingrid", "Clock Icon Restorer of the Minimal Cards"), car(52, "landed", "Cache the rates"), car(53, "culprit", "Speed up the importer", "failed: test (" + REPO + "/actions/runs/7)"), car(54, "returned", "Tidy the logs")],
    },
  ],
};

async function open(page: Page, snapshot: object = SNAPSHOT) {
  await page.addInitScript((data) => {
    class HeldStream extends EventTarget {
      onerror: unknown = null;
      constructor() { super(); setTimeout(() => this.dispatchEvent(new MessageEvent("snapshot", { data }))); }
      close() {}
    }
    Object.defineProperty(window, "EventSource", { value: HeldStream });
  }, JSON.stringify(snapshot));
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

// A running train with the riders a case names, alone on the board.
const trainWith = (cars: ReturnType<typeof car>[]) => ({ ...SNAPSHOT, tasks: [], merge_trains: [{ ...SNAPSHOT.merge_trains[0], note: "", cars }] });
const SHAPES = [
  { name: "one rider", cars: [car(55, "riding", "Retry the webhook")] },
  { name: "two riders", cars: [car(55, "riding", "Retry the webhook"), car(56, "riding", "Export the audit trail", "", "Biff", "Terminal Tamer")] },
  { name: "a waiting rider", cars: [car(55, "riding", "Retry the webhook"), car(57, "returned", "Fix the docs links")] },
];

// The heights of a card, of each of its parts and of each of its riders.
const heights = (card: Locator) => card.evaluate((element) => {
  const heightOf = (part: Element) => Math.round(part.getBoundingClientRect().height);
  return { card: heightOf(element), parts: [...element.children].map(heightOf), riders: [...element.querySelectorAll(".train-car")].map(heightOf) };
});

const column = (page: Page, name: string) => page.locator(".board-column").filter({ has: page.getByRole("heading", { name: new RegExp("^" + name) }) });

for (const viewport of [{ name: "in his window", width: 1707, height: 1067 }, { name: "on a phone", width: 390, height: 844 }]) {
  test.describe(viewport.name, () => {
    test.use({ viewport: { width: viewport.width, height: viewport.height } });

    test("a running train heads In progress as one card with its pull requests", async ({ page }) => {
      await open(page);

      const running = column(page, "In progress").locator(".train-card");
      await expect(running).toHaveCount(1);
      await expect(running).toContainText("northwind/northwind-api into main");
      const status = running.locator("a.train-status");
      await expect(status).toHaveText("CI tests #55, #56, run 2");
      await expect(status).toHaveAttribute("href", REPO + "/pull/900");
      await expect(status).toHaveClass(/train-pending/);
      // What happened last is in the status's tip, never a line on the card.
      await expect(status).toHaveAttribute("data-tip", /first half rides alone next: #55, #56$/);
      await expect(running.locator("p")).toHaveCount(0);
      // A rider says where it stands only when that differs from the train.
      await expect(running.locator(".train-car")).toHaveText([/#55.*Retry the webhook$/, /#56.*Export the audit trail.*review$/, /#57.*Fix the docs links.*Waits its turn/, /#58.*Rename the sync job.*Waits its turn/]);
      await expect(running.locator(".train-car").first().locator("a")).toHaveAttribute("href", REPO + "/pull/55");
      // The train's card comes before the goblins' cards.
      const first = await column(page, "In progress").locator(".train-card, .task-card-shell").first().getAttribute("class");
      expect(first).toContain("train-card");
    });

    test("a finished train heads Completed and lists what it landed and what it could not merge", async ({ page }) => {
      await open(page);

      const finished = column(page, "Completed").locator(".train-card");
      await expect(finished).toHaveCount(1);
      await expect(finished.locator(".train-status")).toHaveText("Landed on run 3");
      await expect(finished.locator(".train-status")).toHaveClass(/train-passed/);
      // The train's status says Landed, so its landed riders do not say it
      // again; one it could not merge says what happened to it.
      await expect(finished.locator(".train-car")).toHaveText(["#51Ingrid - Clock Icon Restorer of the Minimal Cards", "#52Cache the rates", "#53Speed up the importerBreaks CI", "#54Tidy the logsRides the next train"]);
      await expect(finished.locator(".train-car-state")).toHaveText(["Breaks CI", "Rides the next train"]);
      await expect(finished.locator(".train-car").nth(2)).toHaveClass(/train-failed/);
      await expect(column(page, "In progress").locator(".train-card")).toHaveCount(1);
    });

    test("a train's card stays inside its column and a long title wraps, never over its state", async ({ page }) => {
      await open(page);

      const card = column(page, "In progress").locator(".train-card");
      const fits = await card.evaluate((element) => {
        const frame = element.getBoundingClientRect();
        const states = [...element.querySelectorAll(".train-car-state")].map((state) => state.getBoundingClientRect());
        return {
          inside: [...element.children].every((child) => { const box = child.getBoundingClientRect(); return box.left >= frame.left - 1 && box.right <= frame.right + 1; }),
          statesInside: states.every((state) => state.right <= frame.right + 1),
        };
      });
      expect(fits).toEqual({ inside: true, statesInside: true });
    });

    // The Overlord, 2026-10-17: "don't need to have landed in #1073 lines in
    // completed, instead allow goblin name and title to flex fit that row".
    test("a landed rider's goblin name and title take the row and wrap whole", async ({ page }, testInfo) => {
      // Arrange
      await open(page);
      const finished = column(page, "Completed").locator(".train-card");
      const title = finished.locator(".train-car").first().locator(".train-car-title");

      // Act
      await finished.screenshot({ path: testInfo.outputPath("landed-train.png") });
      const fit = await title.evaluate((element) => {
        const row = element.closest(".train-car")!.getBoundingClientRect(), goblin = element.closest(".train-car-goblin")!.getBoundingClientRect();
        return { isWhole: element.scrollHeight <= element.clientHeight && element.scrollWidth <= element.clientWidth, reachesTheEnd: row.right - goblin.right < 12 };
      });

      // Assert
      await expect(title).toHaveText("Ingrid - Clock Icon Restorer of the Minimal Cards");
      expect(fit).toEqual({ isWhole: true, reachesTheEnd: true });
    });

    // The Overlord, 2026-10-20: "why so much empty space fix ui bug". A
    // grid hands the height it has over its parts to its rows, so a card in a
    // grid taller than it, or held taller than its parts, spread them apart.
    for (const shape of SHAPES) {
      test(`${shape.name}: a train's card is no taller for a column with height to spare`, async ({ page }) => {
        // Arrange
        await open(page, trainWith(shape.cars));
        const card = column(page, "In progress").locator(".train-card");
        const before = await heights(card);

        // Act
        await page.locator(".train-cards").evaluate((element) => element.style.setProperty("height", "900px"));

        // Assert
        expect(await heights(card)).toEqual(before);
      });

      test(`${shape.name}: a train's card keeps its rows to their content when held taller than they are`, async ({ page }) => {
        // Arrange
        await open(page, trainWith(shape.cars));
        const card = column(page, "In progress").locator(".train-card");
        const before = await heights(card);

        // Act
        await card.evaluate((element) => element.style.setProperty("min-height", "700px"));

        // Assert
        expect(await heights(card)).toEqual({ ...before, card: 700 });
      });
    }
  });
}

// The Overlord, 2026-10-21, of a train's card: "need to wrap the 1997 like
// title goblins to not cause spacing issue", then, of a rider whose name broke
// inside its words with "Rides the next train" keeping its width: "this is
// hideous fix layout and formatting". A rider's words wrap inside their own
// column at any card width: the name breaks only at spaces, where the rider
// stands goes under the name when the row is too narrow for both, and the
// rows keep their gaps.
const LONG_RIDERS = [
  car(1995, "riding", "Tidy the logs"),
  car(1997, "riding", "merging main to fix a conflict in the billing sync exporter before it retries the whole run"),
  car(1998, "riding", "retry-the-webhook-after-every-timeout-in-the-billing-sync-exporter-without-spaces"),
  car(2000, "riding", "Cache the rates", "", "Wendell", "Deploy Doctor of the Long Running Checks"),
  car(2003, "returned", "Rename the sync job", "it was labelled hold", "Wendell", "Deploy Doctor"),
];

for (const width of [260, 395, 520]) {
  test(`at ${width}px wide a rider's words wrap inside their column, whole, clear of its icon, with its rows at their natural height`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width: 1200, height: 900 });
    await open(page, trainWith(LONG_RIDERS));
    const card = column(page, "In progress").locator(".train-card");
    await card.evaluate((element, size) => element.style.setProperty("width", size + "px"), width);

    // Act
    const rows = await card.locator(".train-car").evaluateAll((items) => items.map((item) => {
      const title = item.querySelector(".train-car-title")!, state = item.querySelector(".train-car-state");
      const avatar = item.querySelector(".goblin-avatar")!.getBoundingClientRect(), text = title.getBoundingClientRect(), row = item.getBoundingClientRect();
      // A word a line break falls inside makes two boxes. A word longer than
      // the column has no other way to fit, so it alone may break.
      const words = [...title.textContent!.matchAll(/\S+/g)].map((match) => {
        const range = document.createRange();
        range.setStart(title.firstChild!, match.index!);
        range.setEnd(title.firstChild!, match.index! + match[0].length);
        return { isBroken: range.getClientRects().length > 1, isLong: match[0].length > 24 };
      });
      return {
        isWhole: title.scrollWidth <= title.clientWidth + 1 && (!state || state.scrollWidth <= state.clientWidth + 1),
        isClearOfIcon: text.left >= avatar.right - 1,
        isInsideRow: text.right <= row.right + 1 && (!state || state.getBoundingClientRect().right <= row.right + 1),
        isNameInWholeWords: words.every((word) => !word.isBroken || word.isLong),
        top: row.top, bottom: row.bottom,
      };
    }));
    const frame = await card.evaluate((element) => ({ scrollWidth: element.scrollWidth, clientWidth: element.clientWidth }));

    // Assert
    const GOOD = { isWhole: true, isClearOfIcon: true, isInsideRow: true, isNameInWholeWords: true };
    expect(rows.map((row) => ({ isWhole: row.isWhole, isClearOfIcon: row.isClearOfIcon, isInsideRow: row.isInsideRow, isNameInWholeWords: row.isNameInWholeWords }))).toEqual(rows.map(() => GOOD));
    expect(frame.scrollWidth).toBeLessThanOrEqual(frame.clientWidth);
    for (let index = 1; index < rows.length; index++) expect(rows[index].top - rows[index - 1].bottom).toBeLessThanOrEqual(8);
  });
}

for (const width of [320, 420, 560]) {
  test(`with its riders ${width}px wide a train's panel keeps each rider's name in whole words, with where it stands giving way first`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width: 1100, height: 900 });
    await open(page, trainWith(LONG_RIDERS));
    await column(page, "In progress").locator(".train-card .train-title").click();
    const rows = page.locator(".train-panel .train-panel-car");
    await expect(rows).toHaveCount(LONG_RIDERS.length);
    await page.locator(".train-panel .train-panel-rows").evaluate((element, size) => element.style.setProperty("width", size + "px"), width);

    // Act
    const fits = await rows.evaluateAll((items) => items.map((item) => {
      const name = item.querySelector(".train-panel-name")!, state = item.querySelector(".train-panel-state")!;
      const avatar = item.querySelector(".goblin-avatar")!.getBoundingClientRect(), text = name.getBoundingClientRect(), row = item.getBoundingClientRect();
      const words = [...name.textContent!.matchAll(/\S+/g)].map((match) => {
        const range = document.createRange();
        range.setStart(name.firstChild!, match.index!);
        range.setEnd(name.firstChild!, match.index! + match[0].length);
        return { isBroken: range.getClientRects().length > 1, isLong: match[0].length > 24 };
      });
      return {
        isNameInWholeWords: words.every((word) => !word.isBroken || word.isLong),
        isClearOfIcon: text.left >= avatar.right - 1,
        isInsideRow: text.right <= row.right + 1 && state.getBoundingClientRect().right <= row.right + 1,
        isNameTheWiderPart: text.width >= state.getBoundingClientRect().width * 0.5,
      };
    }));

    // Assert
    expect(fits).toEqual(fits.map(() => ({ isNameInWholeWords: true, isClearOfIcon: true, isInsideRow: true, isNameTheWiderPart: true })));
  });
}
