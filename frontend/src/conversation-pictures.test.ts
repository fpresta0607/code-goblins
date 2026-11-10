import { test } from "node:test";
import assert from "node:assert/strict";
import { callPictures, conversationPictures, promptPictures } from "./conversation-pictures.ts";
import { parseMessages } from "./session-messages.ts";

// A message as the supervisor serves it (internal/fleettree/messages.go).
const served = (seq: number, fields: Record<string, unknown> = {}) => ({ id: "m" + seq, seq, kind: "prompt", at: "2026-11-10T09:00:00Z", source: "overlord", text: "", ...fields });
const shot = (id: string) => ({ id, media_type: "image/png", bytes: 120 });
const call = (tool: string, ...ids: string[]) => ({ kind: "call", tool, subject: tool, state: "done", pictures: ids.map(shot) });
const picture = (id: string) => "id:" + id;
const keptPicture = (path: string) => "kept:" + path;
const KEPT = "C:\\goblin\\tasktmp\\cg-touch\\composer-files\\pasted-1.png";

test("a row's pictures carry the verb of the call that holds each, in the order the calls ran", () => {
  // Arrange
  const [reply] = parseMessages([served(1, { kind: "reply", blocks: [call("Read", "a", "b"), call("Bash"), call("view_image", "c"), call("mcp__chrome__take_screenshot", "d")] })]);

  // Act
  const pictures = callPictures(reply.blocks, picture);

  // Assert
  assert.deepEqual(pictures, [
    { src: "id:a", verb: "Read" }, { src: "id:b", verb: "Read" }, { src: "id:c", verb: "Viewed" }, { src: "id:d", verb: "Chrome · Take screenshot" },
  ]);
});

for (const [name, fields, want] of [
  ["the pictures the transcript holds come before those the box kept by path", { text: "what is wrong here?\n" + KEPT, pictures: [shot("p1")] }, ["id:p1", "kept:" + KEPT]],
  ["a prompt with no picture has none", { text: "Read the brief" }, []],
  ["a command's words are never read as a kept picture", { source: "shell", text: KEPT }, []],
] as const) {
  test("a prompt's pictures: " + name, () => {
    // Arrange
    const [prompt] = parseMessages([served(0, fields)]);

    // Act
    const sources = promptPictures(prompt, picture, keptPicture);

    // Assert
    assert.deepEqual(sources, want);
  });
}

test("a conversation's pictures are every picture its rows show, once each, in the order they show them", () => {
  // Arrange: a prompt with a pasted and a kept picture, a reply whose calls
  // hold three, a notice, which draws no picture, the same kept picture sent
  // again, and a compaction.
  const messages = parseMessages([
    served(0, { text: "look\n" + KEPT, pictures: [shot("p1")] }),
    served(1, { kind: "reply", blocks: [{ kind: "text", text: "Reading them." }, call("Read", "a"), call("Bash"), call("Read", "b", "c")] }),
    served(2, { source: "notice", text: "A hook said so", pictures: [shot("hidden")] }),
    served(3, { text: KEPT }),
    served(4, { kind: "compaction", text: "Summary" }),
    served(5, { kind: "reply", blocks: [call("view_image", "d")] }),
  ]);

  // Act
  const sources = conversationPictures(messages, picture, keptPicture);

  // Assert
  assert.deepEqual(sources, ["id:p1", "kept:" + KEPT, "id:a", "id:b", "id:c", "id:d"]);
});

test("a conversation with no picture has an empty set", () => {
  assert.deepEqual(conversationPictures(parseMessages([served(0, { text: "Read the brief" }), served(1, { kind: "reply", blocks: [call("Bash")] })]), picture, keptPicture), []);
});
