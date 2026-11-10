import { createContext } from "react";

// How a picture in a conversation's row opens in that conversation's viewer,
// by the address the row draws it from. Outside a conversation's rows there
// is none, and a picture opens among those of its own message.
export const PictureOpener = createContext<((src: string) => void) | null>(null);
