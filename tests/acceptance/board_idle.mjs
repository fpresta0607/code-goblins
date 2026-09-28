// What an open board costs while nothing happens on it. It drives a headless
// Edge over the DevTools protocol against a running board, lets it settle,
// then counts for a while the style recalculations, layouts and main-thread
// time the page spends on its own and lists every animation that never ends.
// It prints one JSON line and exits 1 when the idle page keeps restyling
// itself or runs an endless animation, since either keeps the browser and its
// GPU busy for as long as the board is open. It starts only its own browser
// and ends it by its pid.
//
//   node tests/acceptance/board_idle.mjs --url http://127.0.0.1:PORT --seconds 10 --label after
//
// --view Orchestration measures that view instead of the Board, and
// --max-recalcs sets the style recalculations a second an idle page may make
// (default 30: a short transition after a snapshot, such as the memory meter's,
// restyles a few frames; an endless animation restyles on every frame, 60 or
// more a second).
import { spawn, spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

const options = {};
for (let i = 2; i < process.argv.length; i += 2) options[process.argv[i].replace(/^--/, "")] = process.argv[i + 1];
if (!options.url) throw new Error("--url is required");
const seconds = Number(options.seconds || 10);
const maxRecalcs = Number(options["max-recalcs"] || 30);
const browser = options.browser || "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe";
const profile = mkdtempSync(join(tmpdir(), "cfo-idle-"));
const edge = spawn(browser, ["--headless=new", "--no-first-run", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--window-size=2560,1440", "about:blank"], { stdio: "ignore" });

// settle runs in the board's page: past the first-run page, the Command
// Center closed, and the view asked for shown.
async function settle(view) {
  const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const skip = [...document.querySelectorAll("a, button")].find((element) => /Open the board without a CFO/.test(element.textContent || ""));
  if (skip) { skip.click(); await pause(2500); }
  if (document.querySelector("dialog.question-modal[open]")) { document.querySelector(".question-close")?.click(); await pause(800); }
  if (view) {
    const tab = [...document.querySelectorAll("button, a, [role=tab]")].find((element) => (element.textContent || "").trim() === view);
    if (!tab) throw new Error("no " + view + " view");
    tab.click();
    await pause(2500);
  }
  return document.querySelectorAll(".task-card, .flow-node").length;
}

// endless lists the animations running with no end, by name and element.
function endless() {
  const named = (element) => element ? element.tagName.toLowerCase() + "." + String(element.className?.baseVal ?? element.className ?? "").trim().split(/\s+/).join(".") : "";
  return document.getAnimations()
    .filter((animation) => animation.playState === "running" && animation.effect?.getTiming().iterations === Infinity)
    .map((animation) => (animation.animationName || "animation") + " on " + named(animation.effect?.target));
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
    const timer = setTimeout(() => { waiting.delete(id); reject(new Error(method + " did not answer")); }, 30000);
    waiting.set(id, (message) => { clearTimeout(timer); if (message.error) reject(new Error(method + ": " + message.error.message)); else resolve(message.result); });
    socket.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async (source, ...args) => {
    const answer = await call("Runtime.evaluate", { expression: `(${source})(...${JSON.stringify(args)})`, awaitPromise: true, returnByValue: true });
    if (answer.exceptionDetails) throw new Error(answer.exceptionDetails.exception?.description || answer.exceptionDetails.text);
    return answer.result.value;
  };
  await call("Emulation.setFocusEmulationEnabled", { enabled: true });
  await call("Performance.enable");
  await call("Page.enable");
  await call("Page.navigate", { url: options.url });
  await sleep(6000);
  const cards = await evaluate(settle.toString(), options.view || "");
  await sleep(2000);
  const metrics = async () => Object.fromEntries((await call("Performance.getMetrics")).metrics.map((metric) => [metric.name, metric.value]));
  const before = await metrics();
  await sleep(seconds * 1000);
  const after = await metrics();
  const perSecond = (name, scale = 1) => +((after[name] - before[name]) / seconds * scale).toFixed(1);
  const animations = await evaluate(endless.toString());
  result = {
    label: options.label || "", view: options.view || "Board", cards, seconds,
    style_recalcs_per_s: perSecond("RecalcStyleCount"), layouts_per_s: perSecond("LayoutCount"),
    main_thread_ms_per_s: perSecond("TaskDuration", 1000), script_ms_per_s: perSecond("ScriptDuration", 1000),
    heap_mb: +(after.JSHeapUsedSize / 2 ** 20).toFixed(1), endless_animations: animations,
  };
  socket.close();
} finally {
  spawnSync("taskkill.exe", ["/PID", String(edge.pid), "/T", "/F"], { stdio: "ignore" });
  await sleep(1000);
  try { rmSync(profile, { recursive: true, force: true }); } catch {}
}
console.log(JSON.stringify(result));
process.exit(result.endless_animations.length > 0 || result.style_recalcs_per_s > maxRecalcs ? 1 : 0);
