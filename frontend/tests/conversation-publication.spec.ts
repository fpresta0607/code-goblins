import { expect, test, type Locator, type Page } from "./site";
import { at, box, field, GOBLIN, NAME, open, openGoblin, send, snapshot, typed } from "./session-board";

const conversation = "fixture-conversation";
const reply = (seq: number, text: string) => ({ id: "m" + seq, seq, at, kind: "reply", blocks: [{ kind: "text", text }] });
const prompt = (seq: number, text = "Question " + seq) => ({ id: "m" + seq, seq, at, kind: "prompt", source: "overlord", text });
const messages = Array.from({ length: 1000 }, (_, seq) => seq % 2 ? reply(seq, "Answer " + seq) : prompt(seq));
const pageOf = (rows: object[] = messages, identity = conversation, more = false) => ({ conversation: identity, messages: rows, more });
const talkOf = (page: Page, name = NAME) => page.getByRole("log", { name: "Messages of " + name });
const sendPage = (page: Page, data: object) => page.evaluate((value) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data: value } })), data);
const sendBoard = (page: Page, data: object) => page.evaluate((value) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: value })), data);

async function reading(talk: Locator) {
  return talk.evaluate(async (element) => {
    let previous = "", quiet = 0;
    for (let frame = 0; frame < 120; frame++) {
      await new Promise(requestAnimationFrame);
      const top = element.getBoundingClientRect().top;
      const row = [...element.querySelectorAll<HTMLElement>("[data-message]")].find((item) => item.getBoundingClientRect().bottom > top);
      const place = { id: row?.dataset.message ?? "", offset: (row?.getBoundingClientRect().top ?? top) - top, top: element.scrollTop, height: element.scrollHeight, text: row?.textContent };
      const next = JSON.stringify(place);
      quiet = next === previous ? quiet + 1 : 0;
      if (quiet === 3 && place.id) return place;
      previous = next;
    }
    throw new Error("Reading position did not settle");
  });
}

async function captureRows(talk: Locator) {
  return talk.evaluate((element) => {
    const rows = [...element.querySelectorAll<HTMLElement>("[data-message], [data-queued], .reply-working")];
    (window as unknown as { capturedRows: HTMLElement[] }).capturedRows = rows;
    return rows.map((row) => ({ text: row.textContent, rect: row.getBoundingClientRect().toJSON() }));
  });
}

async function currentRows(talk: Locator) {
  return talk.evaluate((element) => {
    const rows = [...element.querySelectorAll<HTMLElement>("[data-message], [data-queued], .reply-working")];
    const prior = (window as unknown as { capturedRows: HTMLElement[] }).capturedRows;
    return { sameNodes: rows.length === prior.length && rows.every((row, index) => row === prior[index]),
      rows: rows.map((row) => ({ text: row.textContent, rect: row.getBoundingClientRect().toJSON() })) };
  });
}

// pressGated presses a control the message no longer allows and expects a
// new banner over the box to say why, not one an earlier press left.
async function pressGated(page: Page, button: Locator, why: string) {
  await box(page).locator(".composer-banner").evaluateAll((banners) => { for (const banner of banners) banner.setAttribute("data-seen", ""); });
  await button.evaluate((element: HTMLButtonElement) => element.click());
  await expect(box(page).locator(".composer-banner:not([data-seen])")).toHaveText(why);
}

async function readNearEnd(talk: Locator, page: Page) {
  await reading(talk);
  await talk.evaluate((element) => { element.scrollTop -= 30; });
  const place = await reading(talk);
  await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
  return place;
}

