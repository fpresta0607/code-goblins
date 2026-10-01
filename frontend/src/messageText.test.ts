import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
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

test("markup and entities in a question are shown as text, never interpreted; only a backticked value is code", () => {
  assert.equal(html('<img src=x onerror="alert(1)"> & **<b>bold?</b>**'), '<p>&lt;img src=x onerror=&quot;alert(1)&quot;&gt; &amp; <strong>&lt;b&gt;bold?&lt;/b&gt;</strong></p>');
  assert.equal(html("[link](javascript:alert(1)) `code` _em_"), "<p>[link](javascript:alert(1)) <code>code</code> _em_</p>");
  assert.equal(html("`<script>alert(1)</script>`"), "<p><code>&lt;script&gt;alert(1)&lt;/script&gt;</code></p>");
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

// The Overlord, 2026-09-28, on a waiting card that listed three Cloudflare DNS
// records as one paragraph with literal ** marks: "this should be a clean
// copyable field for each in clean table". A value between backticks is one
// to enter somewhere, and a Markdown pipe table is a table.
test("a value between backticks is a code value the card can copy", () => {
  // Arrange
  const copyable = (value: string) => createElement("button", { "data-copy": value }, value);

  // Act
  const plain = html("Point `mcp` at `mcp-precisiondocs.fly.dev` and keep `**`.");
  const copied = renderToStaticMarkup(messageElements("Set `MCP_HOST=mcp.precisiondocs.ai` **now**", copyable));

  // Assert
  assert.equal(plain, "<p>Point <code>mcp</code> at <code>mcp-precisiondocs.fly.dev</code> and keep <code>**</code>.</p>");
  assert.equal(copied, '<p>Set <button data-copy="MCP_HOST=mcp.precisiondocs.ai">MCP_HOST=mcp.precisiondocs.ai</button> <strong>now</strong></p>');
});

test("a Markdown pipe table becomes a table whose backticked cells copy", () => {
  // Arrange
  const text = "Add these three DNS records in **Cloudflare**, then tell me\n| Type | Name | Content | Proxy |\n| --- | --- | --- | --- |\n| CNAME | `mcp` | `mcp-precisiondocs.fly.dev` | Off |\n| TXT | `_acme-challenge.mcp` | `9fQe2kLx7Rm0aPz4Vb8Nw` | Off |\nSo mcp.precisiondocs.ai serves the connector.";

  // Act
  const blocks = messageBlocks(text);
  const markup = html(text);

  // Assert
  assert.deepEqual(blocks.map((block) => block.kind), ["paragraph", "table", "paragraph"]);
  assert.equal(markup, "<p>Add these three DNS records in <strong>Cloudflare</strong>, then tell me</p>"
    + "<div class=\"message-table\"><table><thead><tr><th>Type</th><th>Name</th><th>Content</th><th>Proxy</th></tr></thead><tbody>"
    + "<tr><td>CNAME</td><td><code>mcp</code></td><td><code>mcp-precisiondocs.fly.dev</code></td><td>Off</td></tr>"
    + "<tr><td>TXT</td><td><code>_acme-challenge.mcp</code></td><td><code>9fQe2kLx7Rm0aPz4Vb8Nw</code></td><td>Off</td></tr>"
    + "</tbody></table></div><p>So mcp.precisiondocs.ai serves the connector.</p>");
});

test("pipes without a separator row stay text, and a one-line summary leaves the table out", () => {
  assert.equal(html("a | b | c"), "<p>a | b | c</p>");
  assert.equal(html("| a | b |\n| c | d |"), "<p>| a | b |\n| c | d |</p>");
  assert.equal(plainMessage("Add the records\n| Type | Name |\n| --- | --- |\n| CNAME | `mcp` |\nthen tell me"), "Add the records then tell me");
  assert.equal(plainMessage("Run `cfo doctor` first"), "Run cfo doctor first");
});
