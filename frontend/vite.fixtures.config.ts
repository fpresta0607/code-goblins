import { readdirSync } from "node:fs";
import path from "node:path";
import { defineConfig } from "vite";

const root = import.meta.dirname;
const fixtures = "tests/fixtures";

// The pages the browser tests load, built once before they run: the board
// itself and every page under tests/fixtures, bundled as the shipped board is,
// into a folder the tests read. They run with no web server, so no page load
// depends on a socket.
export default defineConfig({
  root,
  build: {
    outDir: "test-site",
    emptyOutDir: true,
    sourcemap: false,
    rollupOptions: {
      input: ["index.html", ...readdirSync(path.join(root, fixtures)).filter((name) => name.endsWith(".html")).map((name) => fixtures + "/" + name)],
    },
  },
});