for (const ordering of ["response first", "stream first"] as const) {
  test(`pagination rejects another conversation when ${ordering}`, async ({ page }) => {
    const first = pageOf(messages.slice(50, 100), conversation, true);
    const reset = pageOf(messages.slice(50, 100).map((item) => ({ ...item, ...(item.kind === "reply" ? { blocks: [{ kind: "text", text: "Reset answer " + item.seq }] } : { text: "Reset question " + item.seq }) })), "reset-conversation", true);
    await open(page, snapshot(), "", first);
    let release = () => {};
    const held = new Promise<void>((resolve) => { release = resolve; });
    const requests: string[] = [];
    await page.route((url) => url.pathname === "/api/messages" && url.searchParams.has("before"), async (route) => {
      requests.push(route.request().url());
      await held;
      await route.fulfill({ json: pageOf(messages.slice(0, 50).map((item) => ({ ...item, ...(item.kind === "reply" ? { blocks: [{ kind: "text", text: "Reset older " + item.seq }] } : { text: "Reset older " + item.seq }) })), "reset-conversation") });
    });
    await openGoblin(page);
    const talk = talkOf(page);
    await expect(talk).toContainText("Answer 99");
    await talk.evaluate((element) => { element.scrollTop = 0; });
    await expect.poll(() => requests.length).toBe(1);
    const place = await reading(talk);
    if (ordering === "stream first") await sendPage(page, reset);
    const response = page.waitForResponse((value) => new URL(value.url()).searchParams.has("before"));
    release();
    await (await response).finished();
    if (ordering === "response first") {
      expect(await reading(talk)).toEqual(place);
      await expect(talk).not.toContainText("Reset older");
      await sendPage(page, reset);
    }
    expect(await reading(talk)).toEqual(place);
    await expect(talk).not.toContainText("Reset older");
    await page.getByRole("button", { name: "Jump to latest" }).click();
    await expect(talk.locator('[data-message="m99"]')).toContainText("Reset answer 99");
    await expect(talk.locator('[data-message="m99"]')).toHaveCount(1);
    expect(new URL(requests[0]).searchParams.get("conversation")).toBe(conversation);
  });
}

for (const owner of ["CFO", "goblin"] as const) {
  test(`${owner} keeps its working word while reading and Stop uses the live relay`, async ({ page }) => {
    const tail = { id: "working-tail", kind: "message", task_id: owner === "CFO" ? "" : GOBLIN.id, status: "queued", text: "A queued message below the working indicator\nwith another line", created_at: at, updated_at: at };
    const relay = await open(page, snapshot({ actions: [tail] }, { phase: "settled" }), "", pageOf());
    if (owner === "CFO") await page.keyboard.press("Control+Alt+1"); else await openGoblin(page);
    const talk = talkOf(page, owner === "CFO" ? "CFO" : NAME);
    await expect(talk).toBeVisible();
    await expect.poll(() => relay.views).toBeGreaterThan(0);
    relay.say({ type: "screen", asking: false, open: false, working: true, doing: "Cogitating" });
    await expect(talk.getByRole("status", { name: "Cogitating" })).toBeVisible();
    const stop = talk.getByRole("button", { name: "Stop", exact: true });
    await expect(stop).toHaveCount(1);
    const place = await readNearEnd(talk, page);
    const rows = await captureRows(talk);
    relay.say({ type: "screen", asking: false, open: false, working: false });
    await expect(talk.getByRole("status", { name: "Cogitating" })).toBeVisible();
    expect(await reading(talk)).toEqual(place);
    expect(await currentRows(talk)).toEqual({ sameNodes: true, rows });
    await stop.click();
    await expect.poll(() => typed(relay)).toBe("\x1b");
    await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
    relay.say({ type: "held", reason: "held" });
    await expect(stop).toHaveAttribute("aria-disabled", "true");
    await expect(stop).toHaveCSS("opacity", "0.55");
    await stop.evaluate((button: HTMLButtonElement) => button.click());
    expect(typed(relay)).toBe("\x1b");
    await page.getByRole("button", { name: "Jump to latest" }).click();
    await expect(talk.getByRole("status", { name: "Cogitating" })).toHaveCount(0);
  });
}

test("a subagent row keeps its child presentation until the latest page is published", async ({ page }) => {
  const child = { id: "subagent:one", kind: "helper", label: "Original helper", state: "working", started: at };
  const tree = (children: object[]) => ({ task_id: GOBLIN.id, generation: GOBLIN.generation, harness: "claude", children });
  const row = { id: "m999", seq: 999, at, kind: "reply", blocks: [{ kind: "text", text: "Answer 999" }, { kind: "call", tool: "Agent", subject: "inspect", state: "running", agent: { node: child.id, type: "Explore" } }] };
  await open(page, snapshot({}, { tree: tree([child]) }), "", pageOf([...messages.slice(0, 999), row]));
  await openGoblin(page);
  const talk = talkOf(page);
  const baby = talk.locator('[data-message="m999"] .message-baby');
  await expect(baby).toContainText("Original helper");
  await expect(baby.getByRole("button", { name: "Open" })).toHaveCount(1);
  const place = await readNearEnd(talk, page);
  const before = await baby.textContent() ?? "";
  const rows = await captureRows(talk);
  await sendBoard(page, snapshot({ revision: 2 }, { tree: tree([{ ...child, label: "Changed helper" }]) }));
  await expect(baby).toHaveText(before);
  await sendBoard(page, snapshot({ revision: 3 }, { tree: tree([]) }));
  await expect(baby).toHaveText(before);
  await expect(baby.getByRole("button", { name: "Open" })).toHaveCount(1);
  expect(await reading(talk)).toEqual(place);
  expect(await currentRows(talk)).toEqual({ sameNodes: true, rows });
  await page.getByRole("button", { name: "Jump to latest" }).click();
  await expect(baby).toContainText("Baby goblin");
  await expect(baby.getByRole("button", { name: "Open" })).toHaveCount(0);
});

