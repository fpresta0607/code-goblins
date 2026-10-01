// The browser half of the credential canary proof: it opens a board in a
// headless Edge over the DevTools protocol, finds a credential request's card
// in the Command Center, clicks into one of its fields as a mouse does, enters
// a value the way a paste lands, clicks Save (confirming a replace if the card
// asks), and waits for the row to show it saved. It then fills a plain sign-in
// form of its own with values it makes up, which a browser keeps and offers to
// save: finding that shows the proof can see a browser do either. The value
// comes from a file this script deletes as soon as it reads it, travels only
// over the local DevTools socket, and is never printed.
//
// A fresh profile folder is not isolation: Edge signs a new profile in to the
// Windows account by itself and syncs it, which pulls that account's data into
// the folder and sends up what is typed. So the browser starts with sync and
// automatic sign-in switched off, on a profile that disallows sign-in, and the
// script reads the browser's own sign-in state before it opens the board: an
// account, or a state it cannot read, ends the run with nothing typed.
//
// It starts only its own browser, closes it, ends whatever is left running
// from its profile by its pid, then reads every file of the profile and prints
// one JSON line with what it found: whether the browser was signed out before
// anything was typed and at the end, whether the row saved, whether the
// browser stopped, which profile files hold the value, how many it read and
// how many it could not, which hold the plain form's user name, how many saved
// form entries the proof did not type, how many logins are stored for the
// board, and whether the profile was removed.
//
// --visible yes opens a window instead, because only a visible browser shows
// its offer to save a password. The line then also says how many window
// elements were read and what the browser's windows said about saving a
// password after the card's save and after the plain form.
//
//   node tests/acceptance/credential_card.mjs --url http://127.0.0.1:PORT --request cred-... --name NAME --canary-file PATH [--browser PATH] [--visible yes]
import { spawn, spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, unlinkSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

const options = {};
for (let i = 2; i < process.argv.length; i++) options[process.argv[i].replace(/^--/, "")] = process.argv[++i];
for (const required of ["url", "request", "name", "canary-file"]) if (!options[required]) throw new Error("--" + required + " is required");
const value = readFileSync(options["canary-file"], "utf8");
unlinkSync(options["canary-file"]);
const browser = options.browser || "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe";
const visible = options.visible === "yes";
const profile = mkdtempSync(join(tmpdir(), "cfo-credential-card-"));
mkdirSync(join(profile, "Default"));
writeFileSync(join(profile, "Default", "Preferences"), JSON.stringify({ signin: { allowed: false, allowed_on_next_startup: false } }));
const started = Date.now();
const seconds = () => Math.round((Date.now() - started) / 1000);
const edge = spawn(browser, [...(visible ? ["--window-position=40,40", "--window-size=1100,820"] : ["--headless=new", "--window-size=1600,1100"]), "--no-first-run", "--no-default-browser-check", "--disable-sync", "--disable-features=msImplicitSignin", "--disable-backgrounding-occluded-windows", "--disable-renderer-backgrounding", "--disable-background-timer-throttling", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "about:blank"], { stdio: "ignore" });
// When the program this script started ended, which for a visible browser is
// when its window closed.
let exited = -1;
edge.on("exit", () => { exited = seconds(); });

// The plain sign-in form, on an origin of its own. Nothing in its pages says
// "password", so that word in the browser's windows is the browser's own.
const plainUser = "plainuser" + randomBytes(8).toString("hex");
const plain = createServer((request, response) => {
  request.resume();
  if (request.method === "POST") { response.writeHead(303, { Location: "/in" }).end(); return; }
  response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" }).end(request.url === "/in"
    ? "<!doctype html><title>Plain form</title><p>Signed in.</p>"
    : '<!doctype html><title>Plain form</title><form method="post" action="/"><input name="username" autocomplete="username" aria-label="User"><input name="password" type="password" autocomplete="current-password" aria-label="Secret"><button>Go</button></form>');
});
await new Promise((resolve) => plain.listen(0, "127.0.0.1", resolve));

// open runs in the board's page: past the first-run page, the Command
// Center's inbox open, and the request's own Answer button pressed, which
// opens its card. It answers whether the named field showed.
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
  return Boolean(card()?.querySelector(`input[aria-label="Value for ${CSS.escape(name)}"]`));
}

// middle runs in a page: the middle of the element a selector names, scrolled
// into view, or null when there is none.
function middle(selector) {
  const element = document.querySelector(selector);
  if (!element) return null;
  element.scrollIntoView({ block: "center", inline: "center", behavior: "instant" });
  const box = element.getBoundingClientRect();
  return { x: box.left + box.width / 2, y: box.top + box.height / 2 };
}

