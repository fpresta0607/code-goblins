import { useState, type ReactNode } from "react";
import { Icon } from "./Icon";

// A picture of a conversation, or a quiet mark in its place when it cannot
// be read or drawn, never the browser's broken image: one whose conversation
// was replaced, whose file is gone or whose bytes are no picture. The mark
// stands for the address that failed, so another address draws its picture.
// isLazy has it load only as it nears the screen. children are the words the
// mark says where it has room for them.
export function PictureImage({ src, alt, isLazy = false, onClick, children }: {
  src: string; alt: string; isLazy?: boolean; onClick?: () => void; children?: ReactNode;
}) {
  const [unread, setUnread] = useState("");
  if (unread === src) return <span className="picture-missing"><Icon name="images" />{children}</span>;
  return <img src={src} alt={alt} loading={isLazy ? "lazy" : undefined} decoding="async" draggable={false} onClick={onClick} onError={() => setUnread(src)} />;
}
