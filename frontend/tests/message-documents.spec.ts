import { expect, test } from "./site";
import { at, NAME, open, openGoblin, snapshot } from "./session-board";

const TARGET = "D:/task/output/Retail%20candidate%20(approved).pdf";
const LABEL = "Retail candidate PDF with a long human label that wraps on a narrow phone";
const LINK = `[${LABEL}](${TARGET})`;
const REPORT = `Targeted source repairs pass.\n\n${LINK}: 12,000 sq ft, 40 spaces.\n\nStill unapproved.\n\nExample: \`${LINK}\`\n\n\`\`\`md\n${LINK}\n\`\`\`\n\nhttps://example.com/report\n\n[Unsafe](javascript:alert(1)) <img src=x onerror="alert(1)">`;
const PDF = Buffer.from("%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>\nendobj\nxref\n0 4\n0000000000 65535 f \n0000000009 00000 n \n0000000058 00000 n \n0000000115 00000 n \ntrailer\n<< /Size 4 /Root 1 0 R >>\nstartxref\n186\n%%EOF\n");

for (const width of [390, 1440]) {
  test.describe(`document at ${width}px`, () => {
    test.use({ viewport: { width, height: 1000 }, isMobile: width === 390, hasTouch: width === 390 });
    test(`Modern PDF card loads only on demand at ${width}px and keeps report and code literal`, async ({ page }, testInfo) => {
      await page.addInitScript(() => {
        Object.defineProperty(navigator, "clipboard", { value: { writeText: async (text: string) => localStorage.setItem("copied-report", text) } });
      });
      await page.setViewportSize({ width, height: 1000 });
      await open(page, snapshot({}, { harness: "codex" }), "", { messages: [
        { id: "pdf-report", seq: 0, at, kind: "reply", blocks: [{ kind: "text", text: REPORT }] },
        { id: "turn-end", seq: 1, at, kind: "interrupt", text: "Turn finished" },
      ], more: false });
      const requests: { token: string | undefined; query: URLSearchParams }[] = [];
      await page.route("**/api/messages?*", (route) => {
        requests.push({ token: route.request().headers()["x-cfo-token"], query: new URL(route.request().url()).searchParams });
        return route.fulfill({ status: 403, json: { error: "That PDF is unavailable for this session." } });
      });
      await openGoblin(page);
      const log = page.getByRole("log", { name: "Messages of " + NAME, exact: true });
      const document = log.getByRole("group", { name: LABEL, exact: true });

      await expect(document).toBeVisible();
      await expect(document).not.toContainText("D:/task");
      await expect(log).toContainText(": 12,000 sq ft, 40 spaces.");
      await expect(log).toContainText("Still unapproved.");
      await expect(log.locator("code").first()).toHaveText(LINK);
      await expect(log.locator(".message-code")).toContainText(LINK);
      await expect(log.getByRole("link", { name: "https://example.com/report" })).toHaveAttribute("href", "https://example.com/report");
      await expect(log.locator(".message-words img, .message-words script, a[href^='javascript:']")).toHaveCount(0);
      await log.locator(".message-reply-actions").getByRole("button", { name: "Copy", exact: true }).click();
      expect(await page.evaluate(() => localStorage.getItem("copied-report"))).toBe(REPORT);
      expect(requests).toHaveLength(0);
      expect(await document.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
      expect(await log.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
      await testInfo.attach(`pdf-card-${width}.png`, { body: await document.screenshot(), contentType: "image/png" });

      const action = document.getByRole("button", { name: "Open PDF", exact: true });
      await action.focus();
      await page.keyboard.press("Enter");
      await expect(document.getByRole("status")).toHaveText("That PDF is unavailable for this session.");
      expect(requests).toHaveLength(1);
      expect(requests[0].token).toBe("fixture");
      expect(requests[0].query.get("token")).toBeNull();
      expect(requests[0].query.get("document")).toBe(TARGET);
      expect(requests[0].query.get("task")).toBe("cg-touch");
      expect(requests[0].query.get("generation")).toBe("cg-touch-1");
      await expect(action).toBeEnabled();
      await expect(document.locator("a")).toHaveCount(0);
    });

    test("a fetched PDF opens through a token-free action and keeps a download fallback", async ({ page, request }) => {
      const proof = process.env.BOARD_DOCUMENT_PROOF;
      const messages: unknown = proof ? await (await request.get(proof + "/api/messages?task=cg-touch&generation=cg-touch-1&token=fixture")).json()
        : { messages: [{ id: "pdf-report", seq: 0, at, kind: "reply", blocks: [{ kind: "text", text: `Before ${LINK}: after.` }] }], more: false };
      if (typeof messages !== "object" || messages === null) throw new Error("The disposable conversation is not a message page");
      await open(page, snapshot({}, { harness: "codex" }), "", messages);
      const reads: string[] = [];
      await page.route("**/api/messages?*", async (route) => {
        const url = new URL(route.request().url());
        expect(url.searchParams.get("token")).toBeNull();
        expect(route.request().headers()["x-cfo-token"]).toBe("fixture");
        reads.push(url.searchParams.get("document") ?? "");
        if (proof) return route.fulfill({ response: await route.fetch({ url: proof + url.pathname + url.search, headers: { "X-CFO-Token": "fixture" } }) });
        return route.fulfill({ contentType: "application/pdf", body: PDF });
      });
      await openGoblin(page);
      const document = page.getByRole("group", { name: LABEL, exact: true });
      expect(reads).toHaveLength(0);
      const opened = page.context().waitForEvent("page");
      await document.getByRole("button", { name: "Open PDF", exact: true }).click();
      const popup = await opened;
      await expect(document.getByRole("link", { name: "Open PDF", exact: true })).toHaveAttribute("href", /^blob:/);
      expect(reads).toHaveLength(1);
      expect(popup.url()).not.toContain("token=");
      await popup.close();
      const content = await document.getByRole("link", { name: "Open PDF", exact: true }).evaluate(async (element) => {
        const response = await fetch((element as HTMLAnchorElement).href);
        return { type: response.headers.get("Content-Type"), text: await response.text() };
      });
      expect(content.type).toBe("application/pdf");
      expect(content.text).toContain("%PDF-1.4");
      expect(content.text).toContain("%%EOF");
      const downloaded = page.waitForEvent("download");
      await document.getByRole("link", { name: "Download PDF", exact: true }).click();
      const download = await downloaded;
      expect(download.suggestedFilename()).toBe("Retail candidate (approved).pdf");
      expect(await download.failure()).toBeNull();
      expect(reads).toHaveLength(1);
    });
  });
}

test("a browser that cannot open a PDF keeps its safe download action", async ({ page }) => {
  await page.addInitScript(() => { window.open = () => null; });
  await open(page, snapshot({}, { harness: "codex" }), "", { messages: [{ id: "pdf-report", seq: 0, at, kind: "reply", blocks: [{ kind: "text", text: LINK }] }], more: false });
  await page.route("**/api/messages?*", (route) => route.fulfill({ contentType: "application/pdf", body: PDF }));
  await openGoblin(page);
  const document = page.getByRole("group", { name: LABEL, exact: true });

  await document.getByRole("button", { name: "Open PDF", exact: true }).click();

  await expect(document.getByRole("link", { name: "Open PDF", exact: true })).toHaveAttribute("href", /^blob:/);
  const downloaded = page.waitForEvent("download");
  await document.getByRole("link", { name: "Download PDF", exact: true }).click();
  expect(await (await downloaded).failure()).toBeNull();
});

test("a PDF link in thinking stays plain text with no card", async ({ page }) => {
  await open(page, snapshot({}, { harness: "codex" }), "", { messages: [{ id: "pdf-thought", seq: 0, at, kind: "reply", blocks: [{ kind: "thinking", text: `Check ${LINK} first.` }] }], more: false });
  await openGoblin(page);
  const log = page.getByRole("log", { name: "Messages of " + NAME, exact: true });

  await log.locator(".message-thinking summary").click();

  await expect(log.locator(".message-thinking .message-words")).toHaveText(`Check ${LINK} first.`);
  await expect(log.getByRole("group", { name: LABEL, exact: true })).toHaveCount(0);
});

test("Open PDF pressed from the keyboard moves focus to the ready Open PDF link", async ({ page }) => {
  await page.addInitScript(() => { window.open = () => null; });
  await open(page, snapshot({}, { harness: "codex" }), "", { messages: [{ id: "pdf-report", seq: 0, at, kind: "reply", blocks: [{ kind: "text", text: LINK }] }], more: false });
  await page.route("**/api/messages?*", (route) => route.fulfill({ contentType: "application/pdf", body: PDF }));
  await openGoblin(page);
  const document = page.getByRole("group", { name: LABEL, exact: true });
  await document.getByRole("button", { name: "Open PDF", exact: true }).focus();

  await page.keyboard.press("Enter");

  await expect(document.getByRole("link", { name: "Open PDF", exact: true })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(document.getByRole("link", { name: "Download PDF", exact: true })).toBeFocused();
});

test("a PDF whose answer never starts says it timed out rather than refused", async ({ page }) => {
  await page.clock.install();
  await open(page, snapshot({}, { harness: "codex" }), "", { messages: [{ id: "pdf-report", seq: 0, at, kind: "reply", blocks: [{ kind: "text", text: LINK }] }], more: false });
  await page.route("**/api/messages?*", () => {});
  await openGoblin(page);
  const document = page.getByRole("group", { name: LABEL, exact: true });
  await document.getByRole("button", { name: "Open PDF", exact: true }).click();
  await expect(document.getByRole("button", { name: "Loading PDF...", exact: true })).toBeDisabled();

  await page.clock.runFor(30_000);

  await expect(document.getByRole("status")).toHaveText("Opening that PDF timed out. Try again.");
  await expect(document.getByRole("button", { name: "Open PDF", exact: true })).toBeEnabled();
});

test("a PDF still downloading after its answer starts is not cut off", async ({ page }) => {
  await page.clock.install();
  await page.addInitScript((pdf) => {
    window.open = () => null;
    const send = window.fetch;
    window.fetch = (input, init) => {
      if (!String(input).includes("document=")) return send(input, init);
      const signal = init?.signal;
      const body = new ReadableStream<Uint8Array>({ start(controller) {
        signal?.addEventListener("abort", () => controller.error(signal.reason));
        window.addEventListener("fixture-pdf-finished", () => { controller.enqueue(new TextEncoder().encode(pdf)); controller.close(); }, { once: true });
      } });
      return Promise.resolve(new Response(body, { headers: { "Content-Type": "application/pdf" } }));
    };
  }, PDF.toString());
  await open(page, snapshot({}, { harness: "codex" }), "", { messages: [{ id: "pdf-report", seq: 0, at, kind: "reply", blocks: [{ kind: "text", text: LINK }] }], more: false });
  await openGoblin(page);
  const document = page.getByRole("group", { name: LABEL, exact: true });
  await document.getByRole("button", { name: "Open PDF", exact: true }).click();
  await expect(document.getByRole("button", { name: "Loading PDF...", exact: true })).toBeDisabled();

  await page.clock.runFor(60_000);
  await page.evaluate(() => window.dispatchEvent(new Event("fixture-pdf-finished")));

  await expect(document.getByRole("link", { name: "Open PDF", exact: true })).toHaveAttribute("href", /^blob:/);
  await expect(document.getByRole("status")).toHaveCount(0);
});