const pick = (object, path) => path.split(".").reduce((found, key) => (found == null ? undefined : found[key]), object);
// The browser's live preferences page wraps each value with where it came from.
const unwrap = (node) => node && typeof node === "object" && !Array.isArray(node)
  ? (Array.isArray(node.metadata) && "value" in node ? node.value : Object.fromEntries(Object.entries(node).map(([key, child]) => [key, unwrap(child)])))
  : node;
// account says whether a browser's preferences show an account or sync set up.
const account = (prefs) => (Array.isArray(prefs.account_info) ? prefs.account_info.length > 0 : Boolean(prefs.account_info))
  || pick(prefs, "google.services.consented_to_sync") === true || pick(prefs, "sync.has_setup_completed") === true
  || Boolean(pick(prefs, "google.services.last_username")) || Boolean(pick(prefs, "google.services.account_id"));

// search reads every file under dir and answers the files whose bytes hold
// text, by their path under the profile, how many it read, and how many it
// could not read: a search that skipped a file has not shown the file is
// clean. A browser writes text as UTF-8 in some of its files and as UTF-16 in
// others, so it looks for both.
function search(dir, text, seen = { hits: [], files: 0, unreadable: 0 }) {
  const needles = [Buffer.from(text), Buffer.from(text, "utf16le")];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) { search(path, text, seen); continue; }
    try {
      const bytes = readFileSync(path);
      if (needles.some((needle) => bytes.includes(needle))) seen.hits.push(relative(profile, path));
      seen.files++;
    } catch { seen.unreadable++; }
  }
  return seen;
}

// profileProcesses are the pids of every process still running from this
// script's own browser profile. Edge hands a headless run to processes the
// one this script started does not parent, so they are found by the
// profile's path. A process that was ended stays listed while Windows tears
// it down, so one that has exited is not counted.
function profileProcesses() {
  const listed = spawnSync("powershell.exe", ["-NoProfile", "-Command", `Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -like '*${profile}*' -and $_.ProcessId -ne $PID } | ForEach-Object { $live = Get-Process -Id $_.ProcessId -ErrorAction SilentlyContinue; if ($live -and -not $live.HasExited) { $_.ProcessId } }`], { encoding: "utf8" });
  return (listed.stdout || "").split(/\s+/).filter(Boolean).map(Number);
}

// passwordNames reads the visible browser's own windows through Windows UI
// Automation: how many elements it read, and the names of those on screen
// that mention a password. A name that holds the value is never passed on.
function passwordNames() {
  const script = [
    "[Console]::OutputEncoding = [Text.Encoding]::UTF8",
    "Add-Type -AssemblyName UIAutomationClient, UIAutomationTypes",
    `$pids = @(${profileProcesses().join(",")})`,
    "$all = [System.Windows.Automation.Condition]::TrueCondition",
    "$examined = 0",
    "$names = New-Object System.Collections.Generic.List[string]",
    "foreach ($id in $pids) { foreach ($window in [System.Windows.Automation.AutomationElement]::RootElement.FindAll([System.Windows.Automation.TreeScope]::Children, (New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ProcessIdProperty, [int]$id)))) { try { foreach ($element in @($window) + @($window.FindAll([System.Windows.Automation.TreeScope]::Descendants, $all))) { try { $name = $element.Current.Name; $examined++; if ($name -match 'password' -and -not $element.Current.IsOffscreen) { $names.Add($name) } } catch { } } } catch { } } }",
    "ConvertTo-Json -Compress -InputObject @{ examined = $examined; names = @($names | Select-Object -Unique) }",
  ].join("; ");
  const read = spawnSync("powershell.exe", ["-NoProfile", "-Command", script], { encoding: "utf8", timeout: 120000 });
  try {
    const seen = JSON.parse(read.stdout);
    return { examined: Number(seen.examined), names: [].concat(seen.names || []).map((name) => String(name).includes(value) ? "[a name holding the value]" : String(name).slice(0, 120)) };
  } catch { return { examined: 0, names: [] }; }
}
// offer says whether a name that mentions a password is an offer to keep
// one: the bubble a browser opens, or the key in its address bar.
const offer = (name) => /\b(sav(e|ed|ing)|updat(e|ed|ing)|remember)/i.test(name);

// rows answers the count one query finds in one of the profile's stores: -1
// when the browser made no such store, -2 when it could not be read. A closed
// browser can leave a store mid-write, so a copy is opened together with its
// journal, which lets SQLite finish or undo that write.
async function rows(file, sql, ...values) {
  const store = join(profile, "Default", file);
  if (!existsSync(store)) return -1;
  try {
    const copy = join(profile, "copy-of-" + file.replaceAll(" ", "-"));
    copyFileSync(store, copy);
    for (const journal of ["-journal", "-wal"]) if (existsSync(store + journal)) copyFileSync(store + journal, copy + journal);
    const { DatabaseSync } = await import("node:sqlite");
    const database = new DatabaseSync(copy);
    try { return Number(database.prepare(sql).get(...values).stored); } finally { database.close(); }
  } catch { return -2; }
}

