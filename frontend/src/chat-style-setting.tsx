import { useChatStyle, type ChatStyle } from "./chat-style";

// The two ways the board draws a conversation, as one radio group: picking one
// saves it for this browser and every open chat follows at once. It changes
// only how the conversation is drawn, never the harness session, the theme or
// anything the conversation holds.
const CHOICES: { style: ChatStyle; label: string; tip: string }[] = [
  { style: "classic", label: "Classic", tip: "The harness's own terminal, as it runs" },
  { style: "modern", label: "Modern", tip: "The conversation, with the terminal a tap away" },
];

export function ChatStyleSetting() {
  const [style, choose] = useChatStyle();
  return <div className="chat-style-setting">
    <strong>Chat</strong>
    <div className="chat-style-choices" role="radiogroup" aria-label="Chat style">
      {CHOICES.map((choice) => <label key={choice.style} className={"chat-style-choice" + (style === choice.style ? " chosen" : "")} data-tip={choice.tip}>
        <input type="radio" name="chat-style" value={choice.style} checked={style === choice.style} onChange={() => choose(choice.style)} />
        <span>{choice.label}</span>
      </label>)}
    </div>
  </div>;
}
