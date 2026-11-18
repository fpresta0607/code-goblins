import { createContext } from "react";

// A pasted text a chip opens (#2404): its name, and the address its text is
// read back from.
export interface OpenedPaste { name: string; source: string }

// How a pasted text's chip opens its text in the one window of its
// conversation, which the conversation's view holds, so the window stays
// open while the row whose chip opened it leaves the rows drawn or a queued
// message gives way to its row in the record. Every chip is in a
// conversation's view.
export const PastedTextOpener = createContext<(paste: OpenedPaste) => void>(() => {});
