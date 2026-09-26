import { test } from "node:test";
import assert from "node:assert/strict";
import { unsentComment, updateAction } from "./boardUpdate.ts";
import type { Action } from "./types.ts";

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

test("a code-review comment typed and not yet delivered is unsent, so a reload waits for it", () => {
  const action = (status: string) => ({ id: "c1", kind: "review", status, question_id: "", answer_kind: "", task_id: "", generation: "", message: "", text: "", file: "", line: 0, side: "", updated_at: "" }) as Action;
  const typed = { hidden: false, text: "Rename this", sending: false };
  const cases: [string, Parameters<typeof unsentComment>, boolean][] = [
    ["typed and not sent", [typed, undefined], true],
    ["cancelled", [{ ...typed, hidden: true }, undefined], false],
    ["no text", [{ ...typed, text: "  " }, undefined], false],
    ["sending", [{ ...typed, sending: true }, undefined], true],
    ["on its way", [typed, action("queued")], false],
    ["delivered", [typed, action("succeeded")], false],
    ["delivery failed", [typed, action("failed")], true],
    ["delivery unconfirmed", [typed, action("uncertain")], true],
  ];
  for (const [name, args, want] of cases) assert.equal(unsentComment(...args), want, name);
});
