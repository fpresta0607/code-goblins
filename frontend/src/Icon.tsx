// Line icons drawn on one 24px grid, so every glyph centres by construction
// instead of by a font's metrics. The OpenAI and Pi entries are plain glyphs:
// no official mark is available for them.
const PATHS = {
  "command-center": "M6.5 3.5h11a2.5 2.5 0 0 1 2.5 2.5v7.5a2.5 2.5 0 0 1-2.5 2.5H11l-4.5 4v-4a2.5 2.5 0 0 1-2.5-2.5V6a2.5 2.5 0 0 1 2.5-2.5ZM8.5 12.5l-.75-5 2.75 2L12 6.5l1.5 3 2.75-2-.75 5Z",
  close: "M6 6l12 12M18 6 6 18",
  check: "M5 12.5 9.5 17 19 7.5",
  "check-double": "M2 12.5 6.5 17 16 7.5M11.5 16l1 1L22 7.5",
  warning: "M10.3 4.5 2.8 17.6A2 2 0 0 0 4.5 20.5h15a2 2 0 0 0 1.7-2.9L13.7 4.5a2 2 0 0 0-3.4 0ZM12 9.5v4.5M12 17.2v.3",
  clock: "M12 3.5a8.5 8.5 0 1 0 0 17 8.5 8.5 0 0 0 0-17ZM12 7.5V12l3 2",
  refresh: "M19.5 12a7.5 7.5 0 1 1-2.2-5.3M19.5 4v4.5H15",
  mic: "M12 3.5a3 3 0 0 0-3 3v5a3 3 0 0 0 6 0v-5a3 3 0 0 0-3-3ZM5.5 11a6.5 6.5 0 0 0 13 0M12 17.5V21M8.5 21h7",
  chevron: "m9 6 6 6-6 6",
  external: "M14 4h6v6M20 4l-8 8M10 5H6a2 2 0 0 0-2 2v11a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2v-4",
  folder: "M3.5 7a2 2 0 0 1 2-2H9l2 2h7.5a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2Z",
  "pull-request": "M6 4a2 2 0 1 0 0 4 2 2 0 0 0 0-4ZM6 16a2 2 0 1 0 0 4 2 2 0 0 0 0-4ZM18 16a2 2 0 1 0 0 4 2 2 0 0 0 0-4ZM6 8v8M18 16v-6a3 3 0 0 0-3-3h-4M13 4.5 10.5 7 13 9.5",
  terminal: "M4.5 5h15A1.5 1.5 0 0 1 21 6.5v11a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 17.5v-11A1.5 1.5 0 0 1 4.5 5ZM7 10l2.5 2L7 14M12 15h5",
  plus: "M12 5v14M5 12h14",
  minus: "M5 12h14",
  fit: "M4 9V5a1 1 0 0 1 1-1h4M15 4h4a1 1 0 0 1 1 1v4M20 15v4a1 1 0 0 1-1 1h-4M9 20H5a1 1 0 0 1-1-1v-4",
  maximize: "M4 9V4h5M4 4l6 6M20 9V4h-5M20 4l-6 6M4 15v5h5M4 20l6-6M20 15v5h-5M20 20l-6-6",
  restore: "M4 10h6V4M4 4l6 6M20 10h-6V4M20 4l-6 6M4 14h6v6M4 20l6-6M20 14h-6v6M20 20l-6-6",
  arrange: "M4.5 4.5h6v6h-6zM13.5 4.5h6v6h-6zM4.5 13.5h6v6h-6zM13.5 13.5h6v6h-6z",
  key: "M14.5 4a5.5 5.5 0 1 0 0 11 5.5 5.5 0 0 0 0-11ZM10.6 13 3.5 20.1M5.5 18.1l2 2M8 15.6l2 2",
  plug: "M9 3v5M15 3v5M6.5 8h11v3.5a5.5 5.5 0 0 1-11 0ZM12 17v4",
  database: "M12 3.5c4.4 0 8 1.3 8 3s-3.6 3-8 3-8-1.3-8-3 3.6-3 8-3ZM4 6.5v11c0 1.7 3.6 3 8 3s8-1.3 8-3v-11M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3",
  "browser-check": "M4.5 4.5h15A1.5 1.5 0 0 1 21 6v12a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 18V6a1.5 1.5 0 0 1 1.5-1.5ZM3 9h18M8.5 14.5l2.2 2.2 4.8-4.7",
  book: "M4 5.5A1.5 1.5 0 0 1 5.5 4H11v16H5.5A1.5 1.5 0 0 1 4 18.5ZM20 5.5A1.5 1.5 0 0 0 18.5 4H13v16h5.5a1.5 1.5 0 0 0 1.5-1.5Z",
  openai: "M12 3 19.8 7.5v9L12 21l-7.8-4.5v-9ZM12 8v8M8.5 10l7 4M15.5 10l-7 4",
  pi: "M5 7h14M9.5 7v11M14.5 7v8.5a2.5 2.5 0 0 0 2.5 2.5",
  sparkle: "M12 3.5 13.9 10.1 20.5 12 13.9 13.9 12 20.5 10.1 13.9 3.5 12 10.1 10.1Z",
  task: "M10 6h10M10 12h10M10 18h10M3.5 6l1.5 1.5L7.5 5M3.5 12l1.5 1.5 2.5-2.5M3.5 18l1.5 1.5 2.5-2.5",
  back: "M19 12H5M11 6l-6 6 6 6",
  play: "M8 5.5v13l11-6.5Z",
  shield: "M12 3.5 19 6v5.5c0 4.2-2.9 7.9-7 9-4.1-1.1-7-4.8-7-9V6Z",
  copy: "M9 9h10v11H9ZM5 15V4h10",
  next: "M5 12h14M13 6l6 6-6 6",
  comment: "M5 4.5h14A1.5 1.5 0 0 1 20.5 6v9a1.5 1.5 0 0 1-1.5 1.5h-7l-4.5 3.5v-3.5H5A1.5 1.5 0 0 1 3.5 15V6A1.5 1.5 0 0 1 5 4.5Z",
  send: "M20.5 3.5 10.5 13.5M20.5 3.5l-6.5 17-3.5-7-7-3.5Z",
  images: "M7.5 3.5h12a1 1 0 0 1 1 1v12a1 1 0 0 1-1 1h-12a1 1 0 0 1-1-1v-12a1 1 0 0 1 1-1ZM3.5 7.5v12a1 1 0 0 0 1 1h12M6.5 14l4-4 3 3 2-2 5 5M15 7.5h.01",
  question: "M12 3.5a8.5 8.5 0 1 0 0 17 8.5 8.5 0 0 0 0-17ZM9.5 9.5a2.5 2.5 0 1 1 3.5 2.3c-.6.3-1 .8-1 1.5v.7M12 16.8v.2",
} as const;

export type IconName = keyof typeof PATHS;

export function Icon({ name }: { name: IconName }) {
  return <svg className="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={PATHS[name]} /></svg>;
}
