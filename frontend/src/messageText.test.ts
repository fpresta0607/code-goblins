import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { decisionAndDetails, inlineElements, isLongText, leadAndRest, linkedSentence, linkedText, literalLinks, messageBlocks, messageElements, pathElements, plainMessage } from "./messageText.ts";

const html = (text: string) => renderToStaticMarkup(messageElements(text));

test("a Modern document renderer replaces a labeled PDF and preserves adjacent report text", () => {
  const target = "D:/task/Retail%20candidate (approved).pdf";
  const shown = renderToStaticMarkup(messageElements(`Before [Retail PDF](${target}): 40 spaces.`, undefined, undefined,
    (label, path, key) => createElement("span", { key, "data-document": path }, label)));
  assert.equal(shown, `<p>Before <span data-document="${target}">Retail PDF</span>: 40 spaces.</p>`);
});

test("local PDF examples inside inline code remain literal with the document renderer", () => {
  const link = "[Retail PDF](C:/output/candidate.pdf)";
  const shown = renderToStaticMarkup(messageElements(`Read \`${link}\`.`, undefined, undefined,
    (label, path, key) => createElement("span", { key, "data-document": path }, label)));
  assert.equal(shown, `<p>Read <code>${link}</code>.</p>`);
  assert.equal(html(link), `<p>${link}</p>`, "other shared message renderers keep their existing text contract");
});

for (const label of ["Retail **candidate** PDF", "Retail *candidate* PDF", "**Retail *candidate* PDF**", "`Retail candidate PDF`", "`Retail candidate` PDF", "Retail `candidate` PDF"]) {
  for (const target of ["D:/task/report.pdf", String.raw`D:\task\report.pdf`, "/task/report%20(approved).pdf"]) {
    const link = `[${label}](${target})`;
    const card = `<span data-document="${target}">Retail candidate PDF</span>`;
    for (const [name, text, expected] of [
      ["paragraph", `Before ${link}: after.`, `<p>Before ${card}: after.</p>`],
      ["bullet", `- ${link}`, `<ul><li>${card}</li></ul>`],
      ["ordered step", `3. ${link}`, `<ol start="3"><li>${card}</li></ol>`],
      ["table header", `| ${link} |\n| --- |\n| Value |`, `<div class="message-table"><table><thead><tr><th>${card}</th></tr></thead><tbody><tr><td>Value</td></tr></tbody></table></div>`],
      ["table cell", `| Report |\n| --- |\n| ${link} |`, `<div class="message-table"><table><thead><tr><th>Report</th></tr></thead><tbody><tr><td>${card}</td></tr></tbody></table></div>`],
      ["heading", `Before ${link}: after.`, `Before ${card}: after.`],
    ]) {
      test(`a document with label ${label} and target ${target} renders in a ${name}`, () => {
        const render = name === "heading" ? inlineElements : messageElements;

        const shown = renderToStaticMarkup(render(text, undefined, undefined,
          (label, path, key) => createElement("span", { key, "data-document": path }, label)));

        assert.equal(shown, expected);
      });
    }
  }
}

test("document rendering preserves enclosing emphasis, web links and emphasized code examples", () => {
  const link = "[Retail **candidate** PDF](D:/task/report.pdf)";
  const text = `**Before ${link}: after.**\n\n*Read ${link} now.*\n\nExample: \`${link}\`.\n\nhttps://example.com/report`;
  const card = '<span data-document="D:/task/report.pdf">Retail candidate PDF</span>';

  const shown = renderToStaticMarkup(messageElements(text, undefined, undefined,
    (label, path, key) => createElement("span", { key, "data-document": path }, label)));

  assert.equal(shown, `<p><strong>Before ${card}: after.</strong></p><p><em>Read ${card} now.</em></p><p>Example: <code>${link}</code>.</p><p><a class="message-link" href="https://example.com/report" target="_blank" rel="noreferrer">https://example.com/report</a></p>`);
  assert.equal(html(link), "<p>[Retail <strong>candidate</strong> PDF](D:/task/report.pdf)</p>");
});