test("queued presentation stays until publication while settled controls cannot act", async ({ page }) => {
  const action = { id: "q1", kind: "message", task_id: GOBLIN.id, generation: GOBLIN.generation, status: "queued", text: "Queued replacement", can_send_now: true, created_at: at, updated_at: at };
  await open(page, snapshot({ actions: [action] }), "", pageOf());
  const posts: string[] = [];
  await page.route("**/api/actions", (route) => { posts.push(route.request().postData() ?? ""); return route.fulfill({ status: 202, json: { status: "ok" } }); });
  await openGoblin(page);
  const talk = talkOf(page);
  const queued = talk.locator('[data-queued="q1"]');
  await expect(queued).toContainText("Queued replacement");
  const place = await readNearEnd(talk, page);
  const rows = await captureRows(talk);
  await sendPage(page, pageOf([...messages, prompt(1000, action.text)]));
  await sendBoard(page, snapshot({ revision: 2, actions: [{ ...action, status: "succeeded", updated_at: "2026-10-22T09:01:00Z" }] }));
  await expect(queued).toHaveCount(1);
  await expect(queued.getByRole("button", { name: "Cancel" })).toHaveAttribute("aria-disabled", "true");
  expect(await reading(talk)).toEqual(place);
  expect(await currentRows(talk)).toEqual({ sameNodes: true, rows });
  for (const name of ["Send now", "Edit", "Cancel"]) {
    const button = queued.getByRole("button", { name, exact: true });
    await expect(button).toHaveCount(1);
    await expect(button).toHaveCSS("opacity", "0.55");
    await pressGated(page, button, "It has already left the queue.");
  }
  expect(posts).toEqual([]);
  await expect(field(page)).toHaveValue("");
  await page.getByRole("button", { name: "Jump to latest" }).click();
  await expect(queued).toHaveCount(0);
  await expect(talk.locator('[data-message="m1000"]')).toContainText("Queued replacement");
  await expect(talk.locator('[data-message="m1000"]')).toHaveCount(1);
});

test("a current queued action still reaches the board while reading", async ({ page }) => {
  const action = { id: "q-live", kind: "message", task_id: GOBLIN.id, generation: GOBLIN.generation, status: "queued", text: "Waiting for delivery", can_send_now: true, created_at: at, updated_at: at };
  await open(page, snapshot({ actions: [action] }), "", pageOf());
  const posts: { kind: string; target: string }[] = [];
  await page.route("**/api/actions", (route) => {
    const data: unknown = route.request().postDataJSON();
    if (typeof data === "object" && data !== null && "kind" in data && "target" in data && typeof data.kind === "string" && typeof data.target === "string") posts.push({ kind: data.kind, target: data.target });
    return route.fulfill({ status: 202, json: { status: "ok" } });
  });
  await openGoblin(page);
  const talk = talkOf(page);
  await expect(talk.locator('[data-queued="q-live"]')).toBeVisible();
  await readNearEnd(talk, page);
  await talk.locator('[data-queued="q-live"]').getByRole("button", { name: "Send now" }).click();
  await expect.poll(() => posts.map(({ kind, target }) => ({ kind, target }))).toEqual([{ kind: "message_now", target: "q-live" }]);
});

