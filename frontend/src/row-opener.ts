import { createContext } from "react";

// What a conversation's rows open in its panel (#2362): the session's
// terminal, where it has one to show in the conversation's place, and its
// goblin's Screen view while that goblin's browser runs. A row offers only
// what is given, so a view the panel cannot show has no button at all.
export interface RowOpener { terminal?: () => void; screen?: () => void }

export const RowOpener = createContext<RowOpener>({});