// connect opens a DevTools socket to one tab. A tab that closes fails what
// was asked of it at once, saying when.
async function connect(url) {
  const socket = new WebSocket(url);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let next = 0;
  const waiting = new Map();
  socket.onmessage = (event) => { const message = JSON.parse(event.data); waiting.get(message.id)?.(message); waiting.delete(message.id); };
  socket.onclose = () => {
    for (const [id, settle] of waiting) settle({ id, error: { message: "the browser closed the page " + seconds() + " s after it started" } });
    waiting.clear();
  };
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
  return { call, evaluate, close: () => socket.close() };
}

// closeBrowser asks the browser to close as it does when its last window is
// closed, and waits for it to go: it then writes what it keeps to its
// profile first, where a browser ended mid-write could lose the very file
// that would have held the value.
async function closeBrowser(port) {
  try {
    const { webSocketDebuggerUrl } = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json();
    const socket = new WebSocket(webSocketDebuggerUrl);
    await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
    socket.send(JSON.stringify({ id: 1, method: "Browser.close" }));
    for (let i = 0; i < 30 && profileProcesses().length; i++) await sleep(500);
  } catch { /* stopBrowser ends whatever is still running */ }
}

// stopBrowser ends whatever the browser this script started left running,
// and everything else running from its profile, each by its pid, and waits
// until none is left.
async function stopBrowser() {
  spawnSync("taskkill.exe", ["/PID", String(edge.pid), "/T", "/F"], { stdio: "ignore" });
  for (let i = 0; i < 30; i++) {
    const pids = profileProcesses();
    if (!pids.length) return true;
    for (const pid of pids) spawnSync("taskkill.exe", ["/PID", String(pid), "/T", "/F"], { stdio: "ignore" });
    await sleep(500);
  }
  return false;
}