for (const owner of ["CFO", "goblin"] as const) {
  for (const status of ["queued", "succeeded"] as const) test(`${owner} sees a local send through a ${status} board handoff while reading`, async ({ page }) => {
    const relay = await open(page, snapshot(), "", pageOf());
    let release = () => {};
    const response = new Promise<void>((resolve) => { release = resolve; });
    const posted: { id: string; task_id: string; text: string }[] = [];
    await page.route("**/api/actions", async (route) => {
      posted.push(route.request().postDataJSON());
      await response;
      await route.fulfill({ status: 202, json: { status: "ok" } });
    });
    if (owner === "CFO") await page.keyboard.press("Control+Alt+1"); else await openGoblin(page);
    const name = owner === "CFO" ? "CFO" : NAME;
    const talk = talkOf(page, name);
    await expect.poll(() => relay.views).toBeGreaterThan(0);
    relay.say({ type: "held", reason: "fixture hold" });
    await expect(talk.locator('[data-message="m999"]')).toBeVisible();
    const place = await readNearEnd(talk, page);
    await talk.evaluate((element) => { (window as unknown as { originalRows: Element[] }).originalRows = [...element.querySelectorAll("[data-message]")]; });
    await field(page, name).fill("My held message");
    await send(page, name).click();
    await expect.poll(() => posted.length).toBe(1);
    const own = talk.locator("[data-queued]").filter({ hasText: "My held message" });
    await expect(own).toHaveCount(1);
    const action = { ...posted[0], kind: "message", status, created_at: at, updated_at: at };
    const other = { ...action, id: "other-send", text: "Another board message", status: "queued" };
    await sendBoard(page, snapshot({ revision: 2, actions: [action, other] }));
    await expect(own).not.toContainText("Reaching the board.");
    if (status === "queued") await expect(own.getByRole("button", { name: "Cancel" })).toHaveCount(1);
    release();
    await expect(own).toHaveCount(1);
    await expect(talk.locator('[data-queued="other-send"]')).toHaveCount(0);
    await sendPage(page, pageOf([...messages, prompt(1000, "My held message")]));
    await sendBoard(page, snapshot({ revision: 3, actions: [{ ...action, status: "succeeded" }, other] }));
    await expect(own).toHaveCount(1);
    const after = await reading(talk);
    expect({ id: after.id, offset: after.offset, top: after.top, text: after.text }).toEqual({ id: place.id, offset: place.offset, top: place.top, text: place.text });
    expect(await talk.evaluate((element) => {
      const original = (window as unknown as { originalRows: Element[] }).originalRows;
      const current = [...element.querySelectorAll("[data-message]")];
      return current.length === original.length && current.every((row, index) => row === original[index]);
    })).toBe(true);
    await expect(talk.locator('[data-message="m1000"]')).toHaveCount(0);
    await page.getByRole("button", { name: "Jump to latest" }).click();
    await expect(own).toHaveCount(0);
    await expect(talk.locator('[data-message="m1000"]')).toHaveCount(1);
    await expect(talk.locator('[data-queued="other-send"]')).toHaveCount(1);
  });

  test(`${owner} removes local feedback when a send fails while reading`, async ({ page }) => {
    const relay = await open(page, snapshot(), "", pageOf());
    let release = () => {};
    const response = new Promise<void>((resolve) => { release = resolve; });
    await page.route("**/api/actions", async (route) => {
      await response;
      await route.fulfill({ status: 500, json: { error: "Refused by fixture" } });
    });
    if (owner === "CFO") await page.keyboard.press("Control+Alt+1"); else await openGoblin(page);
    const name = owner === "CFO" ? "CFO" : NAME;
    const talk = talkOf(page, name);
    await expect.poll(() => relay.views).toBeGreaterThan(0);
    relay.say({ type: "held", reason: "fixture hold" });
    await expect(talk.locator('[data-message="m999"]')).toBeVisible();
    const place = await readNearEnd(talk, page);
    await field(page, name).fill("Refused local message");
    await send(page, name).click();
    const own = talk.locator("[data-queued]").filter({ hasText: "Refused local message" });
    await expect(own).toHaveCount(1);
    release();
    await expect(own).toHaveCount(0);
    await expect(field(page, name)).toHaveValue("Refused local message");
    const after = await reading(talk);
    expect({ id: after.id, offset: after.offset, top: after.top, text: after.text }).toEqual({ id: place.id, offset: place.offset, top: place.top, text: place.text });
  });
}