test("a code-styled document label keeps its card while code holding or crossing a link stays literal", () => {
  const card = (label: string, path: string, key: number) => createElement("span", { key, "data-document": path }, label);
  const text = "Example: `[Retail PDF](D:/task/report.pdf)` beside [`Retail` PDF](D:/task/report.pdf) and [Retail `PDF](D:/task/report.pdf) now`.";

  const shown = renderToStaticMarkup(messageElements(text, undefined, undefined, card));
  const inWebAddress = renderToStaticMarkup(messageElements("https://example.com/[`Retail` PDF](D:/task/report.pdf)", undefined, undefined, card));

  assert.equal(shown, '<p>Example: <code>[Retail PDF](D:/task/report.pdf)</code> beside <span data-document="D:/task/report.pdf">Retail PDF</span> and [Retail <code>PDF](D:/task/report.pdf) now</code>.</p>');
  assert.equal(inWebAddress, '<p><a class="message-link" href="https://example.com/[" target="_blank" rel="noreferrer">https://example.com/[</a><code>Retail</code> PDF](D:/task/report.pdf)</p>');
  assert.equal(html("[`Retail` PDF](D:/task/report.pdf)"), "<p>[<code>Retail</code> PDF](D:/task/report.pdf)</p>", "other shared message renderers keep their existing text contract");
});

test("document labels and adjacent web addresses keep their separate actions", () => {
  const shown = renderToStaticMarkup(messageElements("[Report https://example.com](D:/task/report.pdf)https://example.com/details", undefined, undefined,
    (label, path, key) => createElement("span", { key, "data-document": path }, label)));

  assert.equal(shown, '<p><span data-document="D:/task/report.pdf">Report https://example.com</span><a class="message-link" href="https://example.com/details" target="_blank" rel="noreferrer">https://example.com/details</a></p>');
});

test("PDF link syntax within a web address remains part of the web address", () => {
  const shown = renderToStaticMarkup(messageElements("https://example.com/[Report](D:/task/report.pdf)", undefined, undefined,
    (label, path, key) => createElement("span", { key, "data-document": path }, label)));

  assert.equal(shown, '<p><a class="message-link" href="https://example.com/[Report](D:/task/report.pdf" target="_blank" rel="noreferrer">https://example.com/[Report](D:/task/report.pdf</a>)</p>');
});

test("thinking prose renders emphasis and ordered steps without changing code", () => {
  assert.equal(html("Read *carefully* and `*literally*`.\n\n3. First\n4. **Next**"), "<p>Read <em>carefully</em> and <code>*literally*</code>.</p><ol start=\"3\"><li>First</li><li><strong>Next</strong></li></ol>");
});

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
  ["an http address in brackets", "(page http://127.0.0.1:4387/review/f26e)", '<p>(page <a class="message-link" href="http://127.0.0.1:4387/review/f26e" target="_blank" rel="noreferrer">http://127.0.0.1:4387/review/f26e</a>)</p>'],
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
  assert.equal(shown, 'Answered: **done** at <a class="message-link message-chip" href="https://github.com/example/board/pull/412" target="_blank" rel="noreferrer" title="https://github.com/example/board/pull/412">board PR #412</a>');
});

const named = (address: string, label: string) => `<a class="message-link" href="${address}" target="_blank" rel="noreferrer" title="${address}">${label}</a>`;
const chip = (address: string, label: string) => `<a class="message-link message-chip" href="${address}" target="_blank" rel="noreferrer" title="${address}">${label}</a>`;
const raw = (address: string) => `<a class="message-link" href="${address}" target="_blank" rel="noreferrer">${address}</a>`;

for (const [name, address, label] of [
  ["a pull request", "https://github.com/example/board/pull/412", "board PR #412"],
  ["a pull request's files page", "https://github.com/example/board/pull/412/files", "board PR #412"],
  ["an issue's comment", "https://github.com/example/board/issues/2360#issuecomment-99", "board issue #2360"],
  ["a CI run", "https://github.com/example/board/actions/runs/19283746", "board CI run 19283746"],
  ["a CI run's job", "https://github.com/example/board/actions/runs/19283746/job/55", "board CI run 19283746"],
  ["a review page's session", "http://127.0.0.1:4387/session/a1b2", "Review page"],
  ["a review page's session by name", "http://localhost:4387/session/a1b2#item-3", "Review page"],
] as const) {
  test(`${name} address reads as a chip with its short name and opens the whole address in a new tab`, () => {
    // Arrange, act
    const shown = html(`See ${address}.`);

    // Assert
    assert.equal(shown, `<p>See ${chip(address, label)}.</p>`);
  });
}

