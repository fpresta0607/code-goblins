import { expect, test, type Page } from "@playwright/test";

const question = (id: string, status: string) => ({ id, identity: "cfo", text: "Which layout should I keep?", options: ["Kanban", "Stacked"], status, created_at: "2026-09-30T20:00:00Z" });

// The board served with a stubbed supervisor: each new stream connection gets
// whatever snapshot the test holds at that moment.
async function board(page: Page, first: object) {
  let snapshot = first;
  await page.route("**/api/**", async (route) => {
    if (new URL(route.request().url()).pathname === "/api/events") {
      await route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n` });
    } else {
      await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
    }
  });
  await page.goto("/");
  return (next: object) => { snapshot = next; };
}

test("the tab counts what waits on the Overlord and drops the count once nothing does", async ({ page }) => {
  const base = { instance: "tab-fixture", healthy: true, cfo_runs: true, tasks: [] };
  const serve = await board(page, { ...base, revision: 1, questions: [question("layout", "pending"), question("colors", "pending")] });

  await expect.poll(() => page.title()).toBe("(2) Code Goblins");

  serve({ ...base, revision: 2, questions: [question("layout", "succeeded"), question("colors", "succeeded")] });
  await expect.poll(() => page.title(), { timeout: 10_000 }).toBe("Code Goblins");
});

// Chrome and Edge offer to install a page as its own window only when its
// manifest names it, displays standalone and carries 192 and 512 px icons.
test("the board names its icons and an installable manifest in the board's colors", async ({ page }) => {
  await board(page, { instance: "tab-fixture", revision: 1, healthy: true, cfo_runs: true, tasks: [] });

  const head = await page.evaluate(() => ({
    theme: document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')?.content,
    manifest: document.querySelector<HTMLLinkElement>('link[rel="manifest"]')?.href,
    icons: [...document.querySelectorAll<HTMLLinkElement>('link[rel="icon"], link[rel="apple-touch-icon"]')].map((link) => ({ rel: link.rel, href: link.href, sizes: link.getAttribute("sizes") ?? "", type: link.type })),
  }));
  expect(head.theme).toBe("#03050a");
  expect(head.icons.map(({ rel, sizes, type }) => [rel, sizes, type])).toEqual([
    ["icon", "", "image/svg+xml"],
    ["icon", "32x32", "image/png"],
    ["icon", "16x16", "image/png"],
    ["apple-touch-icon", "180x180", ""],
  ]);
  expect(head.manifest).toBeTruthy();
  const manifest = await (await page.request.get(head.manifest!)).json();
  expect(manifest).toMatchObject({ name: "Code Goblins", start_url: "/", display: "standalone", theme_color: head.theme, background_color: head.theme });
  const declared = (manifest.icons as { sizes: string }[]).map((icon) => icon.sizes);
  expect(declared).toEqual(expect.arrayContaining(["192x192", "512x512"]));

  // Every icon loads, and every PNG is exactly the size it claims.
  const icons = [...head.icons.map(({ href, sizes }) => ({ src: href, sizes })), ...manifest.icons.map((icon: { src: string; sizes: string }) => ({ src: new URL(icon.src, head.manifest!).href, sizes: icon.sizes }))];
  for (const icon of icons) {
    const size = await page.evaluate(async (src) => {
      const image = new Image();
      image.src = src;
      await image.decode();
      return `${image.naturalWidth}x${image.naturalHeight}`;
    }, icon.src);
    if (icon.sizes) expect(size, icon.src).toBe(icon.sizes);
  }
});