for (const where of ["at the end", "while reading"] as const) {
  test(`a send still reaching the board says so when its controls are pressed ${where}`, async ({ page }) => {
    // Arrange: the board has not answered his send yet, and the session works.
    const relay = await open(page, snapshot(), "", pageOf());
    let release = () => {};
    const response = new Promise<void>((resolve) => { release = resolve; });
    const posted: string[] = [];
    await page.route("**/api/actions", async (route) => {
      posted.push(route.request().postData() ?? "");
      await response;
      await route.fulfill({ status: 202, json: { status: "ok" } });
    });
    await openGoblin(page);
    const talk = talkOf(page);
    await expect.poll(() => relay.views).toBeGreaterThan(0);
    relay.say({ type: "held", reason: "fixture hold" });
    relay.say({ type: "screen", asking: false, open: false, working: true, doing: "Cogitating" });
    await expect(talk.locator('[data-message="m999"]')).toBeVisible();
    if (where === "while reading") await readNearEnd(talk, page);
    await field(page).fill("Still on its way");
    await send(page).click();
    await expect.poll(() => posted.length).toBe(1);
    const own = talk.locator("[data-queued]").filter({ hasText: "Still on its way" });
    await expect(own).toHaveCount(1);

    // Act and Assert: each control looks disabled, and a press says why and
    // asks the board nothing.
    for (const name of ["Send now", "Edit", "Cancel"]) {
      const button = own.getByRole("button", { name, exact: true });
      await expect(button).toHaveAttribute("aria-disabled", "true");
      await expect(button).toHaveCSS("opacity", "0.55");
      await pressGated(page, button, "It is still reaching the board.");
    }
    expect(posted).toHaveLength(1);
    release();
  });
}

test("the box follows the session while he reads up: its model, what it spent and what Up recalls", async ({ page }) => {
  // Arrange
  await open(page, snapshot(), "", pageOf());
  await openGoblin(page);
  const talk = talkOf(page);
  await expect(talk.locator('[data-message="m999"]')).toBeVisible();
  const place = await readNearEnd(talk, page);
  const rows = await captureRows(talk);

  // Act: he sends a message from another screen, and the session records
  // another model and what it spent, while he reads up.
  await page.evaluate((changed) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: [changed] } } })), prompt(1000, "Sent while reading up"));
  await page.evaluate((usage) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "usage", data: { conversation: "fixture-conversation", usage } } })), { input_tokens: 12000, output_tokens: 800, context_tokens: 72000, context_window: 100000, model: "changed-model" });

  // Assert: the box shows them at once, and the history he reads stays put.
  await expect(box(page).locator(".engine-control-model")).toHaveText("Changed Model");
  await expect(page.getByRole("meter", { name: "Context", exact: true })).toHaveAttribute("aria-valuenow", "72");
  await field(page).press("ArrowUp");
  await expect(field(page)).toHaveValue("Sent while reading up");
  expect(await reading(talk)).toEqual(place);
  expect(await currentRows(talk)).toEqual({ sameNodes: true, rows });
  await expect(talk.locator('[data-message="m1000"]')).toHaveCount(0);
});

test("a disjoint reconnect replaces the latest page and makes the missing history reachable", async ({ page }) => {
  await open(page, snapshot(), "", pageOf(messages.slice(0, 50)));
  const requests: string[] = [];
  await page.route((url) => url.pathname === "/api/messages" && url.searchParams.has("before"), (route) => {
    requests.push(route.request().url());
    return route.fulfill({ json: pageOf(messages.slice(50, 100), conversation, true) });
  });
  await openGoblin(page);
  const talk = talkOf(page);
  await expect(talk.locator('[data-message="m49"]')).toBeVisible();
  const place = await readNearEnd(talk, page);
  const rows = await captureRows(talk);
  await sendPage(page, pageOf(messages.slice(100, 150), conversation, true));
  expect(await reading(talk)).toEqual(place);
  expect(await currentRows(talk)).toEqual({ sameNodes: true, rows });
  await expect(talk.locator('[data-message="m149"]')).toHaveCount(0);
  await page.getByRole("button", { name: "Jump to latest" }).click();
  await expect(talk.locator('[data-message="m149"]')).toBeVisible();
  const response = page.waitForResponse((value) => new URL(value.url()).searchParams.has("before"));
  await talk.evaluate((element) => { element.scrollTop = 0; });
  await (await response).finished();
  await expect.poll(() => requests.length).toBe(1);
  expect(new URL(requests[0]).searchParams.get("before")).toBe("100");
  await reading(talk);
  await talk.evaluate((element) => { element.scrollTop = 0; });
  await expect(talk.locator('[data-message="m50"]')).toBeVisible();
  await expect(talk.locator('[data-message="m49"]')).toHaveCount(0);
});