for (const [name, address] of [
  ["a repository page", "https://github.com/example/board"],
  ["a pull request list", "https://github.com/example/board/pulls"],
  ["a look-alike host", "https://github.com.example.org/example/board/pull/412"],
  ["a host that ends like GitHub's", "https://evilgithub.com/example/board/pull/412"],
  ["plain http", "http://github.com/example/board/pull/412"],
  ["a review page's look-alike host", "http://127.0.0.1.example.org:4387/session/a1b2"],
  ["another computer's session page", "http://192.0.2.20:4387/session/a1b2"],
  ["a review page's other path", "http://127.0.0.1:4387/sessions/a1b2"],
] as const) {
  test(`${name} address keeps its own words as its link`, () => {
    // Arrange, act
    const shown = html(address);

    // Assert
    assert.equal(shown, `<p>${raw(address)}</p>`);
  });
}

for (const [name, text, want] of [
  ["a Markdown link reads as its label, with its address in its tip", "Opened [the fix](https://github.com/example/board/pull/412).", `<p>Opened ${named("https://github.com/example/board/pull/412", "the fix")}.</p>`],
  ["a Markdown link whose label is its address reads as the short name", "[https://github.com/example/board/issues/7](https://github.com/example/board/issues/7)", `<p>${chip("https://github.com/example/board/issues/7", "board issue #7")}</p>`],
  ["a Markdown link keeps balanced parentheses in its address", "[Wiki](https://en.example.org/wiki/Go_(language)) page", `<p>${named("https://en.example.org/wiki/Go_(language)", "Wiki")} page</p>`],
  ["a bold Markdown link stays bold", "**[Report](https://example.com/r)**", `<p><strong>${named("https://example.com/r", "Report")}</strong></p>`],
  ["a Markdown link to another scheme stays text", "[run me](javascript:alert(1)) and [file](file:///C:/x)", "<p>[run me](javascript:alert(1)) and [file](file:///C:/x)</p>"],
  ["markup in a Markdown link's label is shown, never run", '[<img src=x onerror="alert(1)">](https://example.com/a)', `<p>${named("https://example.com/a", "&lt;img src=x onerror=&quot;alert(1)&quot;&gt;")}</p>`],
  ["a quote cannot break out of a link's address", 'https://example.com/a"onclick="alert(1)', `<p>${raw("https://example.com/a")}&quot;onclick=&quot;alert(1)</p>`],
  ["a backticked pull request reads as its short name", "`https://github.com/example/board/pull/9`", `<p>${chip("https://github.com/example/board/pull/9", "board PR #9")}</p>`],
] as const) {
  test(`${name}`, () => {
    // Arrange, act
    const shown = html(text);

    // Assert
    assert.equal(shown, want);
  });
}

