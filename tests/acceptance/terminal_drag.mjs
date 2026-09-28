// Dragging the divider between the board and a terminal panel, timed frame by
// frame. It drives a headless Edge over the DevTools protocol against a running
// board, opens a goblin's terminal from its card (or the CFO's, with --cfo),
// shows it beside the board, drags the divider 400 pixels out and back, and
// prints one JSON line: the frame gaps during the drag and the pane resizes
// the view asked for during the drag and after it. It exits 1 when the view
// resized the pane before the drag
// ended: each resize makes the pane's program redraw its whole screen, so a
// drag resizes it once, on release. It types nothing, starts only its own
// browser and ends it by its pid.
//
//   node tests/acceptance/terminal_drag.mjs --url http://127.0.0.1:PORT --label after
//
// --title picks the goblin by its card's title; --no-gpu draws without the GPU,
// which makes every frame far slower than a desktop browser's.
import { spawn, spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

const options = {};
for (let i = 2; i < process.argv.length; i++) {
  const name = process.argv[i].replace(/^--/, "");
  if (name === "cfo" || name === "no-gpu") options[name] = true;
  else options[name] = process.argv[++i];
}
if (!options.url) throw new Error("--url is required");
const browser = options.browser || "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe";
const profile = mkdtempSync(join(tmpdir(), "cfo-drag-"));
const edge = spawn(browser, ["--headless=new", ...(options["no-gpu"] ? ["--disable-gpu"] : []), "--no-first-run", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--window-size=2560,1440", "about:blank"], { stdio: "ignore" });

// open runs in the board's page: past the first-run page, the Command Center
// closed, the terminal opened and shown beside the board, then every frame and
// every pane resize the view asks for recorded.
async function open(cfo, title) {
  const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const skip = [...document.querySelectorAll("a, button")].find((element) => /Open the board without a CFO/.test(element.textContent || ""));
  if (skip) { skip.click(); await pause(2500); }
  if (document.querySelector("dialog.question-modal[open]")) { document.querySelector(".question-close")?.click(); await pause(800); }
  const button = cfo ? document.querySelector(".cfo-pin .open-terminal") : document.querySelector(title ? `[aria-label^="Open the terminal of ${CSS.escape(title)}"]` : '[aria-label^="Open the terminal of "]');
  if (!button) throw new Error("no terminal button");
  button.click();
  const slot = () => document.querySelector(".deck-slot:not([hidden])");
  for (let i = 0; i < 300 && !slot()?.querySelector(".terminal-state.live"); i++) await pause(100);
  if (!slot()?.querySelector(".terminal-state.live")) throw new Error("the terminal did not go live");
  document.querySelector("button[aria-label='Restore the panel']")?.click();
  await pause(3000);
  const divider = document.querySelector(".pane-divider")?.getBoundingClientRect();
  if (!divider) throw new Error("no divider beside the terminal");
  const record = { frames: [], resizes: [] };
  window.__drag = record;
  const fetchPane = window.fetch;
  window.fetch = (input, init) => {
    if (String(input).includes("/api/terminal/input") && String(init?.body || "").includes("terminal.resize")) record.resizes.push(performance.now());
    return fetchPane(input, init);
  };
  const frame = (at) => { record.frames.push(at); if (!record.stopped) requestAnimationFrame(frame); };
  requestAnimationFrame(frame);
  return [Math.round(divider.left + divider.width / 2), Math.round(divider.top + divider.height / 2)];
}

function summary(start, end) {
  const record = window.__drag;
  record.stopped = true;
  const during = record.frames.filter((at) => at >= start && at <= end);
  const gaps = during.slice(1).map((at, i) => at - during[i]).sort((a, b) => a - b);
  const at = (q) => gaps.length ? +gaps[Math.min(gaps.length - 1, Math.round((gaps.length - 1) * q))].toFixed(1) : null;
  return {
    drag_ms: Math.round(end - start), frames: during.length,
    frame_gap_ms: { p50: at(0.5), p95: at(0.95), max: at(1), over_50: gaps.filter((gap) => gap > 50).length },
    resizes_during_drag: record.resizes.filter((time) => time >= start && time <= end).length,
    resizes_after_release: record.resizes.filter((time) => time > end).length,
  };
}

let result;
try {
  let port = "";
  for (let i = 0; i < 200 && !port; i++) { await sleep(100); try { port = readFileSync(join(profile, "DevToolsActivePort"), "utf8").split("\n")[0].trim(); } catch {} }
  if (!port) throw new Error("the browser did not open its DevTools port");
  const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
  const socket = new WebSocket(targets.find((target) => target.type === "page").webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let next = 0;
  const waiting = new Map();
  socket.onmessage = (event) => { const message = JSON.parse(event.data); waiting.get(message.id)?.(message); waiting.delete(message.id); };
  const call = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++next;
    const timer = setTimeout(() => { waiting.delete(id); reject(new Error(method + " did not answer")); }, 60000);
    waiting.set(id, (message) => { clearTimeout(timer); if (message.error) reject(new Error(method + ": " + message.error.message)); else resolve(message.result); });
    socket.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async (source, ...args) => {
    const answer = await call("Runtime.evaluate", { expression: `(${source})(...${JSON.stringify(args)})`, awaitPromise: true, returnByValue: true });
    if (answer.exceptionDetails) throw new Error(answer.exceptionDetails.exception?.description || answer.exceptionDetails.text);
    return answer.result.value;
  };
  await call("Emulation.setFocusEmulationEnabled", { enabled: true });
  await call("Page.enable");
  await call("Page.navigate", { url: options.url });
  await sleep(6000);
  const [x, y] = await evaluate(open.toString(), !!options.cfo, options.title || "");
  await sleep(500);
  const start = await evaluate("() => performance.now()");
  await call("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", buttons: 1, clickCount: 1 });
  for (let i = 1; i <= 80; i++) {
    const phase = i <= 40 ? i / 40 : (80 - i) / 40;
    await call("Input.dispatchMouseEvent", { type: "mouseMoved", x: Math.round(x - 400 * phase), y, button: "left", buttons: 1 });
    await sleep(16);
  }
  const end = await evaluate("() => performance.now()");
  await call("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", buttons: 0, clickCount: 1 });
  await sleep(2500);
  result = { label: options.label || "", gpu: !options["no-gpu"], ...(await evaluate(summary.toString(), start, end)) };
  socket.close();
} finally {
  spawnSync("taskkill.exe", ["/PID", String(edge.pid), "/T", "/F"], { stdio: "ignore" });
  await sleep(1000);
  try { rmSync(profile, { recursive: true, force: true }); } catch {}
}
console.log(JSON.stringify(result));
process.exit(result.resizes_during_drag > 0 ? 1 : 0);
