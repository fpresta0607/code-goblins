import { useEffect, useState } from "react";
import { Avatar } from "./Avatar";
import "./page-preview.css";

// A Scrawl page's preview, live and scaled down, from its own file: a preview
// to look at, never a review window of its own. The export takes a few
// seconds, so a small goblin hops in the frame until the page has loaded, and
// a page that cannot be shown says so. A frame fires load for an error page
// too and its document cannot be read here, so one request beside the frame's
// own, cancelled once its status is known, tells a page from a refusal.
export function PagePreview({ src }: { src: string }) {
  const [isFrameLoaded, setIsFrameLoaded] = useState(false);
  const [probe, setProbe] = useState<"pending" | "ok" | "failed">("pending");
  useEffect(() => {
    const controller = new AbortController();
    fetch(src, { signal: controller.signal, cache: "no-store" }).then(
      (response) => { setProbe(response.ok ? "ok" : "failed"); controller.abort(); },
      () => { if (!controller.signal.aborted) setProbe("failed"); },
    );
    return () => controller.abort();
  }, [src]);
  const isFailed = probe === "failed";
  const isReady = isFrameLoaded && probe === "ok";
  return <div className="page-preview">
    {!isFailed && <iframe src={src} title="" aria-hidden="true" tabIndex={-1} sandbox="allow-scripts" scrolling="no" loading="lazy" onLoad={() => setIsFrameLoaded(true)} />}
    {!isFailed && !isReady && <div className="page-loading" role="status"><Avatar persona="general" /><span>Loading the page</span></div>}
    {isFailed && <div className="page-failed" role="status"><span>The page could not be loaded</span></div>}
  </div>;
}
