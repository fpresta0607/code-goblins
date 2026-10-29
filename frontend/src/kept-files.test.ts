import { test } from "node:test";
import assert from "node:assert/strict";
import { filesOf, isPicturePath, keptFileAddress, keptPictures } from "./kept-files.ts";

const WINDOWS = "C:\\dev\\code-goblins\\state\\tasktmp\\task-1\\composer-files\\pasted-123.png";
const UNIX = "/home/goblin/composer-files/pasted-1.png";

test("a picture the box kept is told from a file by its extension", () => {
  for (const path of [WINDOWS, UNIX, "C:\\x\\composer-files\\pasted-2.JPG", "/x/composer-files/pasted-3.webp"]) assert.equal(isPicturePath(path), true, path);
  for (const path of ["C:\\x\\composer-files\\pasted-4.md", "/x/composer-files/pasted-5", "/x/composer-files/pasted-6.pdf"]) assert.equal(isPicturePath(path), false, path);
});

test("his words keep their text, and each line that names a kept picture becomes its preview", () => {
  // Arrange
  const text = "what is wrong here?\n" + WINDOWS + "\n" + UNIX;

  // Act
  const read = keptPictures(text);

  // Assert
  assert.deepEqual(read, { text: "what is wrong here?", paths: [WINDOWS, UNIX] });
});

test("a kept picture's path inside a sentence, another folder's picture and a kept file stay words", () => {
  for (const text of [
    "look at " + WINDOWS + " please",
    "C:\\Users\\me\\Pictures\\pasted-1.png",
    "C:\\x\\composer-files\\pasted-4.md",
    "C:\\x\\composer-files\\nested\\pasted-1.png",
    "",
  ]) assert.deepEqual(keptPictures(text), { text, paths: [] }, text);
});

test("a message of pictures alone has no words left", () => {
  assert.deepEqual(keptPictures("  " + UNIX + "  \n" + WINDOWS), { text: "", paths: [UNIX, WINDOWS] });
});

test("a kept file's address names the board's token, the task and the path", () => {
  // Act
  const goblin = new URL(keptFileAddress("token-1", "task-1", WINDOWS), "http://board");
  const cfo = new URL(keptFileAddress("token-1", undefined, UNIX), "http://board");

  // Assert
  assert.equal(goblin.pathname, "/api/composer/file");
  assert.deepEqual(Object.fromEntries(goblin.searchParams), { token: "token-1", task: "task-1", path: WINDOWS });
  assert.deepEqual(Object.fromEntries(cfo.searchParams), { token: "token-1", path: UNIX });
});

// transfer is a DataTransfer as a browser hands a paste or a drop: its files
// list, and its items, each a file or a string.
function transfer(files: File[], items: { kind: string; file: File | null }[]): DataTransfer {
  return { files, items: items.map((item) => ({ kind: item.kind, getAsFile: () => item.file })) } as unknown as DataTransfer;
}

test("a paste or drop takes its files from the files list, or from its items when that list is empty", () => {
  // Arrange
  const shot = new File(["png"], "image.png", { type: "image/png" });
  const notes = new File(["# notes"], "notes.md", { type: "text/markdown" });

  // Act and Assert
  assert.deepEqual(filesOf(transfer([shot, notes], [])), [shot, notes], "the files list when it holds any");
  assert.deepEqual(filesOf(transfer([], [{ kind: "string", file: null }, { kind: "file", file: shot }, { kind: "file", file: null }])), [shot], "else each item that is a file");
  assert.deepEqual(filesOf(transfer([], [{ kind: "string", file: null }])), [], "and none for text alone");
  assert.deepEqual(filesOf(null), [], "nor for no data at all");
});
