import { useCallback, useEffect, useState } from "react";

// How a conversation is drawn, kept in this browser as one setting for the
// CFO and for every goblin: Modern draws the shared chat view, which is the
// board's own conversation, and Classic draws the harness's raw terminal,
// which stays the same session and the same renderer either way.
export type ChatStyle = "modern" | "classic";

// The preference's key, beside the board's other browser-kept choices.
export const CHAT_STYLE_KEY = "cfo-chat-style";

// The name the board tells every open panel a choice changed under.
export const CHAT_STYLE_CHANGED = "cfo-chat-style-changed";

// chatStyle reads the saved choice, Classic where none was saved: every
// conversation opens on the harness's own terminal until Modern Chat is ready
// to be the default (the Overlord, 2026-10-28: "the classic terminal worked
// well ... It should be the default for now").
export function chatStyle(saved?: string | null): ChatStyle {
  return saved === "modern" ? "modern" : "classic";
}

// storedChatStyle is the choice this browser holds.
export function storedChatStyle(): ChatStyle {
  try {
    return chatStyle(localStorage.getItem(CHAT_STYLE_KEY));
  } catch {
    return "classic";
  }
}

// saveChatStyle keeps a choice for this browser and tells every open panel,
// so the control and the panel it sits beside never disagree.
export function saveChatStyle(style: ChatStyle): void {
  try {
    localStorage.setItem(CHAT_STYLE_KEY, style);
  } catch {
    // A browser that refuses storage keeps the choice until it is reloaded.
  }
  if (typeof window !== "undefined") window.dispatchEvent(new CustomEvent<ChatStyle>(CHAT_STYLE_CHANGED, { detail: style }));
}

// useChatStyle is the saved choice and the setter that saves it, with the
// panels of one board keeping up with each other.
export function useChatStyle(): [ChatStyle, (next: ChatStyle) => void] {
  const [style, setStyle] = useState<ChatStyle>(storedChatStyle);
  useEffect(() => {
    const listen = (event: Event) => {
      const detail = (event as CustomEvent<ChatStyle>).detail;
      setStyle(chatStyle(detail));
    };
    window.addEventListener(CHAT_STYLE_CHANGED, listen);
    return () => window.removeEventListener(CHAT_STYLE_CHANGED, listen);
  }, []);
  const choose = useCallback((next: ChatStyle) => {
    saveChatStyle(next);
    setStyle(next);
  }, []);
  return [style, choose];
}
