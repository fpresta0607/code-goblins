import { test } from "node:test";
import assert from "node:assert/strict";
import { devDriveOffer } from "./dev-drive-offer.ts";
import { parseSnapshot } from "./types.ts";

const snapshot = (value: Record<string, unknown> = {}) => parseSnapshot({ healthy: true, instance: "fixture", ...value });

test("a board without the Dev Drive setting shows none", () => {
  assert.equal(snapshot().dev_drive, undefined);
});

test("the Dev Drive reads with its line, the panel's note, the explanation and the button to press", () => {
  const drive = snapshot({ dev_drive: { state: "absent", line: "absent: this machine can have one", note: "An optional drive that Defender scans in performance mode, so files open faster.", explain: "It is not a Defender exclusion.", action: "set-up" } }).dev_drive;
  assert.deepEqual(drive, { state: "absent", line: "absent: this machine can have one", note: "An optional drive that Defender scans in performance mode, so files open faster.", explain: "It is not a Defender exclusion.", choice: "", waiting: "", action: "set-up", status: "" });
});

test("the Dev Drive row reads what it waits on, with nothing to press while the supervisor carries the gate's home", () => {
  const status = "The worktrees and caches are on D:\\CodeGoblins. The gate's home is not there yet. Waiting for 1 gate run to finish, then the gate's home moves by itself.";
  const drive = snapshot({ dev_drive: { state: "on", line: "", note: "", explain: "", choice: "wanted", status } }).dev_drive;
  assert.equal(drive?.status, status);
  assert.equal(drive?.action, "");
  assert.equal(drive?.waiting, "");
});

test("a step waiting and a button the board does not know offer nothing to press", () => {
  const drive = snapshot({ dev_drive: { state: "absent", line: "", explain: "", choice: "wanted", waiting: "create", action: "format-everything" } }).dev_drive;
  assert.equal(drive?.action, "");
  assert.equal(drive?.waiting, "create");
});

test("the first run offers a Dev Drive once, where this machine can have one", () => {
  const drive = (state: string, choice = "") => ({ state, line: "", note: "", explain: "", choice, waiting: "", action: "" as const, status: "" });
  assert.equal(devDriveOffer(drive("absent")), "offer");
  assert.equal(devDriveOffer(drive("present")), "offer");
  assert.equal(devDriveOffer(drive("unavailable")), "unavailable");
  assert.equal(devDriveOffer(drive("absent", "declined")), null);
  assert.equal(devDriveOffer(drive("absent", "wanted")), null);
  assert.equal(devDriveOffer(drive("on")), null);
  assert.equal(devDriveOffer(undefined), null);
});
