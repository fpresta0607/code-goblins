import { defineConfig } from "vite";

// BOARD_DEV_PORT moves the dev server off 5173, which Docker and WSL also
// proxy on some machines, and BOARD_SUPERVISOR points it at another
// supervisor, such as an isolated example fixture on a free port.
const port = Number(process.env.BOARD_DEV_PORT) || 5173;
const supervisor = process.env.BOARD_SUPERVISOR || "http://127.0.0.1:4310";

export default defineConfig({
  build: {
    outDir: "../internal/boardweb/dist",
    emptyOutDir: true,
    sourcemap: false,
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