test("a one-line summary keeps a Markdown link as written, so a History row still links its label to its address", () => {
  // Arrange, act
  const summary = plainMessage("Opened [the fix](https://github.com/example/board/pull/412) for **review**.");

  // Assert
  assert.equal(summary, "Opened [the fix](https://github.com/example/board/pull/412) for review.");
  assert.equal(renderToStaticMarkup(linkedText(summary)), 'Opened <a class="message-link" href="https://github.com/example/board/pull/412" target="_blank" rel="noreferrer" title="https://github.com/example/board/pull/412">the fix</a> for review.');
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
      const link = `<a class="message-link message-chip" href="${href}" target="_blank" rel="noreferrer" title="${review}">Review page</a>`;
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
  assert.equal(plainMessage("Run `goblins doctor` first"), "Run goblins doctor first");
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
const linked = (path: string, key: number, isIcon?: boolean) => createElement("a", { key, className: isIcon ? "icon" : undefined }, isIcon ? "" : path);

test("a local path in a card's text is drawn as a link, up to a space and without the punctuation that ends a sentence", () => {
  // Arrange
  const step = String.raw`Send the email in C:\dev\code-goblins\data\pd-proxy-primary\iproyal-refund.md.`;

  // Act
  const drawn = renderToStaticMarkup(createElement("p", null, ...pathElements(step, linked)));

  // Assert
  assert.equal(drawn, String.raw`<p>Send the email in <a>C:\dev\code-goblins\data\pd-proxy-primary\iproyal-refund.md</a>.</p>`);
});

// The board and the supervisor find the same paths in the same text, so a
// link opens the path its card names: internal/supervisor/card_paths.go
// holds this text too. A closing bracket stays with a path that opened it.
test("a path keeps a closing bracket it opened and drops one that ends its sentence, as the supervisor finds it", () => {
  // Arrange
  const step = String.raw`Install it in C:\apps\tool(v2), not (C:\apps\old).`;

  // Act
  const drawn = renderToStaticMarkup(createElement("p", null, ...pathElements(step, linked)));

  // Assert
  assert.equal(drawn, String.raw`<p>Install it in <a>C:\apps\tool(v2)</a>, not (<a>C:\apps\old</a>).</p>`);
});

test("a code value that is a whole path keeps its copy and gains the path's icon, spaces and all, and text with no path renderer is unchanged", () => {
  // Arrange
  const text = String.raw`Keep the notes in ` + "`" + String.raw`C:\Users\fpres\OneDrive - SIQstack\Desktop` + "`" + String.raw` or (D:/CodeGoblins/scratch), not https://example.com/C:\x or 3:4.`;

  // Act
  const withLinks = renderToStaticMarkup(messageElements(text, undefined, linked));
  const withoutLinks = html(text);

  // Assert
  assert.equal(withLinks, String.raw`<p>Keep the notes in <code>C:\Users\fpres\OneDrive - SIQstack\Desktop</code><a class="icon"></a> or (<a>D:/CodeGoblins/scratch</a>), not <a class="message-link" href="https://example.com/C:\x" target="_blank" rel="noreferrer">https://example.com/C:\x</a> or 3:4.</p>`);
  assert.equal(withoutLinks, String.raw`<p>Keep the notes in <code>C:\Users\fpres\OneDrive - SIQstack\Desktop</code> or (D:/CodeGoblins/scratch), not <a class="message-link" href="https://example.com/C:\x" target="_blank" rel="noreferrer">https://example.com/C:\x</a> or 3:4.</p>`);
});

test("a text is long past six lines or 600 characters, and a short one is not", () => {
  const cases: [string, string, boolean][] = [
    ["empty", "", false],
    ["one line", "Fix the swipe", false],
    ["six lines", "1\n2\n3\n4\n5\n6", false],
    ["seven lines", "1\n2\n3\n4\n5\n6\n7", true],
    ["seven Windows lines", "1\r\n2\r\n3\r\n4\r\n5\r\n6\r\n7", true],
    ["six lines and trailing blank lines", "1\n2\n3\n4\n5\n6\n\n\n", false],
    ["600 characters", "x".repeat(600), false],
    ["601 characters", "x".repeat(601), true],
  ];
  for (const [name, text, want] of cases) assert.equal(isLongText(text), want, name);
});

// The Overlord, 2026-11-02, of a goblin's question whose run IDs, finding
// codes and receipts filled his Command Center: "difficult to read, poorly
// formatted and full of gibberish". Its asker puts that evidence after a line
// that reads only "Details:", and the card folds it away; nothing else is
// split, so a question written before that line existed shows whole.
for (const [name, text, decision, details] of [
  ["a Details line", "Fix the parked review now?\n- **Recommended: fix it.** One bounded round.\n\nDetails:\n- registry-parser-divergence | error | tools/select_tests.py:279",
    "Fix the parked review now?\n- **Recommended: fix it.** One bounded round.", "- registry-parser-divergence | error | tools/select_tests.py:279"],
  ["trailing spaces after the Details line", "Ship it?\nDetails: \t \nrun 01M6EASKNZ3NNDFC6PWCBFHY7R", "Ship it?", "run 01M6EASKNZ3NNDFC6PWCBFHY7R"],
  ["a lowercase details line", "Ship it?\ndetails:\nrun 01M6EASKNZ3NNDFC6PWCBFHY7R", "Ship it?\ndetails:\nrun 01M6EASKNZ3NNDFC6PWCBFHY7R", ""],
  ["an indented Details line", "Ship it?\n Details:\nrun 01M6EASKNZ3NNDFC6PWCBFHY7R", "Ship it?\n Details:\nrun 01M6EASKNZ3NNDFC6PWCBFHY7R", ""],
  ["Windows line ends", "Ship it?\r\nDetails:\r\n- head f77bd7ee\r\n- tree 74fbbb32", "Ship it?", "- head f77bd7ee\n- tree 74fbbb32"],
  ["only the first Details line", "Ship it?\nDetails:\none\nDetails:\ntwo", "Ship it?", "one\nDetails:\ntwo"],
  ["no Details line", "Ship it?\n- risk high", "Ship it?\n- risk high", ""],
  ["Details inside a line", "Ship it? See Details: below.\n- **Details:** none", "Ship it? See Details: below.\n- **Details:** none", ""],
  ["nothing before the Details line", "Details:\n- run 01M6", "Details:\n- run 01M6", ""],
  ["nothing after the Details line", "Ship it?\nDetails:\n  \n", "Ship it?\nDetails:\n  \n", ""],
  ["an empty text", "", "", ""],
] as const) {
  test(`a question's decision and details split at ${name}`, () => {
    // Arrange, act
    const split = decisionAndDetails(text);

    // Assert
    assert.deepEqual(split, { decision, details });
  });
}

test("markup in a question's decision and evidence is shown as text, never interpreted", () => {
  // Arrange
  const { decision, details } = decisionAndDetails("Ship <b>it</b>?\nDetails:\n<img src=x onerror=\"alert(1)\"> `<script>alert(1)</script>`");

  // Act
  const shown = [html(decision), html(details)];

  // Assert
  assert.deepEqual(shown, ["<p>Ship &lt;b&gt;it&lt;/b&gt;?</p>", "<p>&lt;img src=x onerror=&quot;alert(1)&quot;&gt; <code>&lt;script&gt;alert(1)&lt;/script&gt;</code></p>"]);
});

test("literal text keeps every word as written and links each address by itself", () => {
  // Arrange
  const text = "gh pr view https://github.com/example/board/pull/412 [x](https://example.com/y) <b>";

  // Act
  const shown = renderToStaticMarkup(literalLinks(text));

  // Assert
  assert.equal(shown, `gh pr view ${raw("https://github.com/example/board/pull/412")} [x](${raw("https://example.com/y")}) &lt;b&gt;`);
});

for (const [name, sentence, want] of [
  ["each word the sentence says an address by", "Opened PR #12 and issue #7.", `Opened ${named("https://github.com/o/r/pull/12", "PR #12")} and ${named("https://github.com/o/r/issues/7", "issue #7")}.`],
  ["a word a capital starts", "PR #12 waits. Issue #7 is open.", `${named("https://github.com/o/r/pull/12", "PR #12")} waits. ${named("https://github.com/o/r/issues/7", "Issue #7")} is open.`],
  ["only the words a summary kept", "Opened PR #12…", `Opened ${named("https://github.com/o/r/pull/12", "PR #12")}…`],
] as const) {
  test(`a plain sentence links ${name}`, () => {
    // Arrange
    const links = [{ label: "PR #12", address: "https://github.com/o/r/pull/12" }, { label: "issue #7", address: "https://github.com/o/r/issues/7" }];

    // Act
    const shown = renderToStaticMarkup(linkedSentence(sentence, links));

    // Assert
    assert.equal(shown, want);
  });
}

const PR_235 = "https://github.com/example/board/pull/235";
const CI_RUN = "https://github.com/example/board/actions/runs/99";
const GUIDE = "https://example.org/guide";
for (const [name, sentence, link, want] of [
  ["a pull request whose number starts a longer one", "Rebased PR #2358 onto PR #235.", { label: "PR #235", address: PR_235 }, `Rebased PR #2358 onto ${named(PR_235, "PR #235")}.`],
  ["a CI run that starts a CI runner", "A CI runner took a CI run.", { label: "a CI run", address: CI_RUN }, `A CI runner took ${named(CI_RUN, "a CI run")}.`],
  ["a host that ends a longer host", "Read api.example.org, then example.org.", { label: "example.org", address: GUIDE }, `Read api.example.org, then ${named(GUIDE, "example.org")}.`],
  ["a host that starts a longer host", "Read example.org.uk, then example.org.", { label: "example.org", address: GUIDE }, `Read example.org.uk, then ${named(GUIDE, "example.org")}.`],
  ["words that stand only inside a longer number", "Rebased PR #2358.", { label: "PR #235", address: PR_235 }, "Rebased PR #2358."],
] as const) {
  test(`a plain sentence links a link's words only where they stand whole, never in ${name}`, () => {
    // Arrange, act
    const shown = renderToStaticMarkup(linkedSentence(sentence, [link]));

    // Assert
    assert.equal(shown, want);
  });
}
