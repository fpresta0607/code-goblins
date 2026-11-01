import { createContext } from "react";

export const DocumentContext = createContext<{ query: string; instance: string } | null>(null);
