// The browser half of the credential canary proof: it opens a board in a
// headless Edge over the DevTools protocol, finds a credential request's card
// in the Command Center, types a value into one of its fields the way a paste
// lands, presses Save (confirming a replace if the card asks), and waits for
// the row to show it saved. The value comes from a file this script deletes as
// soon as it reads it, travels only over the local DevTools socket, and is
// never printed. Before deleting its browser profile it counts the profile's
// files that hold the value, and it prints one JSON line:
// {"saved":true,"profile_hits":0}. It starts only its own browser and ends it
// by its pid.
//
//   node tests/acceptance/credential_card.mjs --url http://127.0.0.1:PORT --request cred-... --name NAME --canary-file PATH
import { spawn, spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, readdirSync, rmSync, statSync, unlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

const options = {};
for (let i = 2; i < process.argv.length; i++) options[process.argv[i].replace(/^--/, "")] = process.argv[++i];
for (const required of ["url", "request", "name", "canary-file"]) if (!options[required]) throw new Error("--" + required + " is required");
const value = readFileSync(options["canary-file"], "utf8");
unlinkSync(options["canary-file"]);
const browser = options.browser || "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe";
const profile = mkdtempSync(join(tmpdir(), "cfo-credential-card-"));
const edge = spawn(browser, ["--headless=new", "--no-first-run", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--window-size=1600,1100", "about:blank"], { stdio: "ignore" });

// open runs in the board's page: past the first-run page, the Command
// Center's inbox open, and the request's own Answer button pressed, which
// opens its card; the named field focused.
async function open(request, name) {
  const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const skip = [...document.querySelectorAll("a, button")].find((element) => /Open the board without a CFO/.test(element.textContent || ""));
  if (skip) { skip.click(); await pause(2500); }
  const card = () => document.querySelector(`[data-credential-request="${CSS.escape(request)}"]`);
  for (let i = 0; i < 100 && !card(); i++) {
    const answer = [...document.querySelectorAll("button[aria-label^='Answer ']")].find((button) => (button.getAttribute("aria-label") || "").includes(name));
    if (answer) answer.click();
    else if (!document.querySelector("details.command-center-menu[open]")) document.querySelector("summary[aria-label^='Command Center']")?.click();
    await pause(400);
  }
  const field = card()?.querySelector(`input[aria-label="Value for ${CSS.escape(name)}"]`);
  if (!field) throw new Error("the request's card or its field never showed");
  field.focus();
  return document.activeElement === field;
}

// save presses Save, confirms a replace if the card asks, and waits for the
// row to read saved.
async function save(request, name) {
  const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const card = () => document.querySelector(`[data-credential-request="${CSS.escape(request)}"]`);
  const button = (label) => [...(card()?.querySelectorAll("button") || []), ...document.querySelectorAll("dialog[open] button")].find((element) => (element.getAttribute("aria-label") || element.textContent || "").trim() === label);
  button("Save")?.click();
  for (let i = 0; i < 100; i++) {
    button("Replace and save")?.click();
    if (card()?.querySelector(`[data-credential-name="${CSS.escape(name)}"][data-state="saved"]`)) return true;
    await pause(300);
  }
  return false;
}

// hits counts the files under dir whose bytes hold text.
function hits(dir, text) {
  const needle = Buffer.from(text);
  let found = 0;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) { found += hits(path, text); continue; }
    try { if (statSync(path).size > 0 && readFileSync(path).includes(needle)) found++; } catch { /* a file the browser removed */ }
  }
  return found;
}

let result = { saved: false, profile_hits: -1 };
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
  await sleep(5000);
  if (!(await evaluate(open.toString(), options.request, options.name))) throw new Error("the value field did not take focus");
  // insertText lands in the focused field as a paste does, through the
  // browser's own input path; it is never part of an evaluated script.
  await call("Input.insertText", { text: value });
  result.saved = await evaluate(save.toString(), options.request, options.name);
  socket.close();
} finally {
  spawnSync("taskkill.exe", ["/PID", String(edge.pid), "/T", "/F"], { stdio: "ignore" });
  await sleep(1500);
  try { result.profile_hits = hits(profile, value); } catch { result.profile_hits = -1; }
  try { rmSync(profile, { recursive: true, force: true }); } catch {}
}
console.log(JSON.stringify(result));
process.exit(result.saved && result.profile_hits === 0 ? 0 : 1);
