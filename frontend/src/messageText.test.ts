import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { leadAndRest, linkedText, messageBlocks, messageElements, pathElements, plainMessage } from "./messageText.ts";

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

for (const [name, text, want] of [
  ["an https address", "Roll it at https://github.com/settings/tokens.", '<p>Roll it at <a class="message-link" href="https://github.com/settings/tokens" target="_blank" rel="noreferrer">https://github.com/settings/tokens</a>.</p>'],
  ["an http address in brackets", "(page http://127.0.0.1:4387/session/f26e)", '<p>(page <a class="message-link" href="http://127.0.0.1:4387/session/f26e" target="_blank" rel="noreferrer">http://127.0.0.1:4387/session/f26e</a>)</p>'],
  ["a backticked address", "Open `https://github.com/settings/tokens`", '<p>Open <a class="message-link" href="https://github.com/settings/tokens" target="_blank" rel="noreferrer">https://github.com/settings/tokens</a></p>'],
  ["a bold address", "**https://example.com/a?b=1**", '<p><strong><a class="message-link" href="https://example.com/a?b=1" target="_blank" rel="noreferrer">https://example.com/a?b=1</a></strong></p>'],
  ["other schemes", "ftp://example.com, javascript:alert(1) and file:///C:/x", "<p>ftp://example.com, javascript:alert(1) and file:///C:/x</p>"],
  ["a quote ending an address", 'see "https://example.com/x" now', '<p>see &quot;<a class="message-link" href="https://example.com/x" target="_blank" rel="noreferrer">https://example.com/x</a>&quot; now</p>'],
] as const) {
  test(`${name} in a message is a link only when it is http or https`, () => {
    // Arrange, act
    const shown = html(text);

    // Assert
    assert.equal(shown, want);
  });
}

test("a line of plain text links its addresses and nothing else", () => {
  // Arrange, act
  const shown = renderToStaticMarkup(linkedText("Answered: **done** at https://github.com/example/board/pull/412"));

  // Assert
  assert.equal(shown, 'Answered: **done** at <a class="message-link" href="https://github.com/example/board/pull/412" target="_blank" rel="noreferrer">https://github.com/example/board/pull/412</a>');
});

test("phone prose, backticks and History validate Scrawl links while local rendering keeps them local", () => {
  const review = "http://127.0.0.1:4387/session/existing?mode=notes#answer";
  const before = Object.getOwnPropertyDescriptor(globalThis, "location");
  try {
    for (const [protocol, hostname, href] of [
      ["http:", "127.0.0.1", review],
      ["https:", "pc.example.ts.net", "/api/remote/scrawl-link?url=" + encodeURIComponent(review)],
    ]) {
      Object.defineProperty(globalThis, "location", { configurable: true, value: { protocol, hostname } });
      const link = `<a class="message-link" href="${href}" target="_blank" rel="noreferrer">${review}</a>`;
      assert.equal(html("Review " + review), `<p>Review ${link}</p>`);
      assert.equal(html("Review `" + review + "`"), `<p>Review ${link}</p>`);
      assert.equal(renderToStaticMarkup(linkedText("Answered on " + review)), "Answered on " + link);
      assert.equal(html("https://example.org/session/review"), '<p><a class="message-link" href="https://example.org/session/review" target="_blank" rel="noreferrer">https://example.org/session/review</a></p>');
    }
  } finally {
    if (before) Object.defineProperty(globalThis, "location", before);
    else Reflect.deleteProperty(globalThis, "location");
  }
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
  assert.equal(plainMessage("Ship it?\n\n- **Verdict:** not yet\n- one defect"), "Ship it? Verdict: not yet. One defect.");
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

test("bold may wrap a code value, sit beside one, and a lone mark near one stays as written", () => {
  // Arrange
  const copyable = (value: string) => createElement("button", { "data-copy": value }, value);

  // Act
  const around = renderToStaticMarkup(messageElements("**Add `mcp` now**", copyable));
  const only = html("**`mcp`**");
  const beside = html("**Add** `mcp` and `www` **now**");
  const lone = html("Add ** `mcp` and `**` now");

  // Assert
  assert.equal(around, '<p><strong>Add <button data-copy="mcp">mcp</button> now</strong></p>');
  assert.equal(only, "<p><strong><code>mcp</code></strong></p>");
  assert.equal(beside, "<p><strong>Add</strong> <code>mcp</code> and <code>www</code> <strong>now</strong></p>");
  assert.equal(lone, "<p>Add ** <code>mcp</code> and <code>**</code> now</p>");
  assert.equal(plainMessage("**Add `mcp` now**"), "Add mcp now");
});

test("a message that opens with a table has no lead line, so the table stays whole in its body", () => {
  // Arrange
  const table = "| Type | Name |\n| --- | --- |\n| CNAME | `mcp` |\nAdd these records";

  // Act
  const opensWithTable = leadAndRest(table);
  const opensWithWords = leadAndRest("Add these records\n" + table);

  // Assert
  assert.deepEqual(opensWithTable, { lead: "", rest: table });
  assert.deepEqual(messageBlocks(opensWithTable.rest).map((block) => block.kind), ["table", "paragraph"]);
  assert.deepEqual(opensWithWords, { lead: "Add these records", rest: table });
});

// The Overlord, 2026-10-17: "if there is a folder, there should be a link to
// the open the file explorer folder on my PC". A path renders as a link the
// way the supervisor finds it (internal/supervisor/card_paths.go).
const linked = (path: string, key: number) => createElement("a", { key }, path);

test("a local path in a card's text is drawn as a link, up to a space and without the punctuation that ends a sentence", () => {
  // Arrange
  const step = String.raw`Send the email in C:\dev\code-goblins\data\pd-proxy-primary\iproyal-refund.md.`;

  // Act
  const drawn = renderToStaticMarkup(createElement("p", null, ...pathElements(step, linked)));

  // Assert
  assert.equal(drawn, String.raw`<p>Send the email in <a>C:\dev\code-goblins\data\pd-proxy-primary\iproyal-refund.md</a>.</p>`);
});

test("a code value that is a whole path is one link, spaces and all, and text with no path renderer is unchanged", () => {
  // Arrange
  const text = String.raw`Keep the notes in ` + "`" + String.raw`C:\Users\fpres\OneDrive - SIQstack\Desktop` + "`" + String.raw` or (D:/CodeGoblins/scratch), not https://example.com/C:\x or 3:4.`;

  // Act
  const withLinks = renderToStaticMarkup(messageElements(text, undefined, linked));
  const withoutLinks = html(text);

  // Assert
  assert.equal(withLinks, String.raw`<p>Keep the notes in <a>C:\Users\fpres\OneDrive - SIQstack\Desktop</a> or (<a>D:/CodeGoblins/scratch</a>), not <a class="message-link" href="https://example.com/C:\x" target="_blank" rel="noreferrer">https://example.com/C:\x</a> or 3:4.</p>`);
  assert.equal(withoutLinks, String.raw`<p>Keep the notes in <code>C:\Users\fpres\OneDrive - SIQstack\Desktop</code> or (D:/CodeGoblins/scratch), not <a class="message-link" href="https://example.com/C:\x" target="_blank" rel="noreferrer">https://example.com/C:\x</a> or 3:4.</p>`);
});
