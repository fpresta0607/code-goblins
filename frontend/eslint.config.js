import js from "@eslint/js";
import tseslint from "typescript-eslint";
import hooks from "eslint-plugin-react-hooks";

export default tseslint.config(
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["src/**/*.{ts,tsx}"],
    plugins: { "react-hooks": hooks },
    rules: {
      ...hooks.configs.recommended.rules,
      "@typescript-eslint/no-explicit-any": "error",
    },
  },
  {
    // A browser test's pages are served by tests/site.ts, so a spec takes
    // test and expect from there; taken from Playwright itself, its pages
    // would have nothing answering for them.
    files: ["tests/**/*.spec.ts"],
    rules: {
      "no-restricted-imports": ["error", { paths: [{ name: "@playwright/test", message: "Import from ./site, which serves the pages the test loads." }] }],
    },
  },
);
