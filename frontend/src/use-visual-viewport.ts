import { useEffect } from "react";

// While isFollowing, the page's root carries the visual viewport's height and
// top as --visual-height and --visual-top. A phone's on-screen keyboard
// shrinks the visual viewport and leaves the layout viewport as it was, and
// Safari moves the visual viewport down the page to keep the focused field in
// sight, so what must stay above the keyboard is sized and placed by these.
export function useVisualViewport(isFollowing: boolean): void {
  useEffect(() => {
    const viewport = window.visualViewport;
    if (!isFollowing || !viewport) return;
    const root = document.documentElement.style;
    const follow = () => {
      root.setProperty("--visual-height", viewport.height + "px");
      root.setProperty("--visual-top", viewport.offsetTop + "px");
    };
    follow();
    viewport.addEventListener("resize", follow);
    viewport.addEventListener("scroll", follow);
    return () => {
      viewport.removeEventListener("resize", follow);
      viewport.removeEventListener("scroll", follow);
      root.removeProperty("--visual-height");
      root.removeProperty("--visual-top");
    };
  }, [isFollowing]);
}
