import { test } from "node:test";
import assert from "node:assert/strict";
import { updateAction } from "./boardUpdate.ts";

test("a tab running an older build reloads only while hidden, idle and live, and otherwise offers a reload", () => {
  const cases: [string, Parameters<typeof updateAction>[0], ReturnType<typeof updateAction>][] = [
    ["the same build", { loaded: "abc", served: "abc", hidden: true, answering: false, connected: true }, "none"],
    ["a board that names no build", { loaded: "abc", served: "", hidden: true, answering: false, connected: true }, "none"],
    ["a page loaded without a build, such as the dev server", { loaded: "", served: "abc", hidden: true, answering: false, connected: true }, "none"],
    ["updated while he looks at the board", { loaded: "abc", served: "def", hidden: false, answering: false, connected: true }, "banner"],
    ["updated in a hidden tab", { loaded: "abc", served: "def", hidden: true, answering: false, connected: true }, "reload"],
    ["updated in a hidden tab mid-answer", { loaded: "abc", served: "def", hidden: true, answering: true, connected: true }, "banner"],
    ["updated in a hidden tab while the supervisor is down", { loaded: "abc", served: "def", hidden: true, answering: false, connected: false }, "banner"],
    ["updated while he answers", { loaded: "abc", served: "def", hidden: false, answering: true, connected: true }, "banner"],
  ];
  for (const [name, given, want] of cases) assert.equal(updateAction(given), want, name);
});
