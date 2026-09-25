import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToStaticMarkup } from "react-dom/server";
import { messageBlocks, messageElements, plainMessage } from "./messageText.ts";

const html = (text: string) => renderToStaticMarkup(messageElements(text));

test("only text the asker marks with double asterisks is bold", () => {
  assert.equal(html("Ship it? **Blocking:** the gate is red."), "<p>Ship it? <strong>Blocking:</strong> the gate is red.</p>");
  assert.equal(html("A lone ** stays as written, and so does **this"), "<p>A lone ** stays as written, and so does **this</p>");
  assert.equal(html("5 ** 2 is 25 ** 1"), "<p>5 ** 2 is 25 ** 1</p>");
  assert.equal(html("**one** and **two**"), "<p><strong>one</strong> and <strong>two</strong></p>");
  assert.equal(html("empty **** marks"), "<p>empty **** marks</p>");
});

test("markup and entities in a question are shown as text, never interpreted", () => {
  assert.equal(html('<img src=x onerror="alert(1)"> & **<b>bold?</b>**'), '<p>&lt;img src=x onerror=&quot;alert(1)&quot;&gt; &amp; <strong>&lt;b&gt;bold?&lt;/b&gt;</strong></p>');
  assert.equal(html("[link](javascript:alert(1)) `code` _em_"), "<p>[link](javascript:alert(1)) `code` _em_</p>");
});

test("a blank line starts a new paragraph and a single line break is kept inside one", () => {
  assert.equal(html("Which layout?\n\nThe grid keeps cards aligned.\nThe list reads faster."), "<p>Which layout?</p><p>The grid keeps cards aligned.\nThe list reads faster.</p>");
  assert.equal(html("\n\n  Only text  \n\n\n"), "<p>Only text</p>");
  assert.equal(html("windows\r\n\r\nline ends"), "<p>windows</p><p>line ends</p>");
});

test("lines that start with a dash and a space become a bulleted list", () => {
  assert.equal(
    html("Is the report ready to send?\n- **Verdict:** not yet\n- one blocking defect\nThe rest passes."),
    "<p>Is the report ready to send?</p><ul><li><strong>Verdict:</strong> not yet</li><li>one blocking defect</li></ul><p>The rest passes.</p>",
  );
  assert.equal(html("-not a bullet\n- a bullet"), "<p>-not a bullet</p><ul><li>a bullet</li></ul>");
});

test("the blocks name each paragraph and list with its bold spans", () => {
  assert.deepEqual(messageBlocks("Go?\n- **yes** now"), [
    { kind: "paragraph", spans: [{ text: "Go?", bold: false }] },
    { kind: "list", items: [[{ text: "yes", bold: true }, { text: " now", bold: false }]] },
  ]);
  assert.deepEqual(messageBlocks(""), []);
});

test("a one-line summary drops the marks and the line breaks", () => {
  assert.equal(plainMessage("Ship it?\n\n- **Verdict:** not yet\n- one defect"), "Ship it? Verdict: not yet; one defect");
  assert.equal(plainMessage("a ** b"), "a ** b");
  assert.equal(plainMessage("Which layout?\nThe grid\n  keeps cards aligned."), "Which layout? The grid keeps cards aligned.");
});
