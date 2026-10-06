import { test } from "node:test";
import assert from "node:assert/strict";
import { panelPeople, profileLink, safeAvatar, safeGitHubLink, sameArea, sameAreaWords, ticketLook, ticketWords } from "./people.ts";
import { parseSnapshot } from "./types.ts";

const REPO = "https://github.com/northwind/northwind-api";
const avatar = (id: number) => "https://avatars.githubusercontent.com/u/" + id + "?v=4";
const ana = { login: "ana-teammate", name: "", avatar_url: avatar(7) };
const ben = { login: "ben-teammate", name: "", avatar_url: avatar(8) };

test("only GitHub's avatar host and GitHub links are drawn or opened", () => {
  for (const [url, want] of [
    [avatar(7), avatar(7)],
    ["https://avatars.githubusercontent.com/u/7\"onerror=alert(1)", ""],
    ["http://avatars.githubusercontent.com/u/7", ""],
    ["https://evil.example/avatars.githubusercontent.com/u/7", ""],
    ["", ""],
  ]) assert.equal(safeAvatar(url), want, url);
  for (const [url, want] of [
    [REPO + "/issues/31", REPO + "/issues/31"],
    ["javascript:alert(1)", ""],
    ["https://github.com.evil.example/x", ""],
    ["", ""],
  ]) assert.equal(safeGitHubLink(url), want, url);
});

test("a person's profile opens on GitHub only when GitHub knows them", () => {
  assert.equal(profileLink(ana), "https://github.com/ana-teammate");
  assert.equal(profileLink({ login: "", name: "Dana Commit", avatar_url: "" }), "");
});

test("a ticket wears GitHub's look for where it stands and says it in its tip", () => {
  for (const [state, look] of [["queued", "open"], ["in progress", "open"], ["pr open", "open"], ["paused", "open"], ["blocked", "open"], ["merged", "merged"], ["closed", "closed"]] as const) {
    assert.equal(ticketLook({ number: 31, url: REPO + "/issues/31", state }), look, state);
  }
  assert.equal(ticketWords({ number: 31, url: REPO + "/issues/31", state: "pr open" }), "Ticket #31: pr open");
});

test("a card names each person in the same area once, with each piece of their work", () => {
  const areas = sameArea([
    { ...ana, what: "PR #46", url: REPO + "/pull/46" },
    { ...ben, what: "issue #49", url: REPO + "/issues/49" },
    { ...ana, what: "PR #37", url: REPO + "/pull/37" },
    { ...ana, what: "PR #46", url: REPO + "/pull/46" },
    { login: "", name: "", avatar_url: "", what: "branch nameless", url: "" },
  ]);
  assert.deepEqual(areas.map(sameAreaWords), ["ana-teammate: PR #46, PR #37", "ben-teammate: issue #49"]);
  assert.equal(areas[0].url, REPO + "/pull/46", "the avatar opens the first piece of work");
});

test("a panel names whoever is in the goblin's area first, then the rest of its project, each once", () => {
  const snapshot = parseSnapshot({
    healthy: true,
    projects: [{ name: "northwind-api", repository: "northwind/northwind-api", contributors: [ben, ana, { login: "", name: "Dana Commit" }] }],
    tasks: [
      { id: "nw-sync", project: "northwind-api", verified: false, overlaps: [{ ...ana, what: "PR #46", url: REPO + "/pull/46" }] },
      { id: "nw-solo", project: "solo-tool", verified: false },
    ],
  });
  const [sync, solo] = snapshot.tasks;
  assert.deepEqual(panelPeople(snapshot, sync).map(({ person, area }) => [person.login || person.name, !!area]), [["ana-teammate", true], ["ben-teammate", false], ["Dana Commit", false]]);
  assert.deepEqual(panelPeople(snapshot, solo), [], "a project only the Overlord works in names nobody");
});

test("a snapshot from a supervisor without tickets or people reads as none", () => {
  const snapshot = parseSnapshot({ healthy: true, tasks: [{ id: "nw-sync", verified: false }] });
  assert.deepEqual(snapshot.projects, []);
  assert.equal(snapshot.tasks[0].ticket, undefined);
  assert.deepEqual(snapshot.tasks[0].overlaps, []);
});
