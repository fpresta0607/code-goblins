import type { Terminal } from "@xterm/xterm";
import { WebglAddon } from "@xterm/addon-webgl";
import { isWebKit } from "./terminalStream";

// drawOnGpu has xterm draw a terminal with WebGL where it can. In Playwright's
// WebKit that renderer drew the grid as nothing while the DOM renderer drew
// the same output, and WebKit is the engine of every browser on the Overlord's
// iPhone, whose terminals were black over Tailscale (2026-10-18). So in WebKit,
// as where WebGL is missing, xterm keeps drawing with its DOM renderer.
export function drawOnGpu(terminal: Terminal): void {
  if (isWebKit(navigator.userAgent)) return;
  try {
    const gpu = new WebglAddon();
    gpu.onContextLoss(() => gpu.dispose());
    terminal.loadAddon(gpu);
  } catch {
    // No WebGL here: xterm keeps drawing with its DOM renderer.
  }
}
