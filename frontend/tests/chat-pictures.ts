import { readFileSync } from "node:fs";
import { expect, type Page } from "./site";
import { at, GOBLIN, NAME, open, openGoblin } from "./session-board";

// A conversation that holds pictures in every place a harness records them
// (#2305): one he pasted, which the transcript holds, and one the message box
// kept and his words name by path; a run of calls, two reads and a browser
// screenshot around a command, that folds into one row; one call whose three
// pictures are one that reads back, one that is gone and one whose bytes are
// no picture; one call with more pictures than its row shows; and a prompt
// whose kept picture is gone. Every picture is one of the board's own
// drawings, read back by the route its row names.
const KEPT = "C:\\goblin\\tasktmp\\cg-touch\\composer-files\\pasted-1.png";
const KEPT_GONE = "C:\\goblin\\tasktmp\\cg-touch\\composer-files\\pasted-2.png";
const shot = (id: string) => ({ id, media_type: "image/png", bytes: 1024 });
const call = (id: string, tool: string, subject: string, ...pictures: string[]) => ({ kind: "call", call_id: id, tool, subject, state: "done", result: "", pictures: pictures.map(shot) });
export const PICTURED = { conversation: "fixture-conversation", more: false, messages: [
  { id: "m0", seq: 0, at, kind: "prompt", source: "overlord", text: "what is wrong here?\n" + KEPT, pictures: [shot("sent")] },
  { id: "m1", seq: 1, at, kind: "reply", blocks: [
    { kind: "text", text: "Reading the screenshots." },
    call("read-0", "Read", "shots/home.png", "home"), call("bash-0", "Bash", "npm test"), call("read-1", "Read", "shots/team.png", "team"),
    call("shot-0", "mcp__chrome__take_screenshot", "", "page"),
    { kind: "text", text: "The button overlaps the title." },
  ] },
  { id: "m2", seq: 2, at, kind: "prompt", source: "overlord", text: "And the older ones?" },
  { id: "m3", seq: 3, at, kind: "reply", blocks: [call("view-0", "view_image", "shots/old", "babies", "gone", "noise"), { kind: "text", text: "One of them is gone." }] },
  { id: "m4", seq: 4, at, kind: "reply", blocks: [call("bash-1", "Bash", "npx playwright test", "s1", "s2", "s3", "s4", "s5"), { kind: "text", text: "Five screenshots came back." }] },
  { id: "m5", seq: 5, at, kind: "prompt", source: "overlord", text: "and this one?\n" + KEPT_GONE },
] };
// Every picture the conversation's rows show, in the order they show them.
export const SET = ["sent", KEPT, "home", "team", "page", "babies", "gone", "noise", "s1", "s2", "s3", "s4", "s5", KEPT_GONE];
export const placeOf = (picture: string) => SET.indexOf(picture) + 1 + " of " + SET.length;

const FILES: Record<string, string> = {
  sent: "goblin-app.png", [KEPT]: "goblin-workshop.webp", home: "goblin-personas.webp", team: "goblin-specialists.webp", page: "goblin-specialists-2.webp", babies: "goblin-babies.png",
  s1: "goblin-workshop.webp", s2: "goblin-personas.webp", s3: "goblin-app.png", s4: "goblin-specialists.webp", s5: "goblin-babies.png",
};

// servePictures plays the supervisor's two picture routes, each answering
// only to the board's token: a transcript's picture by its id and its
// conversation, and a kept one by its path and the goblin's task. It returns
// each picture asked for, by id or path, in the order the page asked.
export async function servePictures(page: Page): Promise<string[]> {
  const asked: string[] = [];
  const answer = (name: string) => {
    asked.push(name);
    if (name === "noise") return { body: "no picture at all", contentType: "image/png" };
    const file = FILES[name];
    return file ? { body: readFileSync("public/assets/" + file), contentType: file.endsWith(".png") ? "image/png" : "image/webp" } : { status: 404, json: { error: "No such picture is kept" } };
  };
  await page.route((url) => url.pathname === "/api/messages" && url.searchParams.has("picture"), (route) => {
    const query = new URL(route.request().url()).searchParams;
    const isOurs = query.get("token") === "fixture" && query.get("conversation") === PICTURED.conversation && query.get("task") === GOBLIN.id;
    return route.fulfill(isOurs ? answer(query.get("picture") ?? "") : { status: 403, json: { error: "refused" } });
  });
  await page.route("**/api/composer/file**", (route) => {
    const query = new URL(route.request().url()).searchParams;
    const isOurs = query.get("token") === "fixture" && query.get("task") === GOBLIN.id;
    return route.fulfill(isOurs ? answer(query.get("path") ?? "") : { status: 403, json: { error: "refused" } });
  });
  return asked;
}

// openPictured opens Zeke's conversation with its pictures served, and
// returns its list and what the page asks the picture routes for.
export async function openPictured(page: Page) {
  await open(page, undefined, undefined, PICTURED);
  const asked = await servePictures(page);
  await openGoblin(page);
  const talk = page.getByRole("log", { name: "Messages of " + NAME });
  await expect(talk.getByText("Five screenshots came back.")).toBeVisible();
  return { talk, asked };
}

// The folded row of the reply that read two files, ran a command and took a
// screenshot, the viewer, the card a pointer over a thumbnail shows, and
// whether a picture has loaded as a picture.
export const fold = (page: Page) => page.locator('[data-message="m1"] details.message-call-group');
export const viewer = (page: Page) => page.getByRole("dialog", { name: "Pictures" });
export const hoverCard = (page: Page) => page.locator(".picture-hover-card");
export const isDrawn = (image: HTMLImageElement) => image.complete && image.naturalWidth > 0;
