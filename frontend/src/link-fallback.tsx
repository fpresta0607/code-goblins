import { useEffect, useState } from "react";
import { CopyValue } from "./CopyValue";
import { Notice } from "./notice";

// The event the desktop window raises in the board's page when a link the
// page asked it to open could not be opened (linkFailedEvent in
// cmd/goblins-window/links.go), with the link's address.
const LINK_FAILED = "code-goblins-link-failed";

// LinkFallback says a link could not be opened and shows its address to copy
// (#2360), until he closes it or another link fails.
export function LinkFallback() {
  const [address, setAddress] = useState("");
  useEffect(() => {
    const failed = (event: Event) => {
      if (event instanceof CustomEvent && typeof event.detail === "string" && event.detail) setAddress(event.detail);
    };
    window.addEventListener(LINK_FAILED, failed);
    return () => window.removeEventListener(LINK_FAILED, failed);
  }, []);
  if (!address) return null;
  return <Notice form="box" tone="warning" className="link-fallback" actions={<button type="button" onClick={() => setAddress("")}>Close</button>}>
    That link could not be opened. Its address, to copy: <CopyValue value={address} isPlain />
  </Notice>;
}
