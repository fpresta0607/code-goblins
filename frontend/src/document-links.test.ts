import assert from "node:assert/strict";
import { test } from "node:test";
import { documentLinks } from "./document-links.ts";

for (const target of ["C:/output/candidate.pdf", String.raw`C:\output\candidate name.pdf`, "C:/output/Caf%C3%A9%20candidate%20(2).PDF", "C:/output/candidate%25final.pdf"]) {
  test(`document links preserve the label, target and adjacent punctuation for ${target}`, () => {
    const text = `Before [Retail PDF](${target}): after.`;
    assert.deepEqual(documentLinks(text), [{ start: 7, end: text.length - 8, label: "Retail PDF", target }]);
  });
}

for (const target of ["javascript:alert(1).pdf", "data:application/pdf;base64,AA", "file:///C:/private.pdf", "https://example.com/file.pdf", "//host/share/file.pdf", "C:/bad%ZZ.pdf", "C:/output/active.html", "C:/bad\nfile.pdf"]) {
  test(`unsupported or active document target stays literal: ${target}`, () => {
    assert.deepEqual(documentLinks(`[Example](${target})`), []);
  });
}

test("unclosed PDF links and Markdown images stay literal", () => {
  assert.deepEqual(documentLinks("[Unclosed](C:/report.pdf ![Picture](C:/picture.pdf)"), []);
});
