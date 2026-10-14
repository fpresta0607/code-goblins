import { useEffect, useState } from "react";
import "./page-preview.css";

// A Scrawl page's preview, live and scaled down, from its own file: a preview
// to look at, never a review window of its own. The export takes a few
// seconds, so a light sweeps a skeleton of a page in the frame until the page
// has loaded, and a page that cannot be shown says so. A frame fires load for an error page
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
    {!isFailed && !isReady && <div className="page-loading" role="status">
      <span className="page-bars" aria-hidden="true"><i /><i /><i /><i /></span>
      <span className="page-sweep" aria-hidden="true" />
      <span className="page-loading-line">Loading the page</span>
    </div>}
    {isFailed && <div className="page-failed" role="status"><span>Page unavailable</span></div>}
  </div>;
}
