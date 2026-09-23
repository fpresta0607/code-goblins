import { test } from "node:test";
import assert from "node:assert/strict";
import { bracketedPaste, inputBytes, maxInputBytes, queueInput, type TerminalCommand } from "./terminalInput.ts";

test("Unicode paste uses UTF-8 bytes including one complete bracketed wrapper", () => {
  const limit = Math.floor((maxInputBytes - 12) / 3);
  const accepted = bracketedPaste("界".repeat(limit));
  assert.ok(inputBytes(accepted) <= maxInputBytes);
  assert.ok(accepted.startsWith("\x1b[200~") && accepted.endsWith("\x1b[201~"));
  assert.throws(() => bracketedPaste("界".repeat(limit + 1)), /nothing was sent/);
  assert.throws(() => bracketedPaste("🙂".repeat(16382)), /nothing was sent/);
  assert.equal(bracketedPaste("one\n\x1b[201~two"), "\x1b[200~one\ntwo\x1b[201~");
});

test("typing coalesces adjacent text, preserving Unicode, paste and control barriers", () => {
  const queue:TerminalCommand[]=[];
  for(const text of ["c","a","f","\u00e9","\u65e5\u672c","\ud83d\ude42","e\u0301"]) queueInput(queue,{type:"terminal.input",text});
  queueInput(queue,{type:"terminal.input",text:"\r"});
  const paste=bracketedPaste("one\ntwo");
  queueInput(queue,{type:"terminal.input",text:paste});
  queueInput(queue,{type:"terminal.resize",cols:80,rows:24});
  queueInput(queue,{type:"terminal.input",text:"after"});
  queueInput(queue,{type:"terminal.input",text:"\x1b[A"});
  assert.deepEqual(queue.map(q=>q.type==="terminal.input"?q.text:q.type),["caf\u00e9\u65e5\u672c\ud83d\ude42e\u0301","\r",paste,"terminal.resize","after","\x1b[A"]);
  const bounded:TerminalCommand[]=[];
  for(let i=0;i<5000;i++) queueInput(bounded,{type:"terminal.input",text:"x"});
  assert.equal(bounded.length,2);
  assert.equal(bounded.map(q=>q.text).join("").length,5000);
});
