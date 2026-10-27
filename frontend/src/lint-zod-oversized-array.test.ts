import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import type * as Zod from "zod";

// The lint run loads zod through eslint-plugin-react-hooks. Through 4.6.5 an
// array schema with a .max() bound walked every element of an oversized input
// and built one issue per bad element before it reported the bound, so a large
// input could exhaust memory (GHSA-mrhx-c7xc-m3w5, #1455).
test("the zod the lint run loads rejects an oversized array before parsing its elements", () => {
  // Arrange
  const requireFromHooks = createRequire(createRequire(import.meta.url).resolve("eslint-plugin-react-hooks"));
  const { z }: typeof Zod = requireFromHooks("zod");
  const schema = z.array(z.string()).max(2);
  const oversized = Array.from({ length: 10_000 }, (_, index) => index);

  // Act
  const codes = schema.safeParse(oversized).error?.issues.map((issue) => issue.code);

  // Assert
  assert.deepEqual(codes, ["too_big"], `an oversized array raised ${codes?.length} issues, not its one size bound`);
});
