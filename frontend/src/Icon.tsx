// Line icons drawn on one 24px grid, so every glyph centres by construction
// instead of by a font's metrics.
const PATHS = {
  "command-center": "M6.5 3.5h11a2.5 2.5 0 0 1 2.5 2.5v7.5a2.5 2.5 0 0 1-2.5 2.5H11l-4.5 4v-4a2.5 2.5 0 0 1-2.5-2.5V6a2.5 2.5 0 0 1 2.5-2.5ZM8.5 12.5l-.75-5 2.75 2L12 6.5l1.5 3 2.75-2-.75 5Z",
  close: "M6 6l12 12M18 6 6 18",
} as const;

export type IconName = keyof typeof PATHS;

export function Icon({ name }: { name: IconName }) {
  return <svg className="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={PATHS[name]} /></svg>;
}
