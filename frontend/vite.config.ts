import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { defineConfig } from "vite";

// BOARD_DEV_PORT moves the dev server off 5173, which Docker and WSL also
// proxy on some machines, and BOARD_SUPERVISOR points it at another
// supervisor, such as an isolated example fixture on a free port.
const port = Number(process.env.BOARD_DEV_PORT) || 5173;
const supervisor = process.env.BOARD_SUPERVISOR || "http://127.0.0.1:4310";

export default defineConfig({
  build: {
    // goblins.exe embeds the board from here; git ignores it.
    outDir: "../internal/boardweb/dist/board",
    emptyOutDir: true,
    sourcemap: false,
    // The supervisor reads from it which files Vite named by their content,
    // which a browser may keep a year (#1966).
    manifest: true,
  },
  experimental: {
    // A file the build copies from public/assets keeps its name, so each
    // address of one the build writes, in the page, a style or a script
    // importing it, carries the digest of its content that the supervisor
    // checks before it lets a browser keep the file a year (#1966).
    renderBuiltUrl(filename, { type }) {
      if (type !== "public" || !filename.startsWith("assets/")) return undefined;
      const file = filename.split("?")[0];
      const content = readFileSync(new URL("public/" + file, import.meta.url));
      return "/" + file + "?v=" + createHash("sha256").update(content).digest("hex").slice(0, 16);
    },
  },
  server: {
    host: "127.0.0.1",
    port,
    strictPort: true,
    proxy: {
      "/api": {
        target: supervisor,
        changeOrigin: true,
        configure(proxy) {
          proxy.on("proxyReq", (outgoing, incoming) => {
            // Translate only this loopback development origin. Other origins
            // and Sec-Fetch-Site still reach the supervisor's strict checks.
            if (incoming.headers.origin === "http://127.0.0.1:" + port) {
              outgoing.setHeader("Origin", new URL(supervisor).origin);
            }
          });
        },
      },
    },
  },
});