const result = { signed_out_before_typing: false, saved: false, browser_stopped: false, profile_hits: -1, profile_hit_files: [], profile_files: 0, profile_unreadable: -1, plain_form_files: [], signed_out_at_the_end: false, foreign_form_entries: -2, login_rows: -2, profile_removed: false };
if (visible) Object.assign(result, { ui_examined: 0, password_offers: [], password_named: [], plain_form_offers: [] });
let port = "";
// Whether the browser may still be asked to close: one that shows an account
// is ended at once instead.
let sealed = true;
try {
  for (let i = 0; i < 200 && !port; i++) { await sleep(100); try { port = readFileSync(join(profile, "DevToolsActivePort"), "utf8").split("\n")[0].trim(); } catch {} }
  if (!port) throw new Error("the browser did not open its DevTools port");
  // A visible Edge also runs pages of its own, such as its sidebar, which its
  // list of pages can name before the window's tab, so the driver opens a tab
  // of its own and drives that.
  const tab = await (await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: "PUT" })).json();
  const { call, evaluate, close } = await connect(tab.webSocketDebuggerUrl);
  if (visible) await call("Page.bringToFront");
  // press clicks the middle of what a selector names as a mouse does, so the
  // page and the browser see a person at work rather than a script.
  const press = async (selector) => {
    const at = await evaluate(middle.toString(), selector);
    if (!at) return false;
    for (const type of ["mousePressed", "mouseReleased"]) await call("Input.dispatchMouseEvent", { type, x: at.x, y: at.y, button: "left", clickCount: 1 });
    return true;
  };
  // enter clicks into a field and puts text in it the way a paste lands,
  // through the browser's own input path; the text is never part of an
  // evaluated script.
  const enter = async (selector, text) => {
    let focused = false;
    for (let i = 0; i < 5 && !focused; i++) {
      if (i) await sleep(400);
      await press(selector);
      focused = await evaluate("(selector) => document.activeElement === document.querySelector(selector)", selector);
    }
    if (!focused) throw new Error("a field did not take the click");
    await call("Input.insertText", { text });
  };
  const fresh = (names, before) => [...new Set(names)].filter((name) => !before.includes(name));

  // A browser that signs a profile in does so in its first seconds, so its
  // own live preferences are read after those, before the board is opened:
  // an account, or preferences that cannot be read, ends the run here.
  await call("Page.enable");
  await sleep(Math.max(0, 12000 - (Date.now() - started)));
  await call("Page.navigate", { url: "chrome://prefs-internals/" });
  await sleep(1500);
  let prefs;
  try { prefs = unwrap(JSON.parse(await evaluate("() => document.body.innerText"))); } catch { throw new Error("the browser's sign-in state could not be read; nothing was typed"); }
  if (account(prefs)) { sealed = false; throw new Error("the browser shows an account; nothing was typed"); }
  result.signed_out_before_typing = true;
  await call("Emulation.setFocusEmulationEnabled", { enabled: true });
  if (visible) await call("Page.bringToFront");
  await call("Page.navigate", { url: options.url });
  await sleep(5000);
  if (!(await evaluate(open.toString(), options.request, options.name))) throw new Error("the request's card or its field never showed");
  await sleep(1000);
  const before = visible ? passwordNames() : { examined: 0, names: [] };
  const card = `[data-credential-request="${options.request}"]`;
  await enter(`${card} input[aria-label="Value for ${options.name}"]`, value);
  for (let i = 0; i < 50 && !(await press(`${card} .credential-save:enabled`)); i++) await sleep(100);
  let confirmed = false;
  for (let i = 0; i < 100 && !result.saved; i++) {
    if (!confirmed) confirmed = await press(`${card} .credential-replace-confirm`);
    result.saved = await evaluate("(selector) => Boolean(document.querySelector(selector))", `${card} [data-credential-name="${options.name}"][data-state="saved"]`);
    if (!result.saved) await sleep(300);
  }
  if (visible) {
    // A browser decides whether to offer saving once the fields are gone, and
    // again when the page loads anew.
    await sleep(6000);
    const saved = passwordNames();
    await call("Page.reload");
    await sleep(6000);
    const reloaded = passwordNames();
    const named = fresh([...saved.names, ...reloaded.names], before.names);
    result.ui_examined = Math.min(before.examined, saved.examined, reloaded.examined);
    result.password_offers = named.filter(offer);
    result.password_named = named.filter((name) => !offer(name));
  }

  await call("Page.navigate", { url: `http://127.0.0.1:${plain.address().port}/` });
  await sleep(2000);
  await enter('input[name="username"]', plainUser);
  await enter('input[name="password"]', "plain" + randomBytes(12).toString("hex"));
  await press("button");
  await sleep(6000);
  if (visible) result.plain_form_offers = fresh(passwordNames().names, before.names).filter(offer);
  close();
} catch (error) {
  result.error = String(error?.message || error).replaceAll(value, "[the value]");
  // Which pages the browser had open, and when the program this script
  // started ended, say where a run that stopped early stood.
  try { result.pages = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()).filter((target) => target.type === "page").map((target) => target.url.split("?")[0]); } catch { result.pages = ["the browser no longer answered"]; }
  result.browser_exited_at = exited;
} finally {
  plain.close();
  plain.closeAllConnections();
  if (port && sealed) await closeBrowser(port);
  result.browser_stopped = await stopBrowser();
  // A process that has ended still holds its files for a while, so the
  // search repeats until it has read every file, for up to a minute.
  for (let i = 0; i < 120; i++) {
    try {
      const seen = search(profile, value);
      result.profile_hits = seen.hits.length; result.profile_hit_files = seen.hits; result.profile_files = seen.files; result.profile_unreadable = seen.unreadable;
    } catch { /* the counts stay as a search that did not run */ }
    if (result.profile_unreadable === 0) break;
    await sleep(500);
  }
  try { result.plain_form_files = search(profile, plainUser).hits; } catch { /* stays empty, which fails the check */ }
  try { result.signed_out_at_the_end = !account(JSON.parse(readFileSync(join(profile, "Default", "Preferences"), "utf8"))); } catch { /* stays false, which fails the check */ }
  result.foreign_form_entries = await rows("Web Data", "select count(*) as stored from autofill where value <> ?", plainUser);
  result.login_rows = await rows("Login Data", "select count(*) as stored from logins where origin_url like ?", new URL(options.url).origin + "%");
  // Windows holds a closed browser's profile for a minute or two.
  for (let i = 0; i < 180 && existsSync(profile); i++) {
    try { rmSync(profile, { recursive: true, force: true }); } catch { await sleep(1000); }
  }
  result.profile_removed = !existsSync(profile);
  if (!result.profile_removed) result.profile = profile;
}
console.log(JSON.stringify(result));
const held = result.signed_out_before_typing && result.saved && result.browser_stopped && result.profile_hits === 0 && result.profile_unreadable === 0 && result.profile_files > 0 && result.plain_form_files.length > 0
  && result.signed_out_at_the_end && result.foreign_form_entries === 0 && [0, -1].includes(result.login_rows) && result.profile_removed
  && (!visible || (result.ui_examined > 0 && result.password_offers.length === 0 && result.plain_form_offers.length > 0));
process.exit(held ? 0 : 1);
