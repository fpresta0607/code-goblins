import { createContext } from "react";

// What a conversation's rows open in its panel (#2362): its goblin's Screen
// view while that goblin's browser runs. A row offers only what is given, so
// a view the panel cannot show has no button at all.
export interface RowOpener { screen?: () => void }

export const RowOpener = createContext<RowOpener>({});
