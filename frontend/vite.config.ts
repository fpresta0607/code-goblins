import { defineConfig } from "vite";

export default defineConfig({
  build: {
    outDir: "../internal/boardweb/dist",
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:4310",
        changeOrigin: true,
        configure(proxy) {
          proxy.on("proxyReq", (outgoing, incoming) => {
            // Translate only this loopback development origin. Other origins
            // and Sec-Fetch-Site still reach the supervisor's strict checks.
            if (incoming.headers.origin === "http://127.0.0.1:5173") {
              outgoing.setHeader("Origin", "http://127.0.0.1:4310");
            }
          });
        },
      },
    },
  },
});
