// Keep local reviews local. The board validates the saved Scrawl endpoint
// before redirecting a phone to its separately authenticated HTTPS origin.
export function scrawlLink(raw: string, board = typeof location === "undefined" ? undefined : location): string {
  if (!board || board.protocol !== "https:" || !board.hostname.endsWith(".ts.net")) return raw;
  try {
    const url = new URL(raw);
    const host = url.hostname.replace(/^\[|\]$/g, "");
    const ip = host.split(".").map(Number);
    const tailIP = ip.length === 4 && ip.every((part) => Number.isInteger(part) && part >= 0 && part <= 255) && ip[0] === 100 && ip[1] >= 64 && ip[1] <= 127 || host.startsWith("fd7a:115c:a1e0:");
    const localIP = host === "::1" || ip.length === 4 && ip[0] === 127 && ip.every((part) => Number.isInteger(part) && part >= 0 && part <= 255);
    if ((url.protocol === "http:" && (localIP || tailIP || ["localhost", board.hostname].includes(host)) || url.protocol === "https:" && host === board.hostname && url.port !== "4311" && url.port !== "4312") && url.pathname.startsWith("/session/")) return "/api/remote/scrawl-link?url=" + encodeURIComponent(raw);
  } catch { /* Existing non-URL presentation is unchanged. */ }
  return raw;
}