test("an older response spanning a disjoint reconnect cannot hide missing history", async ({ page }) => {
  await open(page, snapshot(), "", pageOf(messages.slice(50, 100), conversation, true));
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  const requests: number[] = [];
  await page.route((url) => url.pathname === "/api/messages" && url.searchParams.has("before"), async (route) => {
    const before = Number(new URL(route.request().url()).searchParams.get("before"));
    requests.push(before);
    if (requests.length === 1) await held;
    await route.fulfill({ json: pageOf(messages.slice(before - 50, before), conversation, before > 50) });
  });
  await openGoblin(page);
  const talk = talkOf(page);
  await expect(talk.locator('[data-message="m99"]')).toBeVisible();
  await talk.evaluate((element) => { element.scrollTop = 0; });
  await expect.poll(() => requests.length).toBe(1);
  const place = await reading(talk);
  const rows = await captureRows(talk);
  await sendPage(page, pageOf(messages.slice(150, 200), conversation, true));
  const response = page.waitForResponse((value) => new URL(value.url()).searchParams.get("before") === "50");
  release();
  await (await response).finished();
  expect(await reading(talk)).toEqual(place);
  expect(await currentRows(talk)).toEqual({ sameNodes: true, rows });
  await page.getByRole("button", { name: "Jump to latest" }).click();
  await expect(talk.locator('[data-message="m199"]')).toBeVisible();
  for (const [index, before] of [150, 100, 50].entries()) {
    await reading(talk);
    const older = page.waitForResponse((value) => new URL(value.url()).searchParams.get("before") === String(before));
    await talk.evaluate((element) => { element.scrollTop = 0; });
    await expect.poll(() => requests.length).toBe(index + 2);
    expect(requests[index + 1]).toBe(before);
    await (await older).finished();
    await reading(talk);
  }
  await talk.evaluate((element) => { element.scrollTop = 0; });
  await expect(talk.locator('[data-message="m0"]')).toBeVisible();
  const seen = await talk.evaluate(async (element) => {
    const ids = new Set<string>();
    for (;;) {
      for (let frame = 0; frame < 4; frame++) await new Promise(requestAnimationFrame);
      const rows = [...element.querySelectorAll<HTMLElement>("[data-message]")].map((row) => row.dataset.message!);
      if (new Set(rows).size !== rows.length || rows.some((id, index) => index > 0 && Number(id.slice(1)) <= Number(rows[index - 1].slice(1)))) throw new Error("Messages are duplicated or out of order");
      for (const id of rows) ids.add(id);
      const top = element.scrollTop;
      element.scrollTop += Math.max(1, element.clientHeight / 2);
      if (element.scrollTop === top) break;
    }
    return [...ids].sort((left, right) => Number(left.slice(1)) - Number(right.slice(1)));
  });
  expect(seen).toEqual(Array.from({ length: 200 }, (_, index) => "m" + index));
  expect(requests).toEqual([50, 150, 100, 50]);
});

test("an older response remains valid when the latest page retains its first cursor", async ({ page }) => {
  await open(page, snapshot(), "", pageOf(messages.slice(50, 100), conversation, true));
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  const requests: string[] = [];
  await page.route((url) => url.pathname === "/api/messages" && url.searchParams.has("before"), async (route) => {
    requests.push(route.request().url());
    await held;
    await route.fulfill({ json: pageOf(messages.slice(0, 50), conversation, false) });
  });
  await openGoblin(page);
  const talk = talkOf(page);
  await expect(talk.locator('[data-message="m99"]')).toBeVisible();
  const response = page.waitForResponse((value) => new URL(value.url()).searchParams.get("before") === "50");
  await talk.evaluate((element) => { element.scrollTop = 0; });
  await expect.poll(() => requests.length).toBe(1);
  const place = await reading(talk);
  await sendPage(page, pageOf([...messages.slice(50, 99), reply(99, "Updated answer"), prompt(100)], conversation, true));
  release();
  await (await response).finished();
  await expect.poll(() => talk.evaluate((element) => element.scrollHeight)).toBeGreaterThan(place.height);
  await talk.evaluate((element) => { element.scrollTop = 0; });
  await expect(talk.locator('[data-message="m0"]')).toBeVisible();
  await page.getByRole("button", { name: "Jump to latest" }).click();
  await expect(talk.locator('[data-message="m100"]')).toBeVisible();
  await expect(talk.locator('[data-message="m99"]')).toContainText("Updated answer");
});
