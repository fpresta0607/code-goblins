import { test } from "node:test";
import assert from "node:assert/strict";
import { parseSnapshot } from "./types.ts";

const snapshot = (value: Record<string, unknown> = {}) => parseSnapshot({ healthy: true, instance: "fixture", ...value });

test("a board without the Dev Drive setting shows none", () => {
  assert.equal(snapshot().dev_drive, undefined);
});

test("the Dev Drive reads with its line, the explanation and the button to press", () => {
  const drive = snapshot({ dev_drive: { state: "absent", line: "absent: this machine can have one", explain: "It is not a Defender exclusion.", action: "set-up" } }).dev_drive;
  assert.deepEqual(drive, { state: "absent", line: "absent: this machine can have one", explain: "It is not a Defender exclusion.", choice: "", waiting: "", action: "set-up" });
});

test("a step waiting and a button the board does not know offer nothing to press", () => {
  const drive = snapshot({ dev_drive: { state: "absent", line: "", explain: "", choice: "wanted", waiting: "create", action: "format-everything" } }).dev_drive;
  assert.equal(drive?.action, "");
  assert.equal(drive?.waiting, "create");
});
