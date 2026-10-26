import { mkdirSync } from "node:fs";
import { expect, test } from "./site";
import { at, GOBLIN, open, openGoblin, snapshot } from "./session-board";

// The page of before and after the Overlord asked for on 2026-10-26: the
// selector, the floated composer, the queue with its actions, readable
// thinking and the bottom line while the session works, at desktop and phone
// width. The screenshots are attached to the run and copied to the page.

const LONG = "git log --oneline --decorate --graph --all --stat -- . ':(exclude)frontend/node_modules' ':(exclude)internal/boardweb/dist' ':(exclude)docs/evidence'";
const page = {
  messages: [
    { id: "m0", seq: 0, at, kind: "prompt", source: "overlord", text: "Take the chat panel through these fixes and show me the result." },
    { id: "m1", seq: 1, at, kind: "reply", blocks: [
      { kind: "thinking", text: "The composer needs to float over the conversation, and the working state should be the dither the Overlord picked, in the harness colour. The queue entries need their three icon actions and readable thinking." },
      { kind: "call", tool: "Bash", subject: LONG, state: "running" },
      { kind: "text", text: "The conversation reads without a wall of numbers: the line at the bottom carries the working state, then when the last message was sent and when the reply arrived." },
    ] },
    { id: "m2", seq: 2, at, kind: "prompt", source: "overlord", text: "Also show the queued message in the pane, with send now, edit and delete." },
  ],
  more: false,
  usage: { input_tokens: 1_400_000, output_tokens: 289_000, cache_read_tokens: 208_000_000, cost_usd: 3.209, context_tokens: 593_000, context_window: 1_000_000, model: "claude-opus-5-5" },
};
const action = {
  id: "q1", kind: "message", task_id: GOBLIN.id, generation: GOBLIN.generation, status: "queued",
  text: "hold the merge until the gate lets go and CI has passed on the new head",
  message: "Queued for " + GOBLIN.id + ". Its gate holds the branch, so it is delivered once the gate lets go, however long that takes.",
  can_send_now: false, send_now_why: "Its gate holds the branch.", updated_at: at,
};
const subscriptions = [{ provider: "claude", status: "available", percent_remaining: 12, read_at: at, resets_at: "2026-10-28T09:00:00Z", source: "oauth", floor_percent: 5 }];
const tree = { task_id: GOBLIN.id, generation: GOBLIN.generation, harness: GOBLIN.harness, memory: 208 * 2 ** 20, own_memory: 208 * 2 ** 20, conversation_at: at, children: [], unread: [], source_updated_at: at, fetched_at: at };

test("the panel after the fixes, at desktop and phone width", async ({ page: board }, testInfo) => {
  // Arrange
  await open(board, snapshot({ actions: [action], subscriptions }, { tree, runtime: { state: "working", at: new Date(Date.now() - 72_000).toISOString() } }), "rebase onto main", page);
  await openGoblin(board);
  await expect(board.locator(".queued-message.pane")).toBeVisible();

  // Assert desktop
  mkdirSync("test-results/preview", { recursive: true });
  await board.screenshot({ path: "test-results/preview/panel-after-desktop.png" });
  await testInfo.attach("panel-after-desktop.png", { body: await board.screenshot(), contentType: "image/png" });
  // The top of the conversation shows the readable thinking and the wrapped
  // tool row.
  await board.locator(".conversation").evaluate((element) => { element.scrollTop = 0; });
  await board.screenshot({ path: "test-results/preview/panel-after-thinking.png" });

  // Act: a phone's width.
  await board.setViewportSize({ width: 390, height: 844 });

  // Assert phone
  await expect(board.getByRole("group", { name: "Panel view", exact: true })).toBeVisible();
  await board.screenshot({ path: "test-results/preview/panel-after-phone.png" });
  await testInfo.attach("panel-after-phone.png", { body: await board.screenshot(), contentType: "image/png" });
});
