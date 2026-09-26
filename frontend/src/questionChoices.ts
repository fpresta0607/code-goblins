import type { Action, Question } from "./types.ts";
import { chosenOption, questionOutcome } from "./commandQueue.ts";

export function questionSelection(question: Question, draft?: {selection:string; written:string}, receipt?: Action) {
  if (questionOutcome(question) === "answered") return { selection: question.answer_kind === "other" ? "other" : "option:"+chosenOption(question), written: question.answer_kind === "other" ? question.answer : "" };
  if ((receipt?.kind === "cfo_answer" || receipt?.kind === "goblin_answer") && receipt.question_id === question.id && receipt.generation === question.identity) return { selection: receipt.answer_kind === "other" ? "other" : "option:"+receipt.text, written: receipt.answer_kind === "other" ? receipt.text : "" };
  return question.status === "pending" && draft ? draft : { selection:"", written:"" };
}

// A goblin's own leading letter: "A) ", "b. ", "(c) " or "D: ".
const OWN_LETTER = /^\s*\(?([A-Ha-h])[).:]\s+/;

// The goblin's own letters are dropped only when every option carries one and
// they run A, B, C in the goblin's order; otherwise they mean something.
function optionTexts(options: string[]): Map<string, string> {
  const inOrder = options.every((option, i) => option.match(OWN_LETTER)?.[1].toUpperCase() === String.fromCharCode(65 + i));
  return new Map(options.map((option) => [option, inOrder ? option.replace(OWN_LETTER, "") : option]));
}

export function questionChoices(question: Question) {
  const options = [...question.options];
  const index = options.indexOf(question.recommended);
  if (index > 0) options.unshift(...options.splice(index, 1));
  const texts = optionTexts(question.options);
  // Moving the recommendation first reorders the choices, so each keeps the
  // image the goblin attached to it by its original position. The value stays
  // the goblin's option word for word; only its shown text loses the letter.
  return options.map((value, i) => ({ value, text: texts.get(value) || value, label: String.fromCharCode(65 + i), recommended: value === question.recommended, image: question.image_count ? "/api/questions/" + encodeURIComponent(question.id) + "/images/" + question.options.indexOf(value) : "" }));
}

export function questionAnswer(question: Question, selection: string, written: string) {
  const other = selection === "other";
  const text = other ? written : selection.startsWith("option:") ? selection.slice(7) : "";
  if (!text.trim() || (!other && !question.options.includes(text))) return null;
  return { kind: question.task ? "goblin_answer" : "cfo_answer", question_id: question.id, generation: question.identity, answer_kind: other ? "other" : "option", text };
}
