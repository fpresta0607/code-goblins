import assert from "node:assert/strict";
import { test } from "node:test";
import { scrawlLink } from "./scrawl-link.ts";

test("phone review links go through validation; local and external links stay intact", () => {
  const phone = { protocol: "https:", hostname: "pc.example.ts.net" } as Location;
  const local = { protocol: "http:", hostname: "127.0.0.1" } as Location;
  const review = "http://127.0.0.1:4387/session/existing?mode=notes#answer";
  assert.equal(scrawlLink(review, phone), "/api/remote/scrawl-link?url=" + encodeURIComponent(review));
  assert.equal(scrawlLink(review, local), review);
  for (const url of ["https://example.org/session/one", "http://127.0.0.1:4387/api/remote", "http://elsewhere.example:4387/session/one"]) assert.equal(scrawlLink(url, phone), url);
});
